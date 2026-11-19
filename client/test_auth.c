/*
 * test_auth - psnr_auth against a live server with sign-in passes on (server/client_auth_test.go starts it,
 * makes the passes with throwaway keys and writes each to a file):
 *
 *   test_auth <port> connect <good> <refresh> <other-account> <bad-signature>
 *   test_auth <port> taken   <good>
 *   test_auth <port> relay   <good>
 *   test_auth <port> old
 *
 * The pass files are read as opaque bytes and are never printed.
 */
#include "psnr.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define COMM "NPWR00001_00"

static uint8_t* slurp(const char* path, size_t* len)
{
    FILE* f = fopen(path, "rb");
    uint8_t* b = (uint8_t*)malloc(PSNR_PASS_MAX + 1);
    assert(f && b);
    *len = fread(b, 1, PSNR_PASS_MAX + 1, f);
    fclose(f);
    assert(*len > 0 && *len <= PSNR_PASS_MAX);
    return b;
}

static uint32_t udp_token(const psnr_client* c)
{
    uint8_t pkt[PSNR_UDP_PROBE_LEN];
    psnr_probe_packet(c, pkt);
    return psnr_get32(pkt + 9);
}

/* The connection still answers a request: nothing was sent that it would choke on. */
static void still_usable(psnr_client* c)
{
    psnr_msg m;
    psnr_caps caps;
    assert(psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, 2000) == 1);
    if (m.type == PSNR_CAPS) assert(psnr_caps_parse(&m, &caps));
    else assert(m.type == PSNR_ERROR);
    psnr_msg_free(&m);
}

static uint32_t caps_bits(psnr_client* c)
{
    psnr_msg m;
    psnr_caps caps;
    assert(psnr_call(c, PSNR_GET_CAPS, NULL, 0, &m, 2000) == 1);
    assert(m.type == PSNR_CAPS && psnr_caps_parse(&m, &caps));
    psnr_msg_free(&m);
    return caps.bits;
}

static void arguments(psnr_client* c, const uint8_t* good, size_t n)
{
    uint8_t big[PSNR_PASS_MAX + 1];
    memset(big, 'x', sizeof(big));
    assert(psnr_auth(c, NULL, 5, 1000) == PSNR_AUTH_E_ARG);
    assert(psnr_auth(c, good, 0, 1000) == PSNR_AUTH_E_ARG);
    assert(psnr_auth(c, big, PSNR_PASS_MAX + 1, 1000) == PSNR_AUTH_E_ARG);
    assert(psnr_auth(NULL, good, n, 1000) == PSNR_AUTH_E_ARG);
    assert(psnr_auth(c, good, n, 0) == PSNR_AUTH_E_ARG);    /* a timeout of 0 or less is refused, not a quick timeout */
    assert(psnr_auth(c, good, n, -1) == PSNR_AUTH_E_ARG);
    still_usable(c);   /* nothing was sent, so the server counted no refusal */
}

int main(int argc, char** argv)
{
    uint16_t port;
    const char* mode;
    psnr_client* c;
    uint32_t hello_token;
    size_t n = 0;
    uint8_t* good;
    assert(argc >= 3);
    port = (uint16_t)atoi(argv[1]);
    mode = argv[2];

    if (strcmp(mode, "connect") == 0) {
        uint8_t *refresh, *other, *bad;
        size_t nr, no, nb;
        uint8_t big[PSNR_PASS_MAX];
        assert(argc == 7);
        good = slurp(argv[3], &n);
        refresh = slurp(argv[4], &nr);
        other = slurp(argv[5], &no);
        bad = slurp(argv[6], &nb);
        c = psnr_connect("127.0.0.1", port, COMM, "alice", 0, NULL, NULL);
        assert(c);
        assert(psnr_pass_hint(c) == 1);
        assert(udp_token(c) == 0);                         /* HELLO is parked: no token yet */
        assert((caps_bits(c) & ~PSNR_CAP_PASS_RELAY) == 0);   /* before AUTH only bit 7 may show */
        arguments(c, good, n);
        assert(psnr_auth(c, bad, nb, 2000) == PSNR_E_PASS);
        memset(big, 'x', sizeof(big));                     /* exactly 2048 bytes is sent, and refused */
        assert(psnr_auth(c, big, sizeof(big), 2000) == PSNR_E_PASS);
        assert(psnr_auth(c, good, n, 2000) == PSNR_AUTH_OK);   /* a good one resets the refusal count */
        hello_token = udp_token(c);
        assert(hello_token != 0);                          /* the AUTH reply's token replaced it */
        assert(psnr_auth(c, refresh, nr, 2000) == PSNR_AUTH_OK);
        assert(udp_token(c) == hello_token);               /* a refresh returns the same token */
        assert(psnr_auth(c, other, no, 2000) == PSNR_E_PASS);   /* another account may not refresh it */
        assert(udp_token(c) == hello_token);
        {   /* the connection is a player now: a room request is answered, not refused with ERROR 10 */
            uint8_t search[4] = {0, 0, 0, 10};
            psnr_msg m;
            assert(psnr_call(c, PSNR_SEARCH_ROOMS, search, 4, &m, 2000) == 1 && m.type == PSNR_ROOM_LIST);
            psnr_msg_free(&m);
        }
        psnr_close(c);
        free(refresh); free(other); free(bad);
    } else if (strcmp(mode, "taken") == 0) {
        assert(argc == 4);
        good = slurp(argv[3], &n);
        c = psnr_connect("127.0.0.1", port, COMM, "taken", 0, NULL, NULL);
        assert(c);
        assert(psnr_auth(c, good, n, 2000) == PSNR_E_NAME_TAKEN);
        psnr_close(c);
    } else if (strcmp(mode, "relay") == 0) {
        assert(argc == 4);
        good = slurp(argv[3], &n);
        c = psnr_connect("127.0.0.1", port, COMM, "alice", 0, NULL, NULL);
        assert(c);
        assert(psnr_pass_hint(c) == 0);
        hello_token = udp_token(c);
        assert(hello_token != 0);
        assert((caps_bits(c) & PSNR_CAP_PASS_RELAY) && !(caps_bits(c) & PSNR_CAP_RELAY));
        arguments(c, good, n);
        assert(psnr_auth(c, good, n, 2000) == PSNR_AUTH_OK);
        assert(udp_token(c) == hello_token);               /* no token in the reply: the HELLO one stays */
        assert(caps_bits(c) & PSNR_CAP_RELAY);             /* the relay opens once a pass is accepted */
        psnr_close(c);
    } else if (strcmp(mode, "old") == 0) {
        uint8_t pass[] = "v1.AAAA.BBBB";
        assert(argc == 3);
        c = psnr_connect("127.0.0.1", port, COMM, "alice", 0, NULL, NULL);
        assert(c);
        assert(psnr_pass_hint(c) == 0);
        hello_token = udp_token(c);
        assert(psnr_auth(c, pass, sizeof(pass) - 1, 2000) == PSNR_E_BAD_REQUEST);
        assert(udp_token(c) == hello_token);
        still_usable(c);
        psnr_close(c);
        printf("test_auth: %s passed\n", mode);
        return 0;
    } else {
        fprintf(stderr, "unknown mode\n");
        return 2;
    }
    free(good);
    printf("test_auth: %s passed\n", mode);
    return 0;
}
