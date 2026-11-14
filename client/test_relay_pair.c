/* Two native client handles exchanging raw bytes through the real Go relay.
 * POSIX only: the connector uses a thread while the acceptor pumps its offers. */
#include "psnr.h"

#include <assert.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

#define PAYLOAD_LEN (65536 + 17)

typedef struct {
    psnr_client* client;
    uint32_t target;
    int64_t socket;
} connector;

static uint64_t milliseconds(void)
{
    struct timespec ts;
    assert(clock_gettime(CLOCK_MONOTONIC, &ts) == 0);
    return (uint64_t)ts.tv_sec * 1000u + (uint64_t)ts.tv_nsec / 1000000u;
}

static psnr_msg push_of_type(psnr_client* c, uint8_t type)
{
    uint64_t deadline = milliseconds() + 3000;
    psnr_msg msg;
    while (milliseconds() < deadline) {
        int result = psnr_poll(c, &msg);
        assert(result >= 0);
        if (result == 1) {
            if (msg.type == type) return msg;
            psnr_msg_free(&msg);
        } else {
            usleep(1000);
        }
    }
    fprintf(stderr, "missing push 0x%02x\n", type);
    exit(1);
}

static uint64_t room_call(psnr_client* c, uint8_t type, const uint8_t* body, size_t len)
{
    psnr_msg msg;
    psnr_room_joined room;
    uint64_t id;
    assert(psnr_call(c, type, body, (uint32_t)len, &msg, 3000) == 1);
    assert(psnr_room_joined_parse(&msg, &room));
    id = room.room_id;
    psnr_msg_free(&msg);
    return id;
}

static void* connect_stream(void* arg)
{
    connector* job = (connector*)arg;
    job->socket = psnr_stream_connect(job->client, job->target, 7, 3000);
    return NULL;
}

static void transfer(int sender, int receiver, unsigned seed)
{
    uint8_t sent[PAYLOAD_LEN], received[PAYLOAD_LEN];
    size_t offset = 0;
    for (size_t i = 0; i < sizeof(sent); i++) sent[i] = (uint8_t)(i * 31u + seed);
    memcpy(sent, "PSNR\0\xff", 6); /* Raw data includes control-looking bytes and spans relay buffers. */
    while (offset < sizeof(sent)) {
        ssize_t n = send(sender, sent + offset, sizeof(sent) - offset, 0);
        if (n < 0 && errno == EINTR) continue;
        assert(n > 0);
        offset += (size_t)n;
    }
    offset = 0;
    while (offset < sizeof(received)) {
        ssize_t n = recv(receiver, received + offset, sizeof(received) - offset, 0);
        if (n < 0 && errno == EINTR) continue;
        assert(n > 0);
        offset += (size_t)n;
    }
    assert(memcmp(sent, received, sizeof(sent)) == 0);
}

int main(int argc, char** argv)
{
    uint32_t alice_id, bob_id;
    psnr_client *alice, *bob;
    uint8_t create[11] = {2}, join[10] = {0};
    uint64_t room;
    psnr_msg msg;
    pthread_t thread;
    connector job;
    int a, b, closing, remaining;
    struct timeval timeout = {2, 0};
    char byte;
    assert(argc == 3);
    alice = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "RELAY-PAIR", "pair-alice", 0,
                                 &alice_id, NULL, 3000);
    bob = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "RELAY-PAIR", "pair-bob", 0,
                               &bob_id, NULL, 3000);
    assert(alice && bob && alice_id != bob_id);
    assert(psnr_relay_available(alice) && psnr_relay_available(bob));
    room = room_call(alice, PSNR_CREATE_ROOM, create, sizeof(create));
    psnr_put64(join, room);
    assert(room_call(bob, PSNR_JOIN_ROOM, join, sizeof(join)) == room);
    msg = push_of_type(bob, PSNR_ROUTE);
    assert(msg.len == 13 && psnr_get64(msg.data) == room && psnr_get32(msg.data + 8) == alice_id);
    assert(msg.data[12] & PSNR_ROUTE_STREAM_RELAY);
    psnr_msg_free(&msg);

    job.client = alice; job.target = bob_id; job.socket = -1;
    assert(pthread_create(&thread, NULL, connect_stream, &job) == 0);
    msg = push_of_type(bob, PSNR_STREAM_OFFER);
    assert(msg.len == 10 && psnr_get32(msg.data + 4) == alice_id && psnr_get16(msg.data + 8) == 7);
    b = (int)psnr_stream_accept(bob, psnr_get32(msg.data), 3000);
    psnr_msg_free(&msg);
    assert(pthread_join(thread, NULL) == 0);
    a = (int)job.socket;
    assert(a >= 0 && b >= 0);
    assert(!(fcntl(a, F_GETFL, 0) & O_NONBLOCK) && !(fcntl(b, F_GETFL, 0) & O_NONBLOCK));
    assert(setsockopt(a, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) == 0);
    assert(setsockopt(a, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout)) == 0);
    assert(setsockopt(b, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof(timeout)) == 0);
    assert(setsockopt(b, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof(timeout)) == 0);
    transfer(a, b, 3);
    transfer(b, a, 91);

    closing = strcmp(argv[2], "connector-close") == 0 ? a : b;
    remaining = closing == a ? b : a;
    assert(close(closing) == 0);
    assert(recv(remaining, &byte, 1, 0) == 0); /* Either leg's EOF closes the splice's other leg. */
    assert(close(remaining) == 0);
    psnr_close(alice);
    psnr_close(bob);
    printf("native relay pair passed bytes=%u\n", 2u * PAYLOAD_LEN);
    return 0;
}
