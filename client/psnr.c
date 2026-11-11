/*
 * psnr.c - see psnr.h. The socket is nonblocking after connect; psnr_poll
 * reads whatever has arrived, cuts complete frames into a FIFO, and hands
 * them out one at a time.
 */
#include "psnr.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#ifdef _WIN32
#  if !defined(_WIN32_WINNT) || _WIN32_WINNT < 0x0600
#    undef _WIN32_WINNT
#    define _WIN32_WINNT 0x0600   /* WSAPoll, GetTickCount64 (MinGW defaults older) */
#  endif
#  define WIN32_LEAN_AND_MEAN
#  include <winsock2.h>
#  include <ws2tcpip.h>
#  ifdef _MSC_VER
#    pragma comment(lib, "ws2_32.lib")
#  endif
   typedef SOCKET sock_t;
#  define BAD_SOCK INVALID_SOCKET
#  define sock_close closesocket
#  define WOULD_BLOCK() (WSAGetLastError() == WSAEWOULDBLOCK)
#  define CONNECT_PENDING() (WSAGetLastError() == WSAEWOULDBLOCK)   /* non-blocking connect in progress */
#  define poll WSAPoll
   typedef WSAPOLLFD pollfd_t;
#else
#  include <sys/socket.h>
#  include <netdb.h>
#  include <netinet/in.h>
#  include <netinet/tcp.h>
#  include <fcntl.h>
#  include <unistd.h>
#  include <errno.h>
#  include <poll.h>
#  include <ifaddrs.h>
#  include <net/if.h>
   typedef int sock_t;
#  define BAD_SOCK (-1)
#  define sock_close close
#  define WOULD_BLOCK() (errno == EAGAIN || errno == EWOULDBLOCK)
#  define CONNECT_PENDING() (errno == EINPROGRESS)
   typedef struct pollfd pollfd_t;
#endif

#define HEARTBEAT_SECS 20
#define RTT_SAMPLES    8       /* completed calls the reported round trip is the smallest of */
#define RTT_MAX_MS     60000   /* the server ignores more; so do we */

static uint64_t now_ms(void)
{
#ifdef _WIN32
    return GetTickCount64();
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000u + (uint64_t)ts.tv_nsec / 1000000u;
#endif
}

typedef struct node { psnr_msg m; struct node* next; } node;

struct psnr_client {
    sock_t   s;
    uint32_t next_req;
    uint8_t* rx;          /* bytes received, not yet framed */
    size_t   rx_len, rx_cap;
    node    *head, *tail; /* framed messages waiting for psnr_poll */
    time_t   last_send;
    int      dead;
    uint32_t user_id, token;              /* from HELLO_ACK; UDP probes carry them */
    int      relay;                       /* HELLO_ACK: the server relays */
    uint8_t  server_ip[4], local_ip[4];   /* the TCP connection's two ends */
    uint16_t server_port;
    uint16_t rtt[RTT_SAMPLES];            /* round trips of recent psnr_call replies, in ms */
    int      rtt_n, rtt_next;
    int      report_rtt;                  /* send the smallest in HEARTBEAT; off until psnr_report_rtt(c, 1) */
    int      dead_reason;                 /* enum psnr_dead_reason, set with dead */
    int      dead_os_err;                 /* the OS error at that point, 0 if none */
};

static int os_error(void)
{
#ifdef _WIN32
    return WSAGetLastError();
#else
    return errno;
#endif
}

/* The first failure sticks: a client is dead once, with the reason of the first thing that broke. */
static void mark_dead(psnr_client* c, int reason, int os_err)
{
    if (c->dead) return;
    c->dead = 1;
    c->dead_reason = reason;
    c->dead_os_err = os_err;
}

static int send_all(psnr_client* c, const uint8_t* p, size_t n)
{
    uint64_t t0 = 0;   /* when the socket first refused data in this call */
    while (n) {
        int k = send(c->s, (const char*)p, (int)n, 0);
        if (k < 0) {
            pollfd_t pf;
            uint64_t waited, rest;
            if (!WOULD_BLOCK()) { mark_dead(c, PSNR_DEAD_IO_ERROR, os_error()); return -1; }
            if (!t0) t0 = now_ms();
            waited = now_ms() - t0;
            if (waited >= PSNR_SEND_TIMEOUT_MS) { mark_dead(c, PSNR_DEAD_SEND_TIMEOUT, 0); return -1; }
            rest = PSNR_SEND_TIMEOUT_MS - waited;
            pf.fd = c->s; pf.events = POLLOUT; pf.revents = 0;
            poll(&pf, 1, (int)(rest < 1000 ? rest : 1000));
            continue;
        }
        p += k;
        n -= (size_t)k;
    }
    c->last_send = time(NULL);
    return 0;
}

uint32_t psnr_send(psnr_client* c, uint8_t type, const void* body, uint32_t len)
{
    uint8_t hdr[7];
    if (c->dead || len > 0xFFFF - 4) return 0;
    if (++c->next_req == 0) c->next_req = 1;   /* 0 means "push" */
    hdr[0] = type;
    psnr_put16(hdr + 1, (uint16_t)(len + 4));
    psnr_put32(hdr + 3, c->next_req);
    if (send_all(c, hdr, 7) || (len && send_all(c, (const uint8_t*)body, len))) return 0;
    return c->next_req;
}

/* Pull what the socket has and frame it. -1 once the connection is gone. */
static int pump(psnr_client* c)
{
    for (;;) {
        if (c->rx_cap - c->rx_len < 4096) {
            size_t cap = c->rx_cap ? c->rx_cap * 2 : 8192;
            uint8_t* grown = (uint8_t*)realloc(c->rx, cap);
            if (!grown) { mark_dead(c, PSNR_DEAD_NOMEM, os_error()); break; }
            c->rx = grown;
            c->rx_cap = cap;
        }
        int k = recv(c->s, (char*)c->rx + c->rx_len, (int)(c->rx_cap - c->rx_len), 0);
        if (k == 0) { mark_dead(c, PSNR_DEAD_PEER_CLOSED, 0); break; }
        if (k < 0) {
            if (!WOULD_BLOCK()) mark_dead(c, PSNR_DEAD_IO_ERROR, os_error());
            break;
        }
        c->rx_len += (size_t)k;
    }

    size_t off = 0;
    while (c->rx_len - off >= 3) {
        uint8_t  type = c->rx[off];
        uint16_t n = psnr_get16(c->rx + off + 1);
        if (c->rx_len - off < 3u + n) break;
        const uint8_t* p = c->rx + off + 3;

        /* Replies (0x8x, 0x9x, 0xBx) start with the request id; pushes (0xAx) don't. */
        int has_req = (type & 0xF0) != 0xA0;
        if (has_req && n < 4) {   /* a reply with no request id is damaged: drop it, do not pass it off as a push */
            off += 3u + n;
            continue;
        }
        node* nd = (node*)calloc(1, sizeof(node));
        if (!nd) { mark_dead(c, PSNR_DEAD_NOMEM, os_error()); break; }
        if (has_req) { nd->m.req = psnr_get32(p); p += 4; n -= 4; }
        nd->m.type = type;
        nd->m.len = n;
        nd->m.data = (uint8_t*)malloc(n ? n : 1);
        if (!nd->m.data) { mark_dead(c, PSNR_DEAD_NOMEM, os_error()); free(nd); break; }
        memcpy(nd->m.data, p, n);
        if (c->tail) c->tail->next = nd; else c->head = nd;
        c->tail = nd;
        off += 3u + psnr_get16(c->rx + off + 1);
    }
    memmove(c->rx, c->rx + off, c->rx_len - off);
    c->rx_len -= off;
    return c->dead ? -1 : 0;
}

static int take(psnr_client* c, psnr_msg* out, uint32_t want_req)
{
    node *prev = NULL, *nd = c->head;
    for (; nd; prev = nd, nd = nd->next)
        if (!want_req || nd->m.req == want_req) break;
    if (!nd) return 0;
    if (prev) prev->next = nd->next; else c->head = nd->next;
    if (c->tail == nd) c->tail = prev;
    *out = nd->m;
    free(nd);
    return 1;
}

int psnr_rtt_ms(const psnr_client* c)
{
    int i, best = -1;
    for (i = 0; i < c->rtt_n; i++)
        if (best < 0 || c->rtt[i] < best) best = c->rtt[i];
    return best;
}

void psnr_report_rtt(psnr_client* c, int on)
{
    c->report_rtt = on != 0;
}

uint32_t psnr_heartbeat(psnr_client* c)
{
    uint8_t body[2];
    int ms = c->report_rtt ? psnr_rtt_ms(c) : -1;
    if (ms < 0) return psnr_send(c, PSNR_HEARTBEAT, NULL, 0);   /* as before: no body */
    psnr_put16(body, (uint16_t)ms);
    return psnr_send(c, PSNR_HEARTBEAT, body, sizeof(body));
}

int psnr_poll(psnr_client* c, psnr_msg* out)
{
    if (!c->dead && time(NULL) - c->last_send >= HEARTBEAT_SECS)
        psnr_heartbeat(c);
    if (!c->dead) pump(c);
    if (take(c, out, 0)) return 1;
    return c->dead ? -1 : 0;
}

int psnr_call(psnr_client* c, uint8_t type, const void* body, uint32_t len,
              psnr_msg* reply, int timeout_ms)
{
    uint64_t t0 = now_ms();
    uint32_t req = psnr_send(c, type, body, len);
    uint64_t end = t0 + (uint64_t)timeout_ms;
    if (!req) return -1;
    for (;;) {
        pollfd_t pf;
        if (!c->dead) pump(c);
        if (take(c, reply, req)) {
            uint64_t ms = now_ms() - t0;   /* send to reply: this player's round trip, server time included */
            if (ms <= RTT_MAX_MS) {
                c->rtt[c->rtt_next] = (uint16_t)ms;
                c->rtt_next = (c->rtt_next + 1) % RTT_SAMPLES;
                if (c->rtt_n < RTT_SAMPLES) c->rtt_n++;
            }
            return 1;
        }
        if (c->dead) return -1;
        if (now_ms() >= end) return 0;
        pf.fd = c->s; pf.events = POLLIN; pf.revents = 0;
        poll(&pf, 1, 10);
    }
}

void psnr_msg_free(psnr_msg* m)
{
    free(m->data);
    m->data = NULL;
}

static int s_connect_error;

static void set_nonblock(sock_t s)
{
#ifdef _WIN32
    u_long nb = 1;
    ioctlsocket(s, FIONBIO, &nb);
#else
    fcntl(s, F_SETFL, fcntl(s, F_GETFL, 0) | O_NONBLOCK);
#endif
}

/* Milliseconds left until deadline, 0 once it has passed. */
static int remaining_ms(uint64_t deadline)
{
    uint64_t now = now_ms();
    return now < deadline ? (int)(deadline - now) : 0;
}

/* connect() that waits at most timeout_ms for the handshake. 0 when connected. The socket is
 * left non-blocking. */
static int connect_bounded(sock_t s, const struct addrinfo* ai, int timeout_ms)
{
    pollfd_t pf;
    int err = 0;
    socklen_t el = sizeof(err);
    set_nonblock(s);
    if (connect(s, ai->ai_addr, (int)ai->ai_addrlen) == 0) return 0;
    if (!CONNECT_PENDING()) return -1;
    pf.fd = s; pf.events = POLLOUT; pf.revents = 0;
    if (poll(&pf, 1, timeout_ms) <= 0) return -1;
    if (getsockopt(s, SOL_SOCKET, SO_ERROR, (char*)&err, &el) != 0 || err) return -1;
    return 0;
}

/* timeout_ms < 0 is psnr_connect: the blocking connect it has always made, then the 5 s HELLO
 * wait. Otherwise the connect and the HELLO wait share the one budget of timeout_ms. */
static psnr_client* connect_impl(const char* host, uint16_t port,
                                 const char* comm_id, const char* online_id, uint16_t p2p_port,
                                 uint32_t* out_user_id, uint8_t out_public_ip[4], int timeout_ms)
{
    struct addrinfo hints, *res = NULL, *ai;
    char portstr[8];
    sock_t s = BAD_SOCK;
    uint64_t deadline = now_ms() + (uint64_t)(timeout_ms > 0 ? timeout_ms : 0);

    /* The wire fields are 12 and 16 bytes: refuse a longer name instead of cutting it. */
    if (!host || !comm_id || !online_id || strlen(comm_id) > PSNR_COMM_ID_MAX || strlen(online_id) > PSNR_ONLINE_ID_MAX) return NULL;

#ifdef _WIN32
    WSADATA w;
    WSAStartup(MAKEWORD(2, 2), &w);
#endif
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_INET;
    hints.ai_socktype = SOCK_STREAM;
    snprintf(portstr, sizeof(portstr), "%u", port);
    if (getaddrinfo(host, portstr, &hints, &res) != 0) return NULL;
    for (ai = res; ai; ai = ai->ai_next) {
        s = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
        if (s == BAD_SOCK) continue;
        if (timeout_ms < 0 ? connect(s, ai->ai_addr, (int)ai->ai_addrlen) == 0
                           : connect_bounded(s, ai, remaining_ms(deadline)) == 0) break;
        sock_close(s);
        s = BAD_SOCK;
    }
    freeaddrinfo(res);
    if (s == BAD_SOCK) return NULL;

    {   /* small request/reply frames: don't let Nagle sit on them */
        int one = 1;
        setsockopt(s, IPPROTO_TCP, TCP_NODELAY, (const char*)&one, sizeof(one));
    }
    set_nonblock(s);

    psnr_client* c = (psnr_client*)calloc(1, sizeof(psnr_client));
    if (!c) { sock_close(s); return NULL; }
    c->s = s;
    {   /* UDP goes to the same server address and port; the local end is
         * this machine's address on the way to the server, for peers on the
         * same network. */
        struct sockaddr_in a;
        socklen_t n = sizeof(a);
        if (getpeername(s, (struct sockaddr*)&a, &n) == 0) {
            memcpy(c->server_ip, &a.sin_addr, 4);
            c->server_port = ntohs(a.sin_port);
        }
        n = sizeof(a);
        if (getsockname(s, (struct sockaddr*)&a, &n) == 0) memcpy(c->local_ip, &a.sin_addr, 4);
    }

    /* HELLO: comm_id [12] | online_id [16] | p2p_port u16 */
    uint8_t hello[30] = {0};
    memcpy(hello, comm_id, strlen(comm_id));
    memcpy(hello + 12, online_id, strlen(online_id));
    psnr_put16(hello + 28, p2p_port);
    psnr_msg ack;
    s_connect_error = 0;
    if (psnr_call(c, PSNR_HELLO, hello, sizeof(hello), &ack, timeout_ms < 0 ? 5000 : remaining_ms(deadline)) != 1) {
        psnr_close(c);
        return NULL;
    }
    int ok = ack.type == PSNR_HELLO_ACK && ack.len >= 8;
    s_connect_error = (ack.type == PSNR_ERROR && ack.len >= 4) ? (int)psnr_get32(ack.data) : 0;
    if (ok) c->user_id = psnr_get32(ack.data);
    if (ok && ack.len >= 12) c->token = psnr_get32(ack.data + 8);
    if (ok && ack.len >= 13) c->relay = ack.data[12] & 1;
    if (ok && out_user_id) *out_user_id = psnr_get32(ack.data);
    if (ok && out_public_ip) memcpy(out_public_ip, ack.data + 4, 4);
    psnr_msg_free(&ack);
    if (!ok) { psnr_close(c); return NULL; }
    return c;
}

psnr_client* psnr_connect(const char* host, uint16_t port,
                          const char* comm_id, const char* online_id, uint16_t p2p_port,
                          uint32_t* out_user_id, uint8_t out_public_ip[4])
{
    return connect_impl(host, port, comm_id, online_id, p2p_port, out_user_id, out_public_ip, -1);
}

psnr_client* psnr_connect_timeout(const char* host, uint16_t port,
                                  const char* comm_id, const char* online_id, uint16_t p2p_port,
                                  uint32_t* out_user_id, uint8_t out_public_ip[4], int timeout_ms)
{
    if (timeout_ms <= 0) return NULL;
    return connect_impl(host, port, comm_id, online_id, p2p_port, out_user_id, out_public_ip, timeout_ms);
}

void psnr_server_addr(const psnr_client* c, uint8_t ip[4], uint16_t* port)
{
    memcpy(ip, c->server_ip, 4);
    *port = c->server_port;
}

void psnr_probe_packet(const psnr_client* c, uint8_t out[PSNR_UDP_PROBE_LEN])
{
    memcpy(out, "PSNR", 4);
    out[4] = 0x01;
    psnr_put32(out + 5, c->user_id);
    psnr_put32(out + 9, c->token);
    memcpy(out + 13, c->local_ip, 4);
}

void psnr_punch_packet(const psnr_client* c, uint8_t out[PSNR_UDP_PUNCH_LEN])
{
    memcpy(out, "PSNR", 4);
    out[4] = 0x02;
    psnr_put32(out + 5, c->user_id);
}

int psnr_is_control(const void* buf, size_t len)
{
    return len >= 5 && memcmp(buf, "PSNR", 4) == 0;
}

int psnr_probe_reply(const void* buf, size_t len, uint8_t ip[4], uint16_t* port)
{
    const uint8_t* p = (const uint8_t*)buf;
    if (len < 11 || !psnr_is_control(p, len) || p[4] != 0x81) return 0;
    memcpy(ip, p + 5, 4);
    *port = psnr_get16(p + 9);
    return 1;
}

int psnr_relay_available(const psnr_client* c)
{
    return c->relay;
}

/* "PSNR" 03 | from u32 | token u32 | to u32 | payload */
size_t psnr_relay_wrap(const psnr_client* c, uint32_t to_user, const void* payload, size_t len,
                       uint8_t* out, size_t cap)
{
    if (cap < 17 + len) return 0;
    memcpy(out, "PSNR", 4);
    out[4] = 0x03;
    psnr_put32(out + 5, c->user_id);
    psnr_put32(out + 9, c->token);
    psnr_put32(out + 13, to_user);
    memcpy(out + 17, payload, len);
    return 17 + len;
}

/* from the server: "PSNR" 03 | from u32 | payload */
int psnr_relay_unwrap(const void* buf, size_t len, uint32_t* from_user,
                      const uint8_t** payload, size_t* payload_len)
{
    const uint8_t* p = (const uint8_t*)buf;
    if (len < 9 || !psnr_is_control(p, len) || p[4] != 0x03) return 0;
    *from_user = psnr_get32(p + 5);
    *payload = p + 9;
    *payload_len = len - 9;
    return 1;
}

/* psnr_relay_unwrap for a datagram read from the P2P socket: 0 unless it came
 * from the server's UDP address, so a room member can't send a forged sender. */
int psnr_relay_unwrap_from(const psnr_client* c, const uint8_t src_ip[4], uint16_t src_port,
                           const void* buf, size_t len, uint32_t* from_user,
                           const uint8_t** payload, size_t* payload_len)
{
    if (!c || !src_ip || src_port != c->server_port || memcmp(src_ip, c->server_ip, 4) != 0) return 0;
    return psnr_relay_unwrap(buf, len, from_user, payload, payload_len);
}

/* A relay leg: a fresh connection to the server whose first message is
 * STREAM_CONNECT or STREAM_ACCEPT, returned once the server says READY. */
static int64_t stream_leg(const psnr_client* c, uint8_t type, const uint8_t* body, uint16_t len,
                          int timeout_ms)
{
    struct sockaddr_in a;
    uint8_t f[64], hdr[3];
    pollfd_t pfd;
    int one = 1;
    sock_t s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (s == BAD_SOCK) return -1;
    memset(&a, 0, sizeof(a));
    a.sin_family = AF_INET;
    memcpy(&a.sin_addr, c->server_ip, 4);
    a.sin_port = htons(c->server_port);
    if (connect(s, (struct sockaddr*)&a, sizeof(a)) != 0) goto fail;
    setsockopt(s, IPPROTO_TCP, TCP_NODELAY, (const char*)&one, sizeof(one));

    f[0] = type;
    psnr_put16(f + 1, (uint16_t)(4 + len));
    psnr_put32(f + 3, 1);
    memcpy(f + 7, body, len);
    if (send(s, (const char*)f, 7 + len, 0) != 7 + len) goto fail;

    /* The reply: READY, or an ERROR. The server waits up to 10 s for the
     * other side to accept. */
    for (int got = 0; got < 3; ) {
        pfd.fd = s;
        pfd.events = POLLIN;
        pfd.revents = 0;
        if (poll(&pfd, 1, timeout_ms) <= 0) goto fail;
        int n = (int)recv(s, (char*)hdr + got, 3 - got, 0);
        if (n <= 0) goto fail;
        got += n;
    }
    uint16_t plen = psnr_get16(hdr + 1);
    for (uint16_t got = 0; got < plen; ) {   /* req u32 (+ an error code) */
        uint8_t skip[16];
        int want = plen - got > (int)sizeof(skip) ? (int)sizeof(skip) : plen - got;
        int n = (int)recv(s, (char*)skip, want, 0);
        if (n <= 0) goto fail;
        got = (uint16_t)(got + n);
    }
    if (hdr[0] != PSNR_STREAM_READY) goto fail;
    return (int64_t)s;
fail:
    sock_close(s);
    return -1;
}

int64_t psnr_stream_connect(const psnr_client* c, uint32_t to_user, uint16_t vport, int timeout_ms)
{
    uint8_t b[14];
    psnr_put32(b, c->user_id);
    psnr_put32(b + 4, c->token);
    psnr_put32(b + 8, to_user);
    psnr_put16(b + 12, vport);
    return stream_leg(c, PSNR_STREAM_CONNECT, b, sizeof(b), timeout_ms);
}

int64_t psnr_stream_accept(const psnr_client* c, uint32_t stream_id, int timeout_ms)
{
    uint8_t b[12];
    psnr_put32(b, c->user_id);
    psnr_put32(b + 4, c->token);
    psnr_put32(b + 8, stream_id);
    return stream_leg(c, PSNR_STREAM_ACCEPT, b, sizeof(b), timeout_ms);
}

/* ponytail: one static, not per connection -- a process connects once. */
int psnr_connect_error(void)
{
    return s_connect_error;
}

void psnr_close(psnr_client* c)
{
    psnr_msg m;
    if (!c) return;
    sock_close(c->s);
    c->dead = 1;
    while (take(c, &m, 0)) psnr_msg_free(&m);
    free(c->rx);
    free(c);
}

int psnr_dead_reason(const psnr_client* c)
{
    return c->dead ? c->dead_reason : PSNR_DEAD_NONE;
}

int psnr_last_os_error(const psnr_client* c)
{
    return c->dead_os_err;
}

/* LAN discovery (docs/api.md, "LAN discovery"). */
#define DISC_REQ_LEN   64
#define DISC_REPLY_LEN 52
#define DISC_MAX_DEST  32

static uint32_t disc_nonce(void)
{
    static uint32_t counter;
    uint32_t v = 0;
#ifdef _WIN32
    LARGE_INTEGER q;
    QueryPerformanceCounter(&q);
    v = (uint32_t)q.QuadPart ^ (uint32_t)GetCurrentProcessId() << 16;
#else
    FILE* f = fopen("/dev/urandom", "rb");
    if (f) {
        if (fread(&v, sizeof(v), 1, f) != 1) v = 0;
        fclose(f);
    }
    if (!v) v = (uint32_t)getpid() << 16;
#endif
    v ^= (uint32_t)now_ms() * 2654435761u;
    return v ^ (++counter * 40503u);
}

/* Fill the given printable-ASCII, NUL-terminated string from a NUL-padded field. */
static void disc_text(char* dst, const uint8_t* src, size_t n)
{
    size_t i;
    for (i = 0; i < n && src[i]; i++)
        dst[i] = (src[i] >= 0x20 && src[i] < 0x7f) ? (char)src[i] : '?';
    dst[i] = 0;
}

/* Probes every address in dests (IPv4, host order) at port, then collects replies.
 * Not in psnr.h: the public psnr_discover builds the broadcast list and calls
 * this; test_psnr.c calls it directly with a loopback address. */
int psnr_discover_to(uint16_t port, int timeout_ms, psnr_found* out, int max,
                     const uint32_t* dests, int ndests);
int psnr_discover_to(uint16_t port, int timeout_ms, psnr_found* out, int max,
                     const uint32_t* dests, int ndests)
{
    uint8_t req[DISC_REQ_LEN] = {0}, buf[128];
    uint32_t nonce = disc_nonce();
    uint64_t deadline;
    sock_t s;
    int one = 1, sent = 0, found = 0, i;

    if (!out || max <= 0) return -1;
    if (timeout_ms < 0) timeout_ms = 0;
#ifdef _WIN32
    { WSADATA w; WSAStartup(MAKEWORD(2, 2), &w); }
#endif
    s = socket(AF_INET, SOCK_DGRAM, 0);
    if (s == BAD_SOCK) return -1;
    setsockopt(s, SOL_SOCKET, SO_BROADCAST, (const char*)&one, sizeof(one));

    memcpy(req, "PSNR", 4);
    req[4] = 0x04;
    psnr_put32(req + 5, nonce);
    for (i = 0; i < ndests; i++) {
        struct sockaddr_in a;
        memset(&a, 0, sizeof(a));
        a.sin_family = AF_INET;
        a.sin_port = htons(port);
        a.sin_addr.s_addr = htonl(dests[i]);
        if (sendto(s, (const char*)req, DISC_REQ_LEN, 0, (struct sockaddr*)&a, sizeof(a)) == DISC_REQ_LEN) sent++;
    }
    if (!sent) { sock_close(s); return -1; }

    deadline = now_ms() + (uint64_t)timeout_ms;
    while (found < max) {
        uint64_t now = now_ms();
        pollfd_t pf;
        struct sockaddr_in from;
        socklen_t fl = sizeof(from);
        int r, k;
        if (now >= deadline) break;
        pf.fd = s;
        pf.events = POLLIN;
        pf.revents = 0;
        r = poll(&pf, 1, (int)(deadline - now));
        if (r < 0) {
#ifndef _WIN32
            if (errno == EINTR) continue;
#endif
            found = -1;
            break;
        }
        if (r == 0) break;
        r = (int)recvfrom(s, (char*)buf, sizeof(buf), 0, (struct sockaddr*)&from, &fl);
        if (r != DISC_REPLY_LEN || memcmp(buf, "PSNR", 4) != 0 || buf[4] != 0x84 ||
            psnr_get32(buf + 5) != nonce)
            continue;
        {
            psnr_found f;
            memset(&f, 0, sizeof(f));
            memcpy(f.ip, &from.sin_addr, 4);
            f.tcp_port = psnr_get16(buf + 9);
            f.udp_port = psnr_get16(buf + 11);
            f.relay = buf[13] & 1;
            f.players = psnr_get16(buf + 14);
            disc_text(f.name, buf + 16, 24);
            disc_text(f.version, buf + 40, 12);
            for (k = 0; k < found; k++)
                if (memcmp(out[k].ip, f.ip, 4) == 0 && out[k].tcp_port == f.tcp_port) break;
            if (k == found) out[found++] = f;
        }
    }
    sock_close(s);
    return found;
}

int psnr_discover(uint16_t port, int timeout_ms, psnr_found* out, int max)
{
    uint32_t dests[DISC_MAX_DEST];
    int n = 0;
    dests[n++] = 0xffffffffu;
#ifndef _WIN32
    {   /* each IPv4 interface's directed broadcast; Windows uses 255.255.255.255 only */
        struct ifaddrs *ifa, *p;
        if (getifaddrs(&ifa) == 0) {
            for (p = ifa; p; p = p->ifa_next) {
                uint32_t d;
                int k;
                if (!p->ifa_addr || p->ifa_addr->sa_family != AF_INET || !p->ifa_broadaddr) continue;
                if ((p->ifa_flags & (IFF_BROADCAST | IFF_UP)) != (IFF_BROADCAST | IFF_UP)) continue;
                d = ntohl(((struct sockaddr_in*)p->ifa_broadaddr)->sin_addr.s_addr);
                if (!d) continue;
                for (k = 0; k < n && dests[k] != d; k++) {}
                if (k == n && n < DISC_MAX_DEST) dests[n++] = d;
            }
            freeifaddrs(ifa);
        }
    }
#endif
    return psnr_discover_to(port ? port : 36100, timeout_ms, out, max, dests, n);
}

/* ---- payload decoders --------------------------------------------------- */

static void copy_name(char out[PSNR_ONLINE_ID_MAX + 1], const uint8_t* p)
{
    memcpy(out, p, PSNR_ONLINE_ID_MAX);
    out[PSNR_ONLINE_ID_MAX] = 0;
    for (size_t i = 0; out[i]; i++) if ((unsigned char)out[i] < 0x20 || (unsigned char)out[i] > 0x7E) out[i] = '?';
}

/* A u16-length blob at p within len bytes: 1 and the pointers on success. */
static int take_blob(const uint8_t* p, size_t len, const uint8_t** data, uint16_t* n, size_t* used)
{
    if (len < 2) return 0;
    *n = psnr_get16(p);
    if (len - 2 < *n) return 0;
    *data = p + 2;
    *used = 2u + *n;
    return 1;
}

int psnr_member_parse(const uint8_t* p, size_t len, psnr_member* out, size_t* used)
{
    size_t b;
    if (len < 29) return 0;
    out->member_id = psnr_get16(p);
    out->user_id = psnr_get32(p + 2);
    copy_name(out->online_id, p + 6);
    memcpy(out->ip, p + 22, 4);
    out->p2p_port = psnr_get16(p + 26);
    out->owner = p[28];
    if (!take_blob(p + 29, len - 29, &out->data, &out->data_len, &b)) return 0;
    if (used) *used = 29 + b;
    return 1;
}

int psnr_member_next(psnr_cursor* c, psnr_member* out)
{
    size_t used;
    if (c->count == 0 || !psnr_member_parse(c->p, c->left, out, &used)) return 0;
    c->p += used; c->left -= used; c->count--;
    return 1;
}

int psnr_room_joined_parse(const psnr_msg* m, psnr_room_joined* out)
{
    const uint8_t* p = m->data;
    size_t len = m->len, b;
    psnr_cursor probe;
    psnr_member mb;
    if (len < 17) return 0;
    out->room_id = psnr_get64(p);
    out->my_member = psnr_get16(p + 8);
    out->owner = psnr_get16(p + 10);
    out->max = p[12];
    out->flags = psnr_get32(p + 13);
    p += 17; len -= 17;
    if (!take_blob(p, len, &out->external, &out->external_len, &b)) return 0;
    p += b; len -= b;
    if (!take_blob(p, len, &out->internal, &out->internal_len, &b)) return 0;
    p += b; len -= b;
    if (len < 1) return 0;
    out->members.count = p[0];
    out->members.p = p + 1;
    out->members.left = len - 1;
    probe = out->members;               /* every member must parse, and nothing may trail */
    while (probe.count) if (!psnr_member_next(&probe, &mb)) return 0;
    return probe.left == 0;
}

int psnr_room_list_parse(const psnr_msg* m, psnr_cursor* c, uint16_t* total)
{
    psnr_cursor probe;
    psnr_room r;
    if (m->len < 4) return 0;
    if (total) *total = psnr_get16(m->data);
    c->count = psnr_get16(m->data + 2);
    c->p = m->data + 4;
    c->left = m->len - 4;
    probe = *c;
    while (probe.count) if (!psnr_room_next(&probe, &r)) return 0;
    return probe.left == 0;
}

int psnr_room_next(psnr_cursor* c, psnr_room* out)
{
    size_t b;
    if (c->count == 0 || c->left < 30) return 0;
    if (!take_blob(c->p + 30, c->left - 30, &out->external, &out->external_len, &b)) return 0;
    out->room_id = psnr_get64(c->p);
    copy_name(out->owner, c->p + 8);
    out->cur = c->p[24];
    out->max = c->p[25];
    out->flags = psnr_get32(c->p + 26);
    c->p += 30 + b; c->left -= 30 + b; c->count--;
    return 1;
}

int psnr_ranking_parse(const psnr_msg* m, psnr_cursor* c, uint32_t* total)
{
    psnr_cursor probe;
    psnr_rank r;
    if (m->len < 6) return 0;
    if (total) *total = psnr_get32(m->data);
    c->count = psnr_get16(m->data + 4);
    c->p = m->data + 6;
    c->left = m->len - 6;
    probe = *c;
    while (probe.count) if (!psnr_rank_next(&probe, &r)) return 0;
    return probe.left == 0;
}

int psnr_rank_next(psnr_cursor* c, psnr_rank* out)
{
    size_t b;
    if (c->count == 0 || c->left < 28) return 0;
    if (!take_blob(c->p + 28, c->left - 28, &out->comment, &out->comment_len, &b)) return 0;
    if (c->left - 28 - b < 8) return 0;
    out->rank = psnr_get32(c->p);
    copy_name(out->online_id, c->p + 4);
    out->score = (int64_t)psnr_get64(c->p + 20);
    out->recorded_unix = psnr_get64(c->p + 28 + b);
    c->p += 36 + b; c->left -= 36 + b; c->count--;
    return 1;
}
