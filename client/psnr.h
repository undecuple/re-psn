/*
 * psnr.h - client for the psnr server (docs/api.md), in C for ps3recomp's
 * sceNp modules to vendor.
 *
 * No threads. psnr_send() transmits a request and returns its id; the reply and
 * any server pushes come back out of psnr_poll(). Its receive path does not
 * wait for new data, but automatic heartbeat writes may wait for writability, with no time limit.
 * An adapter owns transport calls serially and delivers completions through
 * the title's guest callback queue. Keep potentially blocking transport work
 * away from timing-sensitive guest execution. psnr_call() is the blocking
 * form, for setup and tests.
 *
 * Poll regularly for replies and idle heartbeats. The server drops a
 * connection that sends nothing for 90 seconds.
 *
 * Not thread-safe: one thread per psnr_client at a time.
 *
 * What it touches. The client owns only the sockets it makes itself: the TCP
 * connection psnr_connect opens to the server, and the TCP connection each
 * relay stream opens (psnr_stream_connect and psnr_stream_accept return it to
 * the caller, who owns and closes it) and, only if the title calls
 * psnr_discover, one temporary UDP socket that call opens and closes before it
 * returns. It never opens, reads, writes or closes
 * a socket of the title's; the UDP peer socket is the title's, and the library
 * only builds and reads the packets it exchanges. Connecting and disconnecting
 * are explicit: nothing connects until the title calls psnr_connect, nothing
 * reconnects by itself (psnr_poll returns -1 once the connection is lost), and
 * psnr_close closes it. A title that never calls psnr_connect sends nothing.
 * Process-wide effects: on Windows each psnr_connect and psnr_discover calls WSAStartup (2.2) and
 * the library never calls WSACleanup; psnr_connect_error is one process-global
 * value; the library starts no threads and installs no signal handlers or
 * timers. It sends with plain send(), so on POSIX a write to a connection the
 * server has closed can raise SIGPIPE unless the title ignores that signal. It
 * resolves and connects over IPv4 only.
 */
#ifndef PSNR_H
#define PSNR_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Message types; payloads in docs/api.md. */
enum {
    PSNR_HELLO = 0x01, PSNR_HEARTBEAT = 0x02,
    PSNR_CREATE_ROOM = 0x10, PSNR_SEARCH_ROOMS = 0x11, PSNR_JOIN_ROOM = 0x12,
    PSNR_LEAVE_ROOM = 0x13, PSNR_SET_ROOM_DATA = 0x14, PSNR_ROOM_MESSAGE = 0x15,
    PSNR_KICK_MEMBER = 0x16,
    PSNR_RECORD_SCORE = 0x20, PSNR_GET_RANKING = 0x21, PSNR_GET_RANKING_BY_ID = 0x22,

    PSNR_HELLO_ACK = 0x81, PSNR_ROOM_JOINED = 0x90, PSNR_ROOM_LIST = 0x91,
    PSNR_OK = 0x93, PSNR_SCORE_RECORDED = 0xB0, PSNR_RANKING = 0xB1,
    PSNR_ERROR = 0x8F,

    PSNR_MEMBER_JOINED = 0xA1, PSNR_MEMBER_LEFT = 0xA2, PSNR_ROOM_DATA = 0xA3,
    PSNR_ROOM_MSG = 0xA4, PSNR_KICKED = 0xA5,

    /* the relay (docs/api.md, "Relay") */
    PSNR_STREAM_CONNECT = 0x30, PSNR_STREAM_ACCEPT = 0x31, PSNR_STREAM_READY = 0x96,
    PSNR_STREAM_OFFER = 0xA6, PSNR_ROUTE = 0xA7
};
#define PSNR_ROUTE_STREAM_RELAY 1   /* ROUTE flags: streams to that player go through the relay */

/* ERROR codes */
enum {
    PSNR_E_BAD_REQUEST = 1, PSNR_E_NOT_FOUND = 2, PSNR_E_ROOM_FULL = 3,
    PSNR_E_NOT_OWNER = 4, PSNR_E_NOT_IN_ROOM = 5, PSNR_E_LIMIT = 6, PSNR_E_NO_HELLO = 7,
    PSNR_E_NAME_TAKEN = 8, PSNR_E_NO_RELAY = 9
};

typedef struct psnr_client psnr_client;

/* One reply or push. req is 0 for pushes. data excludes the request id and
 * is freed by psnr_msg_free. */
typedef struct {
    uint8_t  type;
    uint32_t req;
    uint8_t* data;
    uint32_t len;
} psnr_msg;

/* Blocking connect and HELLO setup (DNS/connect/send are not bounded by a
 * caller deadline). comm_id is the title's NP communication ID
 * ("NPWR00860_00"), online_id the player's name, p2p_port the UDP port the
 * title takes peer traffic on (0 if none). Fills the id the server assigned
 * and the address it sees this client at. NULL on failure. */
psnr_client* psnr_connect(const char* host, uint16_t port,
                          const char* comm_id, const char* online_id, uint16_t p2p_port,
                          uint32_t* out_user_id, uint8_t out_public_ip[4]);

/* NAT traversal (docs/api.md, "UDP"). The caller owns the P2P socket; these
 * build and read the packets it exchanges. Every one starts with "PSNR".
 *   - Send a PROBE from the title's own P2P socket to psnr_server_addr until a
 *     probe reply comes back; the server then hands other players this
 *     socket's public endpoint. Keep sending one about every 15 seconds while
 *     the title is online: some home gateways drop an idle UDP mapping after
 *     30 seconds (a 2010 measurement study of home gateways, Haetoenen et al.,
 *     IMC 2010, reported a minimum near 30 seconds and a median near 3
 *     minutes; RFC 4787 asks for at least 2 minutes).
 *   - Send a few PUNCHes to a peer when it appears, so this side's router
 *     lets the peer's packets in.
 *   - Drop anything psnr_is_control() matches before the title sees it. */
#define PSNR_UDP_PROBE_LEN 17
#define PSNR_UDP_PUNCH_LEN 9
void psnr_server_addr(const psnr_client* c, uint8_t ip[4], uint16_t* port);
void psnr_probe_packet(const psnr_client* c, uint8_t out[PSNR_UDP_PROBE_LEN]);
void psnr_punch_packet(const psnr_client* c, uint8_t out[PSNR_UDP_PUNCH_LEN]);
int  psnr_is_control(const void* buf, size_t len);
/* 1 if buf is a probe reply; fills the public endpoint the server saw. */
int  psnr_probe_reply(const void* buf, size_t len, uint8_t ip[4], uint16_t* port);

/* The relay, for peers that can't reach each other directly. It is there only
 * if the server runs with -relay.
 *   Datagrams: wrap a payload for a peer and send it to psnr_server_addr from
 *   the P2P socket; what comes back from the server unwraps to the sender and
 *   the payload. Check the source address of every datagram that unwraps:
 *   use psnr_relay_unwrap_from, which returns 0 unless it came from the server.
 *   Any room member can send a datagram with this header straight to the
 *   title's UDP port, so a title that unwraps without the source check can be
 *   sent a forged from_user.
 *   Streams: when a ROUTE push says a peer takes streams only through the
 *   relay, psnr_stream_connect opens one to it; the peer gets a STREAM_OFFER
 *   push (stream_id u32 | from_user u32 | vport u16) and answers with
 *   psnr_stream_accept. Both return a connected, blocking socket carrying the
 *   stream's raw bytes (a SOCKET on Windows, an fd elsewhere), or -1. */
int     psnr_relay_available(const psnr_client* c);
size_t  psnr_relay_wrap(const psnr_client* c, uint32_t to_user, const void* payload, size_t len,
                        uint8_t* out, size_t cap);   /* 0 if it doesn't fit */
/* Does not check the source: prefer psnr_relay_unwrap_from. Kept for
 * compatibility; it accepts a datagram from anywhere. */
int     psnr_relay_unwrap(const void* buf, size_t len, uint32_t* from_user,
                          const uint8_t** payload, size_t* payload_len);
/* psnr_relay_unwrap, but returns 0 unless the datagram's source (src_ip,
 * src_port, as the UDP socket reports it) is the server's UDP address
 * (psnr_server_addr). Use this one for every datagram a title reads from its
 * P2P socket. */
int     psnr_relay_unwrap_from(const psnr_client* c, const uint8_t src_ip[4], uint16_t src_port,
                               const void* buf, size_t len, uint32_t* from_user,
                               const uint8_t** payload, size_t* payload_len);
int64_t psnr_stream_connect(const psnr_client* c, uint32_t to_user, uint16_t vport, int timeout_ms);
int64_t psnr_stream_accept(const psnr_client* c, uint32_t stream_id, int timeout_ms);

/* LAN discovery. One server found on the local network. name and version are
 * NUL-terminated printable ASCII; ip is where the reply came from, tcp_port is
 * the port to give psnr_connect. */
typedef struct {
    uint8_t  ip[4];
    uint16_t tcp_port;
    uint16_t udp_port;
    uint8_t  relay;      /* 1 if the server's relay is on */
    uint16_t players;
    char     name[25];
    char     version[13];
} psnr_found;

/* Looks for servers on the local network (the DISCOVER packets in docs/api.md). Opens
 * its own temporary UDP socket, never a title socket, sets SO_BROADCAST, sends
 * one request to 255.255.255.255:port and, on POSIX, to each IPv4 interface's
 * directed broadcast address (Windows: 255.255.255.255 only), then collects
 * replies until timeout_ms has passed or max are found, and closes the socket.
 * port is the UDP port to probe; 0 means 36100, the default server port. Fills
 * out[0..max) and returns the number found, or -1 on error. Only a server
 * started with its discovery option answers, and only same-subnet, non-isolated
 * networks are reached: keep a manual host:port path. IPv4 only, no threads,
 * blocks for about timeout_ms at most, and does not touch psnr_connect_error. */
int psnr_discover(uint16_t port, int timeout_ms, psnr_found* out, int max);

/* Last HELLO refusal diagnostic, process-global (PSNR_E_NAME_TAKEN: another
 * player has that name). An earlier DNS/TCP failure may retain a prior code;
 * do not treat it as the definitive cause of every NULL connect result. */
int psnr_connect_error(void);
void psnr_close(psnr_client* c);

/* Transmit a request synchronously; may wait for socket writability, with no
 * time limit. body excludes the request id. Returns the id, or 0 on failure;
 * after a failed write the connection is unusable. */
uint32_t psnr_send(psnr_client* c, uint8_t type, const void* body, uint32_t len);

/* Round trip reported to the server. The library times every psnr_call (send to
 * reply, so the server's own time is included) and keeps the smallest of the last
 * eight; psnr_rtt_ms returns it in milliseconds, or -1 before the first reply.
 * HEARTBEAT (automatic in psnr_poll, or psnr_heartbeat) carries it as an optional
 * 2-byte body, which the server shows next to the player's name on its status
 * page as reported by the player. Servers that do not know the field ignore it.
 * It is off until the title calls psnr_report_rtt(c, 1), which is recommended: hosts then see
 * who is lagging. psnr_report_rtt(c, 0) stops it again; nothing else changes.
 * psnr_heartbeat sends a HEARTBEAT now and returns the request id, or 0 on failure. */
int      psnr_rtt_ms(const psnr_client* c);
void     psnr_report_rtt(psnr_client* c, int on);
uint32_t psnr_heartbeat(psnr_client* c);

/* 1: *out holds the next reply or push. 0: nothing yet. -1: disconnected after
 * queued messages drain. Automatic heartbeat sends may wait for writability. */
int psnr_poll(psnr_client* c, psnr_msg* out);

/* Send, then wait up to timeout_ms for that request's reply (not a full-operation
 * deadline). Timeout does not cancel the request; a late reply can still arrive.
 * Pushes and other replies stay queued for psnr_poll. 1 / 0 (timeout) / -1. */
int psnr_call(psnr_client* c, uint8_t type, const void* body, uint32_t len,
              psnr_msg* reply, int timeout_ms);

void psnr_msg_free(psnr_msg* m);

/* Big-endian field helpers for building and reading payloads. */
static inline uint8_t* psnr_put16(uint8_t* p, uint16_t v) { p[0] = (uint8_t)(v >> 8); p[1] = (uint8_t)v; return p + 2; }
static inline uint8_t* psnr_put32(uint8_t* p, uint32_t v) { psnr_put16(p, (uint16_t)(v >> 16)); return psnr_put16(p + 2, (uint16_t)v); }
static inline uint8_t* psnr_put64(uint8_t* p, uint64_t v) { psnr_put32(p, (uint32_t)(v >> 32)); return psnr_put32(p + 4, (uint32_t)v); }
static inline uint16_t psnr_get16(const uint8_t* p) { return (uint16_t)(p[0] << 8 | p[1]); }
static inline uint32_t psnr_get32(const uint8_t* p) { return (uint32_t)psnr_get16(p) << 16 | psnr_get16(p + 2); }
static inline uint64_t psnr_get64(const uint8_t* p) { return (uint64_t)psnr_get32(p) << 32 | psnr_get32(p + 4); }

#ifdef __cplusplus
}
#endif

#endif /* PSNR_H */
