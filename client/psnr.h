/* SPDX-License-Identifier: MIT (see LICENSE) */
/**
 * @file psnr.h
 * @brief psnr.h - client for the psnr server (docs/api.md), in C for ps3recomp's
 * sceNp modules to vendor.
 *
 * No threads. psnr_send() transmits a request and returns its id; the reply and
 * any server pushes come back out of psnr_poll(). Its receive path does not
 * wait for new data. A write may wait for writability, but each write of a request
 * (the header, then the body) waits no longer than PSNR_SEND_TIMEOUT_MS; then the
 * connection is dead (psnr_dead_reason).
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
 * returns. A host can refuse the first two with psnr_set_connect_policy. It never opens, reads, writes or closes
 * a socket of the title's; the UDP peer socket is the title's, and the library
 * only builds and reads the packets it exchanges. Connecting and disconnecting
 * are explicit: nothing connects until the title calls psnr_connect, nothing
 * reconnects by itself (psnr_poll returns -1 once the connection is lost), and
 * psnr_close closes it. A title that never calls psnr_connect sends nothing.
 * Process-wide effects: on Windows each psnr_connect and psnr_discover calls WSAStartup (2.2) and
 * the library never calls WSACleanup; psnr_connect_error and the connect policy
 * (psnr_set_connect_policy) are process-global values; the library starts no threads and installs no signal handlers or
 * timers. It sends with plain send(), so on POSIX a write to a connection the
 * server has closed can raise SIGPIPE unless the title ignores that signal. It
 * resolves and connects over IPv4 only.
 */

/**
 * @defgroup psnr_connect Connect and poll
 * @brief Connecting, sending, polling for replies and pushes, errors, sign-in passes and wire helpers.
 *
 * @defgroup psnr_rooms Rooms
 * @brief Decoders for room payloads: joined rooms, members and room lists.
 *
 * @defgroup psnr_scores Scores
 * @brief Decoders for ranking payloads.
 *
 * @defgroup psnr_relay Relay streams
 * @brief NAT traversal packets, and the relay's datagrams and streams for peers that cannot reach each other directly.
 *
 * @defgroup psnr_discovery Discovery
 * @brief Finding servers on the local network.
 */
#ifndef PSNR_H
#define PSNR_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/** @addtogroup psnr_connect
 *  @{ */

/**
 * @brief Message types; payloads in docs/api.md.
 *
 * Values below 0x80 are requests; the rest are replies and pushes (a push
 * has req 0 in psnr_msg).
 */
enum {
    PSNR_HELLO = 0x01,          /**< request: HELLO, the first message on a connection */
    PSNR_HEARTBEAT = 0x02,      /**< request: HEARTBEAT, keeps the connection alive (see psnr_heartbeat) */
    PSNR_PEER_AUTH = 0x03,      /**< request: PEER_AUTH */
    PSNR_GET_CAPS = 0x04,       /**< request: GET_CAPS, answered by PSNR_CAPS (see psnr_caps_parse) */
    PSNR_AUTH = 0x05,           /**< request: AUTH, sends a sign-in pass (see psnr_auth) */
    PSNR_CREATE_ROOM = 0x10,    /**< request: CREATE_ROOM */
    PSNR_SEARCH_ROOMS = 0x11,   /**< request: SEARCH_ROOMS, answered by PSNR_ROOM_LIST */
    PSNR_JOIN_ROOM = 0x12,      /**< request: JOIN_ROOM, answered by PSNR_ROOM_JOINED */
    PSNR_LEAVE_ROOM = 0x13,     /**< request: LEAVE_ROOM */
    PSNR_SET_ROOM_DATA = 0x14,  /**< request: SET_ROOM_DATA */
    PSNR_ROOM_MESSAGE = 0x15,   /**< request: ROOM_MESSAGE */
    PSNR_KICK_MEMBER = 0x16,    /**< request: KICK_MEMBER */
    PSNR_SET_ROOM_ACCESS = 0x17,/**< request: SET_ROOM_ACCESS */
    PSNR_JOIN_BY_CODE = 0x18,   /**< request: JOIN_BY_CODE */
    PSNR_GET_ROOM_ACCESS = 0x19,/**< request: GET_ROOM_ACCESS, answered by PSNR_ROOM_ACCESS */
    PSNR_RECORD_SCORE = 0x20,   /**< request: RECORD_SCORE, answered by PSNR_SCORE_RECORDED */
    PSNR_GET_RANKING = 0x21,    /**< request: GET_RANKING, answered by PSNR_RANKING */
    PSNR_GET_RANKING_BY_ID = 0x22, /**< request: GET_RANKING_BY_ID, answered by PSNR_RANKING */

    PSNR_HELLO_ACK = 0x81,      /**< reply: HELLO_ACK */
    PSNR_CAPS = 0x94,           /**< reply: CAPS, to PSNR_GET_CAPS (see psnr_caps_parse) */
    PSNR_ROOM_JOINED = 0x90,    /**< reply: ROOM_JOINED (see psnr_room_joined_parse) */
    PSNR_ROOM_LIST = 0x91,      /**< reply: ROOM_LIST (see psnr_room_list_parse) */
    PSNR_ROOM_ACCESS = 0x97,    /**< reply: ROOM_ACCESS, to PSNR_GET_ROOM_ACCESS */
    PSNR_OK = 0x93,             /**< reply: OK */
    PSNR_SCORE_RECORDED = 0xB0, /**< reply: SCORE_RECORDED, to PSNR_RECORD_SCORE */
    PSNR_RANKING = 0xB1,        /**< reply: RANKING (see psnr_ranking_parse) */
    PSNR_ERROR = 0x8F,          /**< reply: ERROR, with one of the PSNR_E_* codes */

    PSNR_MEMBER_JOINED = 0xA1,  /**< push: a member joined the room */
    PSNR_MEMBER_LEFT = 0xA2,    /**< push: a member left the room */
    PSNR_ROOM_DATA = 0xA3,      /**< push: room data changed */
    PSNR_ROOM_MSG = 0xA4,       /**< push: a room message */
    PSNR_KICKED = 0xA5,         /**< push: this player was kicked from the room */
    PSNR_PEER_KEY = 0xA8,       /**< push: PEER_KEY (see PSNR_CAP_PEER_KEYS) */

    /** the relay (docs/api.md, "Relay") - request STREAM_CONNECT (see psnr_stream_connect) */
    PSNR_STREAM_CONNECT = 0x30,
    PSNR_STREAM_ACCEPT = 0x31,  /**< the relay: request STREAM_ACCEPT (see psnr_stream_accept) */
    PSNR_STREAM_READY = 0x96,   /**< the relay: reply STREAM_READY */
    PSNR_STREAM_OFFER = 0xA6,   /**< the relay: push STREAM_OFFER (stream_id u32 | from_user u32 | vport u16) */
    PSNR_ROUTE = 0xA7           /**< the relay: push ROUTE, with flags such as PSNR_ROUTE_STREAM_RELAY */
};
/** ROUTE flags: streams to that player go through the relay */
#define PSNR_ROUTE_STREAM_RELAY 1

/**
 * @brief ERROR codes
 *
 * The code carried by a PSNR_ERROR reply (payloads in docs/api.md).
 */
enum {
    PSNR_E_BAD_REQUEST = 1,  /**< ERROR code 1: bad request */
    PSNR_E_NOT_FOUND = 2,    /**< ERROR code 2: not found */
    PSNR_E_ROOM_FULL = 3,    /**< ERROR code 3: room full */
    PSNR_E_NOT_OWNER = 4,    /**< ERROR code 4: not the room's owner */
    PSNR_E_NOT_IN_ROOM = 5,  /**< ERROR code 5: not in a room */
    PSNR_E_LIMIT = 6,        /**< ERROR code 6: a limit was reached */
    PSNR_E_NO_HELLO = 7,     /**< ERROR code 7: no HELLO was sent first */
    PSNR_E_NAME_TAKEN = 8,   /**< ERROR code 8: name taken */
    PSNR_E_NO_RELAY = 9,     /**< ERROR code 9: no relay */
    /** a sign-in pass is needed, or the one sent was refused (psnr_auth) */
    PSNR_E_PASS = 10
};

/**
 * @brief Set by the client itself, not sent by a server: the host's connect policy refused every address
 * (see psnr_set_connect_policy). Reported by psnr_connect_error.
 */
#define PSNR_E_POLICY 1000

/** @brief A connection to a psnr server (opaque); made by psnr_connect, freed by psnr_close. */
typedef struct psnr_client psnr_client;

/**
 * @brief One reply or push.
 *
 * req is 0 for pushes. data excludes the request id and
 * is freed by psnr_msg_free.
 */
typedef struct {
    uint8_t  type;   /**< message type (a PSNR_* message type) */
    uint32_t req;    /**< the request id this answers; 0 for pushes */
    uint8_t* data;   /**< the payload, excluding the request id; freed by psnr_msg_free */
    uint32_t len;    /**< length of data in bytes */
} psnr_msg;

/**
 * @name Name limits
 * Name limits on the wire: comm_id 12 bytes, online_id 16, NUL-padded and not necessarily
 * terminated. psnr_connect refuses longer ones (NULL) instead of cutting them. Copy a name
 * you read from a payload into a char[PSNR_ONLINE_ID_MAX + 1] and terminate it.
 * @{
 */
#define PSNR_COMM_ID_MAX   12  /**< longest comm_id on the wire, in bytes */
#define PSNR_ONLINE_ID_MAX 16  /**< longest online_id on the wire, in bytes */
/** @} */

/**
 * @brief Blocking connect and HELLO setup.
 *
 * (DNS/connect/send are not bounded by a
 * caller deadline; psnr_connect_timeout bounds the connect and HELLO). comm_id is the title's NP communication ID
 * ("NPWR00860_00"), online_id the player's name, p2p_port the UDP port the
 * title takes peer traffic on (0 if none). Fills the id the server assigned
 * and the address it sees this client at. NULL on failure.
 * After connecting, it still waits a fixed 5 seconds for HELLO_ACK.
 *
 * @param host          Server host name or address.
 * @param port          Server TCP port.
 * @param comm_id       The title's NP communication ID, at most PSNR_COMM_ID_MAX bytes.
 * @param online_id     The player's name, at most PSNR_ONLINE_ID_MAX bytes.
 * @param p2p_port      The UDP port the title takes peer traffic on (0 if none).
 * @param out_user_id   Receives the id the server assigned.
 * @param out_public_ip Receives the address the server sees this client at.
 * @return The connection, or NULL on failure (see psnr_connect_error).
 * @note May block; see psnr_connect_timeout for a time limit.
 */
psnr_client* psnr_connect(const char* host, uint16_t port,
                          const char* comm_id, const char* online_id, uint16_t p2p_port,
                          uint32_t* out_user_id, uint8_t out_public_ip[4]);

/**
 * @brief psnr_connect with a time limit.
 *
 * The TCP connect is non-blocking and waits at most timeout_ms,
 * and so does the wait for HELLO_ACK: timeout_ms is one budget for both, the HELLO wait gets what
 * the connect left. timeout_ms <= 0 gives NULL. Otherwise the same as psnr_connect, which keeps its
 * blocking connect. DNS (getaddrinfo) is still blocking and is NOT covered by timeout_ms; a numeric
 * IPv4 host avoids it. NULL on failure, and on timeout.
 *
 * @param host          Server host name or address.
 * @param port          Server TCP port.
 * @param comm_id       The title's NP communication ID.
 * @param online_id     The player's name.
 * @param p2p_port      The UDP port the title takes peer traffic on (0 if none).
 * @param out_user_id   Receives the id the server assigned.
 * @param out_public_ip Receives the address the server sees this client at.
 * @param timeout_ms    One budget in milliseconds for the connect and the HELLO wait.
 * @return The connection, or NULL on failure or timeout.
 */
psnr_client* psnr_connect_timeout(const char* host, uint16_t port,
                                  const char* comm_id, const char* online_id, uint16_t p2p_port,
                                  uint32_t* out_user_id, uint8_t out_public_ip[4], int timeout_ms);

/**
 * @brief Why a connect policy callback is being asked.
 *
 * Connect policy: lets the host title (or the runtime around it) refuse a TCP connection before the
 * client opens it. Why a callback: the client opens these sockets itself, so a host that restricts
 * where a game may connect cannot otherwise see or stop them.
 *   PSNR_PURPOSE_SERVER  the connection psnr_connect or psnr_connect_timeout makes. host is the name
 *                        as typed; ip is one address it resolved to. The callback runs once per
 *                        resolved address, before the socket is made; a refused address is skipped
 *                        and the next one tried. If every address is refused, the connect returns
 *                        NULL and psnr_connect_error() is PSNR_E_POLICY.
 *   PSNR_PURPOSE_STREAM  a relay leg (psnr_stream_connect, psnr_stream_accept) to the server the
 *                        client is connected to. host is NULL; ip is the server's address. A refusal
 *                        returns -1 and opens nothing.
 * The callback returns nonzero to allow and 0 to refuse. No callback registered (the default, or
 * NULL) allows everything, exactly as before. The registration is process-wide, because
 * psnr_connect has no client yet: set it before the first connect, from the thread that connects;
 * the library keeps no lock. The callback must not call back into the library. Not covered:
 * psnr_discover (its UDP socket exists only if the title calls it) and the title's own P2P socket.
 */
enum {
    PSNR_PURPOSE_SERVER = 1, /**< the connection psnr_connect or psnr_connect_timeout makes */
    PSNR_PURPOSE_STREAM = 2  /**< a relay leg to the server the client is connected to */
};

/**
 * @brief A connect policy callback (see the PSNR_PURPOSE_* values and psnr_set_connect_policy).
 *
 * @param user    The pointer given to psnr_set_connect_policy.
 * @param purpose PSNR_PURPOSE_SERVER or PSNR_PURPOSE_STREAM.
 * @param host    The name as typed (PSNR_PURPOSE_SERVER), or NULL (PSNR_PURPOSE_STREAM).
 * @param ip      The address about to be connected to.
 * @param port    The port about to be connected to.
 * @return Nonzero to allow the connection, 0 to refuse it.
 */
typedef int (*psnr_connect_policy_fn)(void* user, int purpose, const char* host,
                                      const uint8_t ip[4], uint16_t port);

/**
 * @brief Registers the process-wide connect policy callback.
 *
 * @param fn   The callback, or NULL for none (allows everything).
 * @param user Passed to the callback.
 * @note Process-wide: set it before the first connect, from the thread that connects; the library keeps no lock.
 * See the PSNR_PURPOSE_* values for what it covers.
 */
void psnr_set_connect_policy(psnr_connect_policy_fn fn, void* user);

/** @} */

/** @addtogroup psnr_relay
 *  @{ */

/**
 * @brief Length in bytes of the UDP PROBE packet built by psnr_probe_packet.
 *
 * NAT traversal (docs/api.md, "UDP"). The caller owns the P2P socket; these
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
 *   - Drop anything psnr_is_control() matches before the title sees it.
 */
#define PSNR_UDP_PROBE_LEN 17
/** @brief Length in bytes of the UDP PUNCH packet built by psnr_punch_packet. */
#define PSNR_UDP_PUNCH_LEN 9
/**
 * @brief Largest payload psnr_relay_wrap takes, in bytes.
 *
 * An IPv4 UDP datagram holds 65507 bytes and the relay header takes 17 of
 * them (docs/api.md, "What the relay carries"). psnr_relay_wrap returns 0 for
 * a longer payload.
 */
#define PSNR_RELAY_MAX_PAYLOAD 65490

/**
 * @brief Gives the server's UDP address.
 *
 * @param c    The connection.
 * @param ip   Receives the server's IPv4 address.
 * @param port Receives the server's UDP port.
 */
void psnr_server_addr(const psnr_client* c, uint8_t ip[4], uint16_t* port);

/**
 * @brief Builds a PROBE packet (see PSNR_UDP_PROBE_LEN for how to use it).
 *
 * @param c   The connection.
 * @param out Receives the packet.
 */
void psnr_probe_packet(const psnr_client* c, uint8_t out[PSNR_UDP_PROBE_LEN]);

/**
 * @brief Builds a PUNCH packet (see PSNR_UDP_PROBE_LEN for how to use it).
 *
 * @param c   The connection.
 * @param out Receives the packet.
 */
void psnr_punch_packet(const psnr_client* c, uint8_t out[PSNR_UDP_PUNCH_LEN]);

/**
 * @brief Tells whether a UDP packet is one of this library's control packets.
 *
 * Drop anything this matches before the title sees it.
 *
 * @param buf The received packet.
 * @param len Its length in bytes.
 * @return Nonzero if it is a control packet, 0 if not.
 */
int  psnr_is_control(const void* buf, size_t len);

/**
 * @brief Reads a probe reply.
 *
 * 1 if buf is a probe reply; fills the public endpoint the server saw.
 *
 * @param buf  The received packet.
 * @param len  Its length in bytes.
 * @param ip   Receives the public IPv4 address the server saw.
 * @param port Receives the public port the server saw.
 * @return 1 if buf is a probe reply, 0 if not.
 */
int  psnr_probe_reply(const void* buf, size_t len, uint8_t ip[4], uint16_t* port);

/**
 * @brief Tells whether the relay is available.
 *
 * The relay, for peers that can't reach each other directly. It is there only
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
 *   stream's raw bytes (a SOCKET on Windows, an fd elsewhere), or -1. A nonnegative
 *   timeout_ms bounds the complete TCP connect, setup write and READY reply; fragments
 *   share that one budget, including retries after interrupted waits. A negative
 *   value waits without a deadline. The host's connect-policy callback runs
 *   before that budget starts.
 *
 * @param c The connection.
 * @return Nonzero if the server offers the relay (see PSNR_CAP_RELAY), 0 if not.
 */
int     psnr_relay_available(const psnr_client* c);

/**
 * @brief Wraps a payload for a peer, to send to psnr_server_addr from the P2P socket.
 *
 * @param c       The connection.
 * @param to_user The user id of the peer.
 * @param payload The payload to wrap.
 * @param len     Length of payload in bytes, at most PSNR_RELAY_MAX_PAYLOAD.
 * @param out     Receives the wrapped datagram.
 * @param cap     Size of out in bytes.
 * @return The length written to out (17 + len), or 0 if len is over
 *         PSNR_RELAY_MAX_PAYLOAD or the datagram doesn't fit in cap; out is
 *         then left as it was.
 */
size_t  psnr_relay_wrap(const psnr_client* c, uint32_t to_user, const void* payload, size_t len,
                        uint8_t* out, size_t cap);

/**
 * @brief Unwraps a relay datagram.
 *
 * Does not check the source: prefer psnr_relay_unwrap_from. Kept for
 * compatibility; it accepts a datagram from anywhere.
 *
 * @param buf         The received datagram.
 * @param len         Its length in bytes.
 * @param from_user   Receives the sender's user id.
 * @param payload     Receives a pointer to the payload, inside buf.
 * @param payload_len Receives the payload's length in bytes.
 * @return Nonzero on success, 0 if buf is not a relay datagram.
 */
int     psnr_relay_unwrap(const void* buf, size_t len, uint32_t* from_user,
                          const uint8_t** payload, size_t* payload_len);

/**
 * @brief psnr_relay_unwrap with a source check.
 *
 * psnr_relay_unwrap, but returns 0 unless the datagram's source (src_ip,
 * src_port, as the UDP socket reports it) is the server's UDP address
 * (psnr_server_addr). Use this one for every datagram a title reads from its
 * P2P socket.
 *
 * @param c           The connection.
 * @param src_ip      The datagram's source address, as the UDP socket reports it.
 * @param src_port    The datagram's source port.
 * @param buf         The received datagram.
 * @param len         Its length in bytes.
 * @param from_user   Receives the sender's user id.
 * @param payload     Receives a pointer to the payload, inside buf.
 * @param payload_len Receives the payload's length in bytes.
 * @return Nonzero on success, 0 if the source is not the server or buf is not a relay datagram.
 */
int     psnr_relay_unwrap_from(const psnr_client* c, const uint8_t src_ip[4], uint16_t src_port,
                               const void* buf, size_t len, uint32_t* from_user,
                               const uint8_t** payload, size_t* payload_len);

/**
 * @brief Opens a relay stream to a peer.
 *
 * Returns a connected, blocking socket carrying the stream's raw bytes (a SOCKET on Windows, an
 * fd elsewhere), or -1. See psnr_relay_available for the timeout rules.
 *
 * @param c          The connection.
 * @param to_user    The user id of the peer.
 * @param vport      The virtual port on the peer.
 * @param timeout_ms A nonnegative value bounds the whole connect, setup write and READY reply; negative waits without a deadline.
 * @return The socket (the caller owns and closes it), or -1.
 */
int64_t psnr_stream_connect(const psnr_client* c, uint32_t to_user, uint16_t vport, int timeout_ms);

/**
 * @brief Accepts a relay stream a peer offered with a STREAM_OFFER push.
 *
 * Returns a connected, blocking socket carrying the stream's raw bytes (a SOCKET on Windows, an
 * fd elsewhere), or -1. See psnr_relay_available for the timeout rules.
 *
 * @param c          The connection.
 * @param stream_id  The stream_id from the STREAM_OFFER push.
 * @param timeout_ms A nonnegative value bounds the whole connect, setup write and READY reply; negative waits without a deadline.
 * @return The socket (the caller owns and closes it), or -1.
 */
int64_t psnr_stream_accept(const psnr_client* c, uint32_t stream_id, int timeout_ms);

/** @} */

/** @addtogroup psnr_discovery
 *  @{ */

/**
 * @brief LAN discovery. One server found on the local network.
 *
 * name and version are
 * NUL-terminated printable ASCII; ip is where the reply came from, tcp_port is
 * the port to give psnr_connect.
 */
typedef struct {
    uint8_t  ip[4];      /**< where the reply came from */
    uint16_t tcp_port;   /**< the port to give psnr_connect */
    uint16_t udp_port;   /**< the server's UDP port */
    uint8_t  relay;      /**< 1 if the server's relay is on */
    uint16_t players;    /**< the number of players on the server */
    char     name[25];   /**< the server's name, NUL-terminated printable ASCII */
    char     version[13];/**< the server's version, NUL-terminated printable ASCII */
} psnr_found;

/**
 * @brief Looks for servers on the local network (the DISCOVER packets in docs/api.md). Opens
 * its own temporary UDP socket, never a title socket, sets SO_BROADCAST, sends
 * one request to 255.255.255.255:port and, on POSIX, to each IPv4 interface's
 * directed broadcast address (Windows: 255.255.255.255 only), then collects
 * replies until timeout_ms has passed or max are found, and closes the socket.
 * port is the UDP port to probe; 0 means 36100, the default server port. Fills
 * out[0..max) and returns the number found, or -1 on error. Only a server
 * started with its discovery option answers, and only same-subnet, non-isolated
 * networks are reached: keep a manual host:port path. IPv4 only, no threads,
 * blocks for about timeout_ms at most, and does not touch psnr_connect_error.
 *
 * @param port       The UDP port to probe; 0 means 36100.
 * @param timeout_ms How long to collect replies, in milliseconds.
 * @param out        Receives the servers found, out[0..max).
 * @param max        The size of out, in entries.
 * @return The number found, or -1 on error.
 */
int psnr_discover(uint16_t port, int timeout_ms, psnr_found* out, int max);

/** @} */

/** @addtogroup psnr_connect
 *  @{ */

/**
 * @brief Last HELLO refusal diagnostic, process-global (PSNR_E_NAME_TAKEN: another
 * player has that name). An earlier DNS/TCP failure may retain a prior code;
 * do not treat it as the definitive cause of every NULL connect result.
 *
 * @return 0 if none; else the ERROR code the server sent, or PSNR_E_POLICY.
 */
int psnr_connect_error(void);

/**
 * @brief Closes the connection and frees the client.
 *
 * @param c The connection.
 */
void psnr_close(psnr_client* c);

/**
 * @brief Why the connection was lost. PSNR_DEAD_NONE while it is up.
 *
 * Set once, at the first failure:
 *   PEER_CLOSED  the server closed the connection (a read returned end of stream); this includes a
 *                server that ends a connection after repeated refused passes or a revoked account
 *   IO_ERROR     a read or write failed; psnr_last_os_error says why
 *   NOMEM        an allocation failed
 *   SEND_TIMEOUT one write of a request waited PSNR_SEND_TIMEOUT_MS for writability
 */
enum psnr_dead_reason {
    PSNR_DEAD_NONE = 0,       /**< the connection is up */
    PSNR_DEAD_PEER_CLOSED,    /**< the server closed the connection */
    PSNR_DEAD_IO_ERROR,       /**< a read or write failed */
    PSNR_DEAD_NOMEM,          /**< an allocation failed */
    PSNR_DEAD_SEND_TIMEOUT    /**< one write of a request waited PSNR_SEND_TIMEOUT_MS */
};

/**
 * @brief Gives the reason the connection was lost (see enum psnr_dead_reason).
 *
 * @param c The connection.
 * @return A PSNR_DEAD_* value; PSNR_DEAD_NONE while it is up.
 */
int psnr_dead_reason(const psnr_client* c);

/**
 * @brief errno (POSIX) or WSAGetLastError() (Windows), captured when the connection was marked dead;
 * 0 if there was none (a peer close, a timeout).
 *
 * @param c The connection.
 * @return The captured error number, or 0.
 */
int psnr_last_os_error(const psnr_client* c);

/**
 * @def PSNR_SEND_TIMEOUT_MS
 * @brief How long each write of a request (header, then body) may wait for writability before the
 * connection is marked dead (PSNR_DEAD_SEND_TIMEOUT). Define it before including this header to change it.
 */
#ifndef PSNR_SEND_TIMEOUT_MS
#  define PSNR_SEND_TIMEOUT_MS 5000
#endif

/**
 * @brief Transmit a request synchronously; may wait for socket writability, for at most
 * PSNR_SEND_TIMEOUT_MS for each write of the request (header, then body). body excludes the request id.
 * Returns the id, or 0 on failure; after a failed write the connection is unusable.
 *
 * @param c    The connection.
 * @param type The request type (a PSNR_* message type).
 * @param body The request body, excluding the request id.
 * @param len  Length of body in bytes, at most 65531.
 * @return The request id, or 0 on failure, if the connection is already dead, or if len is too long.
 */
uint32_t psnr_send(psnr_client* c, uint8_t type, const void* body, uint32_t len);

/**
 * @brief Round trip reported to the server.
 *
 * The library times every psnr_call (send to
 * reply, so the server's own time is included) and keeps the smallest of the last
 * eight; psnr_rtt_ms returns it in milliseconds, or -1 before the first reply.
 * HEARTBEAT (automatic in psnr_poll, or psnr_heartbeat) carries it as an optional
 * 2-byte body, which the server shows next to the player's name on its status
 * page as reported by the player. Servers that do not know the field ignore it.
 * It is off until the title calls psnr_report_rtt(c, 1), which is recommended: hosts then see
 * who is lagging. psnr_report_rtt(c, 0) stops it again; nothing else changes.
 * psnr_heartbeat sends a HEARTBEAT now and returns the request id, or 0 on failure.
 *
 * @param c The connection.
 * @return The smallest of the last eight round trips in milliseconds, or -1 before the first reply.
 */
int      psnr_rtt_ms(const psnr_client* c);

/**
 * @brief Turns reporting of the round trip in HEARTBEAT on or off (see psnr_rtt_ms).
 *
 * @param c  The connection.
 * @param on nonzero to report (recommended), 0 to stop.
 */
void     psnr_report_rtt(psnr_client* c, int on);

/**
 * @brief Sends a HEARTBEAT now (see psnr_rtt_ms).
 *
 * @param c The connection.
 * @return The request id, or 0 on failure.
 */
uint32_t psnr_heartbeat(psnr_client* c);

/**
 * @brief Polls for the next reply or push.
 *
 * 1: *out holds the next reply or push. 0: nothing yet. -1: disconnected after
 * queued messages drain (psnr_dead_reason says why). Automatic heartbeat sends may wait for
 * writability, as psnr_send does.
 *
 * @param c   The connection.
 * @param out Receives the message; free it with psnr_msg_free.
 * @return 1 if *out holds a message, 0 if nothing yet, -1 if disconnected.
 */
int psnr_poll(psnr_client* c, psnr_msg* out);

/**
 * @brief Send, then wait for that request's reply.
 *
 * Send, then wait up to timeout_ms for that request's reply (not a full-operation
 * deadline). Timeout does not cancel the request; a late reply can still arrive.
 * Pushes and other replies stay queued for psnr_poll. 1 / 0 (timeout) / -1.
 * A server ERROR is a reply like any other: the return is 1 and reply->type is PSNR_ERROR, with
 * the code in the first four bytes of reply->data. Only psnr_auth and psnr_connect decode it for you.
 *
 * @param c          The connection.
 * @param type       The request type (a PSNR_* message type).
 * @param body       The request body, excluding the request id.
 * @param len        Length of body in bytes.
 * @param reply      Receives the reply; free it with psnr_msg_free.
 * @param timeout_ms How long to wait for the reply, in milliseconds.
 * @return 1 if *reply holds the reply, 0 on timeout, -1 on failure.
 */
int psnr_call(psnr_client* c, uint8_t type, const void* body, uint32_t len,
              psnr_msg* reply, int timeout_ms);

/**
 * @brief Frees the data of a message from psnr_poll or psnr_call.
 *
 * @param m The message.
 */
void psnr_msg_free(psnr_msg* m);

/** @} */

/** @addtogroup psnr_rooms
 *  @{ */

/**
 * @brief A cursor over a list of entries in a payload.
 *
 * Decoders for the payloads in docs/api.md. Each checks every length against the message
 * before it reads a field, so a short or damaged payload gives 0 and never reads past the
 * end. The pointers inside a result (blobs) point into the psnr_msg's data: they are valid
 * until psnr_msg_free. online_id fields come out NUL-terminated.
 *
 *   psnr_msg_free(&m) after psnr_room_joined_parse(&m, &rj) ... psnr_member_next(&rj.members, &mb)
 *   is the pattern: a cursor starts valid and next returns 1 per entry, then 0 at the end.
 */
typedef struct {
    const uint8_t* p;  /**< the next entry */
    size_t left;       /**< bytes left */
    uint32_t count;    /**< entries left */
} psnr_cursor;

/** @brief One member of a room. */
typedef struct {
    uint16_t member_id;       /**< the member's id in the room */
    uint32_t user_id;         /**< the member's user id */
    char     online_id[PSNR_ONLINE_ID_MAX + 1]; /**< the member's name, NUL-terminated */
    uint8_t  ip[4];           /**< the member's public IPv4 address */
    uint16_t p2p_port;        /**< the UDP port the member takes peer traffic on */
    uint8_t  owner;           /**< nonzero if the member owns the room */
    const uint8_t* data;      /**< the member's own data blob */
    uint16_t data_len;        /**< length of data in bytes */
} psnr_member;

/** @brief A ROOM_JOINED reply, decoded by psnr_room_joined_parse. */
typedef struct {
    uint64_t room_id;         /**< the room's id */
    uint16_t my_member, /**< this player's member id */
             owner;     /**< the owner's member id */
    uint8_t  max;             /**< the room's capacity */
    uint32_t flags;           /**< the room's flags */
    const uint8_t* external;  /**< the room's external data blob */
    uint16_t external_len;    /**< length of external in bytes */
    const uint8_t* internal;  /**< the room's internal data blob */
    uint16_t internal_len;    /**< length of internal in bytes */
    psnr_cursor members;      /**< read with psnr_member_next */
} psnr_room_joined;

/** @brief One room in a ROOM_LIST reply, read with psnr_room_next. */
typedef struct {
    uint64_t room_id;         /**< the room's id */
    char     owner[PSNR_ONLINE_ID_MAX + 1]; /**< the owner's name, NUL-terminated */
    uint8_t  cur,             /**< the number of members now */
             max;             /**< the room's capacity */
    uint32_t flags;           /**< the room's flags */
    const uint8_t* external;  /**< the room's external data blob */
    uint16_t external_len;    /**< length of external in bytes */
} psnr_room;

/** @brief One entry of a RANKING reply, read with psnr_rank_next.
 *  @ingroup psnr_scores */
typedef struct {
    uint32_t rank;            /**< the rank */
    char     online_id[PSNR_ONLINE_ID_MAX + 1]; /**< the player's name, NUL-terminated */
    int64_t  score;           /**< the score */
    const uint8_t* comment;   /**< the entry's comment blob */
    uint16_t comment_len;     /**< length of comment in bytes */
    uint64_t recorded_unix;   /**< when the score was recorded, as a Unix time */
} psnr_rank;

/**
 * @brief Decodes a ROOM_JOINED reply.
 *
 * ROOM_JOINED; 1 ok, 0 bad
 *
 * @param m   The reply.
 * @param out Receives the decoded reply; its pointers point into m's data.
 * @return 1 ok, 0 bad.
 */
int psnr_room_joined_parse(const psnr_msg* m, psnr_room_joined* out);

/**
 * @brief Decodes one member entry.
 *
 * a MEMBER_JOINED body after room_id, or one entry; 1 ok
 *
 * @param p    The start of the entry.
 * @param len  Bytes available at p.
 * @param out  Receives the member.
 * @param used Receives the number of bytes the entry took; may be NULL.
 * @return 1 ok, 0 bad.
 */
int psnr_member_parse(const uint8_t* p, size_t len, psnr_member* out, size_t* used);

/**
 * @brief Reads the next member from a cursor.
 *
 * @param c   The cursor, such as psnr_room_joined::members.
 * @param out Receives the member.
 * @return 1 per entry, then 0 at the end.
 */
int psnr_member_next(psnr_cursor* c, psnr_member* out);

/**
 * @brief Decodes a ROOM_LIST reply.
 *
 * ROOM_LIST; 1 ok, 0 bad
 *
 * @param m     The reply.
 * @param c     Receives a cursor to read with psnr_room_next.
 * @param total Receives the total number of rooms; may be NULL.
 * @return 1 ok, 0 bad.
 */
int psnr_room_list_parse(const psnr_msg* m, psnr_cursor* c, uint16_t* total);

/**
 * @brief Reads the next room from a cursor.
 *
 * @param c   The cursor from psnr_room_list_parse.
 * @param out Receives the room.
 * @return 1 per entry, then 0 at the end.
 */
int psnr_room_next(psnr_cursor* c, psnr_room* out);

/** @} */

/** @addtogroup psnr_scores
 *  @{ */

/**
 * @brief Decodes a RANKING reply.
 *
 * RANKING; 1 ok, 0 bad
 *
 * @param m     The reply.
 * @param c     Receives a cursor to read with psnr_rank_next.
 * @param total Receives the total number of entries.
 * @return 1 ok, 0 bad.
 */
int psnr_ranking_parse(const psnr_msg* m, psnr_cursor* c, uint32_t* total);

/**
 * @brief Reads the next entry from a cursor.
 *
 * @param c   The cursor from psnr_ranking_parse.
 * @param out Receives the entry.
 * @return 1 per entry, then 0 at the end.
 */
int psnr_rank_next(psnr_cursor* c, psnr_rank* out);

/** @} */

/** @addtogroup psnr_connect
 *  @{ */

/**
 * @brief Capability report.
 *
 * Send PSNR_GET_CAPS (no body, after HELLO) with psnr_call; the reply is PSNR_CAPS, or a
 * PSNR_ERROR with PSNR_E_BAD_REQUEST from a server that predates it (treat that as "nothing reported").
 * A clear bit means the server does not offer the feature to this player; ignore bits you do not know. The
 * probe and relay bits are per title: a server can turn them off for a title (-no-peer-titles).
 */
enum {
    PSNR_CAP_ROOMS = 1u << 0,      /**< create, search, join and leave rooms */
    PSNR_CAP_SCORES = 1u << 1,     /**< record and read scores */
    PSNR_CAP_ROOM_CODES = 1u << 2, /**< invite-only rooms and join by code */
    PSNR_CAP_PEER_KEYS = 1u << 3,  /**< PEER_AUTH and PEER_KEY pushes */
    PSNR_CAP_PROBE = 1u << 4,      /**< the server answers a UDP PROBE from this player's P2P socket */
    PSNR_CAP_RELAY = 1u << 5,      /**< relay streams and datagrams for this player */
    PSNR_CAP_DISCOVER = 1u << 6,   /**< the server answers LAN discovery */
    PSNR_CAP_PASS_RELAY = 1u << 7  /**< the relay needs a sign-in pass (-require-pass relay); a hint, see psnr_auth */
};

/** @brief A CAPS reply, decoded by psnr_caps_parse. */
typedef struct {
    uint32_t bits;            /**< the PSNR_CAP_* bits */
    const uint8_t* version;   /**< the server's version text, not NUL-terminated; points into the message */
    uint16_t version_len;     /**< length of version in bytes */
} psnr_caps;

/**
 * @brief Decodes a CAPS reply.
 *
 * CAPS; 1 ok, 0 bad (short, or bytes left over)
 *
 * @param m   The reply.
 * @param out Receives the decoded reply; version points into m's data.
 * @return 1 ok, 0 bad (short, bytes left over, or m is not a CAPS reply).
 */
int psnr_caps_parse(const psnr_msg* m, psnr_caps* out);

/**
 * @def PSNR_PASS_MAX
 * @brief The longest sign-in pass psnr_auth accepts, in bytes (2048).
 *
 * Sign-in passes (docs/api.md, "Sign-in passes"). Optional: a server asks for one only if its host turned that on.
 * The client never fetches, stores, parses or checks a pass. The title or host program gives it the opaque
 * bytes it holds, the library sends them in AUTH and reports what the server answered. Nothing the library
 * prints or returns contains the pass, and the copy it builds to send is wiped before psnr_auth returns. Keep
 * your own copy out of logs too.
 */
#define PSNR_PASS_MAX 2048

/**
 * @brief Return values of psnr_auth.
 *
 * PSNR_AUTH_OK (0) when the server accepted the pass. A positive value from psnr_auth is the ERROR code the
 * server answered. A negative value is a local failure (PSNR_AUTH_E_*).
 */
enum {
    PSNR_AUTH_OK = 0,          /**< the server accepted the pass */
    PSNR_AUTH_E_ARG = -1,      /**< c or pass NULL, pass empty or over PSNR_PASS_MAX, or timeout_ms <= 0; nothing was sent */
    PSNR_AUTH_E_IO = -2,       /**< the connection is dead or the write failed (psnr_dead_reason says why) */
    PSNR_AUTH_E_TIMEOUT = -3,  /**< no reply within timeout_ms; the pass may still have been accepted, so call psnr_auth again */
    PSNR_AUTH_E_REPLY = -4,    /**< a reply that is neither OK nor a well-formed ERROR (an ERROR code of 0 or above 0x7FFFFFFF counts as malformed) */
    PSNR_AUTH_E_NOMEM = -5     /**< an allocation failed */
};

/**
 * @brief Sends a sign-in pass.
 *
 * psnr_auth sends AUTH (version 1) after HELLO and waits up to timeout_ms for the reply. pass is 1 to
 * PSNR_PASS_MAX bytes; an empty, NULL or longer one, or a timeout_ms of 0 or less, is refused before anything is sent
 * (PSNR_AUTH_E_ARG, and the connection is unchanged). A second call on the same connection is a refresh (same account, new expiry): call it
 * before the current pass runs out, since the server closes the connection soon after.
 *
 * Returns PSNR_AUTH_OK (0) when the server accepted the pass. A positive value is the ERROR code the server
 * answered: PSNR_E_PASS (the pass is refused, or a pass is needed), PSNR_E_NAME_TAKEN (under -require-pass
 * connect, another player has the name), or PSNR_E_BAD_REQUEST, which is what a server with no pass settings, or one
 * from before AUTH, answers. ERROR 1 and capability bit PSNR_CAP_PASS_RELAY are hints, not proof: both can be forged
 * on a plain TCP connection, so only the player's own settings decide whether to send a pass and to which server.
 * A negative value is a local failure (PSNR_AUTH_E_*).
 *
 * Under -require-pass connect the OK reply carries the UDP token: when it does, the client uses it from then on in
 * psnr_probe_packet and psnr_relay_wrap (a refresh returns the same one). Without those four bytes (the relay-only
 * mode) the HELLO token stays. After AUTH is refused, nothing changed on the server. Three refused AUTH requests in
 * a row make the server close the connection.
 *
 * @param c          The connection, after HELLO.
 * @param pass       The opaque pass bytes, 1 to PSNR_PASS_MAX; the library never reads or keeps them beyond the send.
 * @param len        Length of pass in bytes.
 * @param timeout_ms How long to wait for the reply, in milliseconds; 0 or less is refused.
 * @return PSNR_AUTH_OK (0), a positive ERROR code from the server, or a negative PSNR_AUTH_E_* value.
 * @note Keep your own copy of the pass out of logs.
 */
int psnr_auth(psnr_client* c, const void* pass, size_t len, int timeout_ms);

/**
 * @brief Tells whether the server wants a pass to connect.
 *
 * HELLO_ACK flag bit 3: the server wants a pass to connect (-require-pass connect). A hint only: a forged or older
 * server can say anything. 1 if set, 0 if clear.
 *
 * @param c The connection.
 * @return 1 if the flag is set, 0 if clear.
 */
int psnr_pass_hint(const psnr_client* c);

/**
 * @brief Big-endian field helpers for building and reading payloads.
 *
 * Writes a 16-bit value at p.
 *
 * @param p Where to write.
 * @param v The value.
 * @return p advanced past the bytes written.
 */
static inline uint8_t* psnr_put16(uint8_t* p, uint16_t v) { p[0] = (uint8_t)(v >> 8); p[1] = (uint8_t)v; return p + 2; }
/**
 * @brief Writes a 32-bit big-endian value at p (see psnr_put16).
 * @param p Where to write.
 * @param v The value.
 * @return p advanced past the bytes written.
 */
static inline uint8_t* psnr_put32(uint8_t* p, uint32_t v) { psnr_put16(p, (uint16_t)(v >> 16)); return psnr_put16(p + 2, (uint16_t)v); }
/**
 * @brief Writes a 64-bit big-endian value at p (see psnr_put16).
 * @param p Where to write.
 * @param v The value.
 * @return p advanced past the bytes written.
 */
static inline uint8_t* psnr_put64(uint8_t* p, uint64_t v) { psnr_put32(p, (uint32_t)(v >> 32)); return psnr_put32(p + 4, (uint32_t)v); }
/**
 * @brief Reads a 16-bit big-endian value at p (see psnr_put16).
 * @param p Where to read.
 * @return The value.
 */
static inline uint16_t psnr_get16(const uint8_t* p) { return (uint16_t)(p[0] << 8 | p[1]); }
/**
 * @brief Reads a 32-bit big-endian value at p (see psnr_put16).
 * @param p Where to read.
 * @return The value.
 */
static inline uint32_t psnr_get32(const uint8_t* p) { return (uint32_t)psnr_get16(p) << 16 | psnr_get16(p + 2); }
/**
 * @brief Reads a 64-bit big-endian value at p (see psnr_put16).
 * @param p Where to read.
 * @return The value.
 */
static inline uint64_t psnr_get64(const uint8_t* p) { return (uint64_t)psnr_get32(p) << 32 | psnr_get32(p + 4); }

/** @} */

#ifdef __cplusplus
}
#endif

#endif /* PSNR_H */
