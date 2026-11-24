/*
 * test_call - psnr_call's reply handling against the synthetic responders in
 * server/client_call_test.go, which answer one GET_CAPS after HELLO:
 *
 *   test_call <port> error      an ERROR reply is handed back as one, with its code
 *   test_call <port> foreign    a reply to another request id, then a push, then ours: ours comes back, the rest waits
 *   test_call <port> short      frames too short to carry a request id are dropped, not passed off as pushes
 *   test_call <port> truncated  a reply whose body never completes is a timeout, then the close is reported
 *   test_call <port> max        the largest frame the wire allows arrives in pieces and comes back whole
 */
#include "psnr.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#ifdef _WIN32
#  include <windows.h>
#  define sleep_ms(n) Sleep(n)
#else
#  include <unistd.h>
#  define sleep_ms(n) usleep((n) * 1000)
#endif

static uint64_t mono_ms(void)
{
#ifdef _WIN32
    return GetTickCount64();
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000u + (uint64_t)ts.tv_nsec / 1000000u;
#endif
}

/* The next queued message within ~2 s, or 0 for nothing, -1 for dead. */
static int poll_next(psnr_client* c, psnr_msg* m, int want)
{
    int r = 0;
    for (int i = 0; i < 400; i++) {
        r = psnr_poll(c, m);
        if (r == want || r < 0) break;
        sleep_ms(5);
    }
    return r;
}

/* Nothing else was queued: ~100 ms of polling hands out no message. The responder closes once it has written
 * everything, and queued messages drain before psnr_poll reports that, so a close here is fine; a message is not. */
static void nothing_more(psnr_client* c)
{
    psnr_msg m;
    for (int i = 0; i < 20; i++) {
        int r = psnr_poll(c, &m);
        assert(r != 1);
        if (r < 0) return;
        sleep_ms(5);
    }
}

int main(int argc, char** argv)
{
    const char* mode;
    psnr_client* c;
    psnr_msg m, other;
    uint64_t t0;
    int r;
    assert(argc == 3);
    mode = argv[2];
    c = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "CALL-TEST", "call-test", 0, NULL, NULL, 2000);
    assert(c);

    t0 = mono_ms();
    r = psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, strcmp(mode, "truncated") == 0 ? 300 : 2000);

    if (strcmp(mode, "error") == 0) {
        /* reply type: an ERROR is a reply like any other, the code is in the first four bytes */
        assert(r == 1 && m.type == PSNR_ERROR && m.len == 4 && psnr_get32(m.data) == PSNR_E_PASS);
        psnr_msg_free(&m);
        nothing_more(c);                                    /* nothing else was queued */
    } else if (strcmp(mode, "foreign") == 0) {
        /* request id match: the reply with our id comes back; the other reply and the push wait for psnr_poll, in order */
        assert(r == 1 && m.type == PSNR_CAPS && m.len == 6 && psnr_get32(m.data) == 1);
        assert(poll_next(c, &other, 1) == 1 && other.type == PSNR_OK && other.req == m.req + 1 && other.len == 0);
        psnr_msg_free(&other);
        assert(poll_next(c, &other, 1) == 1 && other.type == PSNR_MEMBER_LEFT && other.req == 0);
        assert(other.len == 10 && psnr_get64(other.data) == 7 && psnr_get16(other.data + 8) == 2);
        psnr_msg_free(&other);
        psnr_msg_free(&m);
        nothing_more(c);
    } else if (strcmp(mode, "short") == 0) {
        /* length bounds: reply frames of 0 and 2 bytes have no request id and are dropped; ours follows them */
        assert(r == 1 && m.type == PSNR_CAPS && m.len == 6);
        psnr_msg_free(&m);
        nothing_more(c);                                    /* the damaged frames did not become pushes */
    } else if (strcmp(mode, "truncated") == 0) {
        /* a header promising more than arrives: the call times out within its budget, nothing is handed back */
        uint64_t took = mono_ms() - t0;
        assert(r == 0);
        assert(took >= 250 && took < 1500);
        assert(psnr_dead_reason(c) == PSNR_DEAD_NONE);
        r = poll_next(c, &m, -1);                           /* the responder closes: the partial frame is never delivered */
        assert(r == -1 && psnr_dead_reason(c) == PSNR_DEAD_PEER_CLOSED);
    } else if (strcmp(mode, "max") == 0) {
        /* a 65535-byte frame, larger than the receive buffer starts out, written in pieces: whole, then the next frame */
        uint32_t i;
        assert(r == 1 && m.type == PSNR_CAPS && m.len == 0xFFFF - 4);
        for (i = 0; i < m.len; i++) assert(m.data[i] == (uint8_t)(i * 7));
        psnr_msg_free(&m);
        assert(poll_next(c, &other, 1) == 1 && other.type == PSNR_MEMBER_LEFT && other.req == 0 && other.len == 10);
        psnr_msg_free(&other);
        nothing_more(c);
    } else {
        fprintf(stderr, "unknown mode\n");
        return 2;
    }
    psnr_close(c);
    printf("test_call: %s passed\n", mode);
    return 0;
}
