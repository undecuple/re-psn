/*
 * test_psnr - the C client against a live server: HELLO, a room joined by a
 * second client, the push that tells the host, a room message, a score.
 * The server's Go tests build and run this when a C compiler is on PATH:
 *
 *   test_psnr <host> <port>
 */
#include "psnr.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#  include <winsock2.h>
#  include <windows.h>
#  define sleep_ms(n) Sleep(n)
   typedef int socklen_t;
#else
#  include <unistd.h>
#  include <sys/socket.h>
#  include <netinet/in.h>
#  include <fcntl.h>
#  define sleep_ms(n) usleep((n) * 1000)
#endif

#define COMM "NPWR00001_00"

/* A UDP socket, as a title's P2P socket would be, probes the server and gets
 * back the address the server saw it at. */
static void probe(psnr_client* c)
{
    int u = (int)socket(AF_INET, SOCK_DGRAM, 0);
    struct sockaddr_in me, srv;
    socklen_t n = sizeof(me);
    uint8_t pkt[PSNR_UDP_PROBE_LEN], reply[64], ip[4];
    uint16_t port;
    assert(u >= 0);
    memset(&me, 0, sizeof(me));
    me.sin_family = AF_INET;
    assert(bind(u, (struct sockaddr*)&me, sizeof(me)) == 0);
    getsockname(u, (struct sockaddr*)&me, &n);
#ifdef _WIN32
    { u_long nb = 1; ioctlsocket(u, FIONBIO, &nb); }
#else
    fcntl(u, F_SETFL, O_NONBLOCK);
#endif
    memset(&srv, 0, sizeof(srv));
    srv.sin_family = AF_INET;
    psnr_server_addr(c, (uint8_t*)&srv.sin_addr, &port);
    srv.sin_port = htons(port);
    psnr_probe_packet(c, pkt);
    assert(psnr_is_control(pkt, sizeof(pkt)));
    for (int i = 0; i < 200; i++) {   /* ~2 s, one probe per 100 ms */
        if (i % 20 == 0) sendto(u, (const char*)pkt, sizeof(pkt), 0, (struct sockaddr*)&srv, sizeof(srv));
        int r = (int)recv(u, (char*)reply, sizeof(reply), 0);
        if (r > 0 && psnr_probe_reply(reply, (size_t)r, ip, &port)) {
            assert(port == ntohs(me.sin_port) && ip[0] == 127);
#ifdef _WIN32
            closesocket(u);
#else
            close(u);
#endif
            return;
        }
        sleep_ms(10);
    }
    fprintf(stderr, "no probe reply\n");
    exit(1);
}

static psnr_msg wait_push(psnr_client* c, uint8_t type)
{
    psnr_msg m;
    for (int i = 0; i < 400; i++) {   /* ~2 s */
        int r = psnr_poll(c, &m);
        assert(r >= 0);
        if (r == 1) {
            assert(m.req == 0 && m.type == type);
            return m;
        }
        sleep_ms(5);
    }
    fprintf(stderr, "no push 0x%02X\n", type);
    exit(1);
}

/* Internal entry point of psnr_discover (psnr.c), not in the public header:
 * it takes the destination list so the test can probe loopback. */
int psnr_discover_to(uint16_t port, int timeout_ms, psnr_found* out, int max,
                     const uint32_t* dests, int ndests);

/* Fake responder on loopback. Each call runs it in a forked child that waits
 * for one request and answers according to mode, so psnr_discover_to can
 * block in the parent. POSIX only; the Windows build skips these checks. */
#ifndef _WIN32
#  include <sys/wait.h>

static void disc_reply(uint8_t* r, uint32_t nonce, uint16_t tcp, const char* name)
{
    memset(r, 0, 52);
    memcpy(r, "PSNR", 4);
    r[4] = 0x84;
    psnr_put32(r + 5, nonce);
    psnr_put16(r + 9, tcp);
    psnr_put16(r + 11, 36101);
    r[13] = 1;
    psnr_put16(r + 14, 7);
    memcpy(r + 16, name, strlen(name) < 24 ? strlen(name) : 24);
    memcpy(r + 40, "1.2.3", 5);
}

static void disc_serve(int sock, int mode)
{
    uint8_t req[128], r[64];
    struct sockaddr_in from;
    socklen_t fl = sizeof(from);
    int n = (int)recvfrom(sock, (char*)req, sizeof(req), 0, (struct sockaddr*)&from, &fl);
    uint32_t nonce;
    assert(n == 64 && memcmp(req, "PSNR", 4) == 0 && req[4] == 4);
    nonce = psnr_get32(req + 5);
    for (int i = 9; i < 64; i++) assert(req[i] == 0);             /* zero padding */
    if (mode == 0) return;                                        /* no reply at all */
    if (mode == 2) {                                              /* garbage name */
        disc_reply(r, nonce, 36100, "");
        memcpy(r + 16, "ab\x01\xff" "cd", 6);
        sendto(sock, (char*)r, 52, 0, (struct sockaddr*)&from, fl);
        return;
    }
    disc_reply(r, nonce + 1, 1111, "wrong-nonce");                /* ignored */
    sendto(sock, (char*)r, 52, 0, (struct sockaddr*)&from, fl);
    disc_reply(r, nonce, 2222, "short");                          /* ignored: 51 bytes */
    sendto(sock, (char*)r, 51, 0, (struct sockaddr*)&from, fl);
    disc_reply(r, nonce, 3333, "long");                           /* ignored: 53 bytes */
    sendto(sock, (char*)r, 53, 0, (struct sockaddr*)&from, fl);
    disc_reply(r, nonce, 4444, "wrong-type");
    r[4] = 0x83;                                                  /* ignored */
    sendto(sock, (char*)r, 52, 0, (struct sockaddr*)&from, fl);
    disc_reply(r, nonce, 36100, "lan box");                       /* accepted */
    sendto(sock, (char*)r, 52, 0, (struct sockaddr*)&from, fl);
    sendto(sock, (char*)r, 52, 0, (struct sockaddr*)&from, fl);   /* duplicate: ignored */
    disc_reply(r, nonce, 36200, "second");                        /* same ip, other port: kept */
    sendto(sock, (char*)r, 52, 0, (struct sockaddr*)&from, fl);
}

static pid_t disc_start(int sock, int mode)
{
    pid_t pid = fork();
    assert(pid >= 0);
    if (pid == 0) { disc_serve(sock, mode); _exit(0); }
    return pid;
}

static void disc_finish(pid_t pid)
{
    int st = 0;
    assert(waitpid(pid, &st, 0) == pid && WIFEXITED(st) && WEXITSTATUS(st) == 0);
}
#endif

static void discover_tests(void)
{
    psnr_found f[4];
    /* bad arguments */
    assert(psnr_discover(0, 10, NULL, 4) == -1 && psnr_discover(0, 10, f, 0) == -1);
#ifndef _WIN32
    {
        int sock = (int)socket(AF_INET, SOCK_DGRAM, 0);
        struct sockaddr_in a;
        socklen_t n = sizeof(a);
        uint32_t lo = 0x7f000001u;
        uint16_t port;
        pid_t pid;

        memset(&a, 0, sizeof(a));
        a.sin_family = AF_INET;
        a.sin_addr.s_addr = htonl(lo);
        assert(sock >= 0 && bind(sock, (struct sockaddr*)&a, sizeof(a)) == 0);
        assert(getsockname(sock, (struct sockaddr*)&a, &n) == 0);
        port = ntohs(a.sin_port);

        /* wrong nonce, length and type and a duplicate are ignored; fields decode */
        pid = disc_start(sock, 1);
        assert(psnr_discover_to(port, 500, f, 4, &lo, 1) == 2);
        disc_finish(pid);
        assert(f[0].ip[0] == 127 && f[0].ip[3] == 1 && f[0].tcp_port == 36100 && f[0].udp_port == 36101);
        assert(f[0].relay == 1 && f[0].players == 7);
        assert(strcmp(f[0].name, "lan box") == 0 && strcmp(f[0].version, "1.2.3") == 0);
        assert(f[1].tcp_port == 36200 && strcmp(f[1].name, "second") == 0);

        /* max is respected */
        pid = disc_start(sock, 1);
        assert(psnr_discover_to(port, 500, f, 1, &lo, 1) == 1);
        disc_finish(pid);
        assert(f[0].tcp_port == 36100);

        /* a garbage name is made printable ASCII */
        pid = disc_start(sock, 2);
        assert(psnr_discover_to(port, 500, f, 4, &lo, 1) == 1);
        disc_finish(pid);
        assert(strcmp(f[0].name, "ab??cd") == 0);

        /* no reply: 0 after about the timeout */
        pid = disc_start(sock, 0);
        assert(psnr_discover_to(port, 200, f, 4, &lo, 1) == 0);
        disc_finish(pid);
        close(sock);
    }
#endif
}

/* The relay's server-to-client datagram: "PSNR" 03 | from u32 | payload.
 * psnr_relay_unwrap_from must take it only from the server's UDP address. */
static void relay_source_tests(psnr_client* c)
{
    uint8_t srv_ip[4], other_ip[4], dg[9 + 5];
    uint16_t srv_port;
    uint32_t from = 0;
    const uint8_t* payload = NULL;
    size_t plen = 0;

    psnr_server_addr(c, srv_ip, &srv_port);
    memcpy(other_ip, srv_ip, 4);
    other_ip[3] ^= 1;                       /* a different address on the same network */
    memcpy(dg, "PSNR", 4);
    dg[4] = 0x03;
    psnr_put32(dg + 5, 4242);               /* from_user */
    memcpy(dg + 9, "hello", 5);

    /* the server's address: accepted, with the sender and payload */
    assert(psnr_relay_unwrap_from(c, srv_ip, srv_port, dg, sizeof(dg), &from, &payload, &plen) == 1);
    assert(from == 4242 && plen == 5 && memcmp(payload, "hello", 5) == 0);

    /* a member's datagram, same bytes, from another address: refused */
    from = 0;
    payload = NULL;
    plen = 0;
    assert(psnr_relay_unwrap_from(c, other_ip, srv_port, dg, sizeof(dg), &from, &payload, &plen) == 0);
    assert(from == 0 && payload == NULL && plen == 0);

    /* the server's address but another port: refused */
    assert(psnr_relay_unwrap_from(c, srv_ip, (uint16_t)(srv_port + 1), dg, sizeof(dg), &from, &payload, &plen) == 0);
    assert(from == 0 && payload == NULL && plen == 0);

    /* the server's address, a datagram that isn't a relay one: still refused (same rules as before) */
    dg[4] = 0x02;
    assert(psnr_relay_unwrap_from(c, srv_ip, srv_port, dg, sizeof(dg), &from, &payload, &plen) == 0);
    dg[4] = 0x03;

    /* the old function is unchanged: it still accepts the same bytes */
    from = 0;
    assert(psnr_relay_unwrap(dg, sizeof(dg), &from, &payload, &plen) == 1);
    assert(from == 4242 && plen == 5 && memcmp(payload, "hello", 5) == 0);
}

/* The decoders on hand-built payloads, no server: every cut-short copy of a good payload is refused, and a trailing byte
 * is refused too. */
static void decoder_tests(void)
{
    uint8_t buf[128], *p = buf;
    psnr_msg m;
    psnr_cursor cur;
    psnr_room_joined rj;
    psnr_rank rk;
    uint32_t total;
    size_t n;

    /* ROOM_JOINED: room 7, me 2, owner 1, max 4, flags 0, external "ab", internal "", 1 member "carol" with data "z" */
    p = psnr_put64(p, 7); p = psnr_put16(p, 2); p = psnr_put16(p, 1); *p++ = 4; p = psnr_put32(p, 0);
    p = psnr_put16(p, 2); *p++ = 'a'; *p++ = 'b'; p = psnr_put16(p, 0); *p++ = 1;
    p = psnr_put16(p, 2); p = psnr_put32(p, 99); memset(p, 0, 16); memcpy(p, "carol", 5); p += 16;
    *p++ = 10; *p++ = 0; *p++ = 0; *p++ = 1; p = psnr_put16(p, 3658); *p++ = 0; p = psnr_put16(p, 1); *p++ = 'z';
    n = (size_t)(p - buf);
    m.type = PSNR_ROOM_JOINED; m.req = 1; m.data = buf;
    m.len = (uint32_t)n;
    assert(psnr_room_joined_parse(&m, &rj) && rj.members.count == 1 && rj.external_len == 2);
    for (size_t cut = 0; cut < n; cut++) { m.len = (uint32_t)cut; assert(!psnr_room_joined_parse(&m, &rj)); }
    buf[n] = 0; m.len = (uint32_t)n + 1;
    assert(!psnr_room_joined_parse(&m, &rj));   /* a trailing byte */

    /* RANKING: total 5, one row: rank 1, "dave", score -3, comment "hi", recorded 1700000000 */
    p = buf;
    p = psnr_put32(p, 5); p = psnr_put16(p, 1);
    p = psnr_put32(p, 1); memset(p, 0, 16); memcpy(p, "dave", 4); p += 16;
    p = psnr_put64(p, (uint64_t)(int64_t)-3); p = psnr_put16(p, 2); *p++ = 'h'; *p++ = 'i'; p = psnr_put64(p, 1700000000u);
    n = (size_t)(p - buf);
    m.type = PSNR_RANKING; m.data = buf; m.len = (uint32_t)n;
    assert(psnr_ranking_parse(&m, &cur, &total) && total == 5 && cur.count == 1);
    assert(psnr_rank_next(&cur, &rk) && rk.rank == 1 && strcmp(rk.online_id, "dave") == 0 && rk.score == -3 && rk.comment_len == 2 && rk.recorded_unix == 1700000000u);
    assert(!psnr_rank_next(&cur, &rk));
    for (size_t cut = 0; cut < n; cut++) { m.len = (uint32_t)cut; assert(!psnr_ranking_parse(&m, &cur, &total)); }

    /* a count larger than the entries present is refused, not read past */
    p = buf; p = psnr_put16(p, 9); p = psnr_put16(p, 3);
    m.type = PSNR_ROOM_LIST; m.len = 4;
    assert(!psnr_room_list_parse(&m, &cur, NULL));
}

/* Names longer than the wire fields are refused, not cut. This needs the live server: against a closed port
 * psnr_connect fails whatever the names are, so only a server that is there shows the check refusing. */
static void name_limit_tests(const char* host, uint16_t port)
{
    psnr_client* ok;

    assert(psnr_connect(host, port, "NPWR00001_00X", "ok", 0, NULL, NULL) == NULL);
    assert(psnr_connect(host, port, COMM, "this-name-is-17-c", 0, NULL, NULL) == NULL);
    /* the longest names that fit still connect */
    ok = psnr_connect(host, port, COMM, "sixteen-chars-ok", 0, NULL, NULL);
    assert(ok);
    psnr_close(ok);
}

int main(int argc, char** argv)
{
    const char* host = argc > 1 ? argv[1] : "127.0.0.1";
    uint16_t port = (uint16_t)(argc > 2 ? atoi(argv[2]) : 36100);
    uint32_t ida, idb;
    uint8_t ip[4];

    decoder_tests();
    name_limit_tests(host, port);
    discover_tests();
    psnr_client* a = psnr_connect(host, port, COMM, "alice", 3658, &ida, ip);
    psnr_client* b = psnr_connect(host, port, COMM, "bob", 3659, &idb, ip);
    assert(a && b && ida != idb);
    assert(ip[0] != 0);   /* the address the server sees us at */
    probe(a);
    {   /* a punch is a control packet; a title's own data isn't */
        uint8_t punch[PSNR_UDP_PUNCH_LEN];
        psnr_punch_packet(a, punch);
        assert(psnr_is_control(punch, sizeof(punch)) && !psnr_is_control("hello", 5));
    }
    relay_source_tests(a);

    /* alice hosts: max 4 | flags 0 | external "x" | internal "" | member data "" */
    uint8_t body[64], *p = body;
    psnr_msg m;
    *p++ = 4;
    p = psnr_put32(p, 0);
    p = psnr_put16(p, 1);
    *p++ = 'x';
    p = psnr_put16(p, 0);
    p = psnr_put16(p, 0);
    assert(psnr_call(a, PSNR_CREATE_ROOM, body, (uint32_t)(p - body), &m, 2000) == 1);
    assert(m.type == PSNR_ROOM_JOINED);
    uint64_t room = psnr_get64(m.data);
    {   /* the decoder reads what the server really sent */
        psnr_room_joined rj;
        psnr_member mb;
        assert(psnr_room_joined_parse(&m, &rj) && rj.room_id == room && rj.max == 4);
        assert(rj.external_len == 1 && rj.external[0] == 'x' && rj.members.count == 1);
        assert(psnr_member_next(&rj.members, &mb) && strcmp(mb.online_id, "alice") == 0 && mb.owner == 1 && mb.p2p_port == 3658);
        assert(!psnr_member_next(&rj.members, &mb));
    }
    psnr_msg_free(&m);

    /* bob searches and joins */
    p = psnr_put16(body, 0);
    p = psnr_put16(p, 10);
    assert(psnr_call(b, PSNR_SEARCH_ROOMS, body, 4, &m, 2000) == 1);
    assert(m.type == PSNR_ROOM_LIST && psnr_get16(m.data + 2) == 1 && psnr_get64(m.data + 4) == room);
    {
        psnr_cursor cur;
        psnr_room r;
        uint16_t total;
        assert(psnr_room_list_parse(&m, &cur, &total) && total == 1 && cur.count == 1);
        assert(psnr_room_next(&cur, &r) && r.room_id == room && strcmp(r.owner, "alice") == 0 && r.cur == 1 && r.max == 4 && r.external_len == 1);
        assert(!psnr_room_next(&cur, &r));
    }
    psnr_msg_free(&m);
    p = psnr_put64(body, room);
    p = psnr_put16(p, 0);   /* no member data */
    assert(psnr_call(b, PSNR_JOIN_ROOM, body, (uint32_t)(p - body), &m, 2000) == 1);
    assert(m.type == PSNR_ROOM_JOINED);
    {
        psnr_room_joined rj;
        assert(psnr_room_joined_parse(&m, &rj) && rj.members.count == 2);
    }
    psnr_msg_free(&m);

    /* alice makes an unrelated call first: the MEMBER_JOINED push that lands
     * meanwhile must stay queued for psnr_poll, not be eaten by psnr_call. */
    p = psnr_put32(body, 1);
    *p++ = 0;
    p = psnr_put64(p, 100);
    p = psnr_put16(p, 0);
    assert(psnr_call(a, PSNR_RECORD_SCORE, body, (uint32_t)(p - body), &m, 2000) == 1);
    assert(m.type == PSNR_SCORE_RECORDED && psnr_get32(m.data) == 1);
    psnr_msg_free(&m);

    m = wait_push(a, PSNR_MEMBER_JOINED);
    /* room u64 | member u16 | user u32 | online_id [16] | ip [4] | port u16 | owner u8 */
    assert(psnr_get64(m.data) == room);
    assert(psnr_get32(m.data + 10) == idb);
    assert(strcmp((const char*)m.data + 14, "bob") == 0);
    assert(psnr_get16(m.data + 34) == 3659);
    psnr_msg_free(&m);

    /* bob -> everyone */
    p = psnr_put64(body, room);
    p = psnr_put16(p, 0);
    p = psnr_put16(p, 2);
    *p++ = 'h';
    *p++ = 'i';
    assert(psnr_call(b, PSNR_ROOM_MESSAGE, body, (uint32_t)(p - body), &m, 2000) == 1);
    assert(m.type == PSNR_OK);
    psnr_msg_free(&m);
    m = wait_push(a, PSNR_ROOM_MSG);
    /* room u64 | from u16 | to u16 | blob */
    assert(psnr_get16(m.data + 8) == 2 && psnr_get16(m.data + 10) == 0);
    assert(psnr_get16(m.data + 12) == 2 && memcmp(m.data + 14, "hi", 2) == 0);
    psnr_msg_free(&m);

    {   /* the round trip the library times from its own calls, reported in HEARTBEAT */
        uint8_t search[4] = {0, 0, 0, 10};
        assert(psnr_rtt_ms(a) >= 0 && psnr_rtt_ms(a) < 5000);
        psnr_report_rtt(a, 1);                       /* alice opts in; bob stays at the default (off) */
        assert(psnr_heartbeat(a) && psnr_heartbeat(b));
        assert(psnr_call(a, PSNR_SEARCH_ROOMS, search, 4, &m, 2000) == 1);   /* the server has handled both */
        psnr_msg_free(&m);
        assert(psnr_call(b, PSNR_SEARCH_ROOMS, search, 4, &m, 2000) == 1);
        psnr_msg_free(&m);
        if (argc > 3 && strcmp(argv[3], "--rtt-sync") == 0) {   /* the Go test looks at the server now */
            char line[16];
            printf("RTT_SENT\n");
            fflush(stdout);
            if (!fgets(line, sizeof(line), stdin)) return 1;
        }
    }

    psnr_close(b);
    m = wait_push(a, PSNR_MEMBER_LEFT);
    psnr_msg_free(&m);
    psnr_close(a);

    printf("test_psnr: all passed\n");
    return 0;
}
