/* psnr_relay_wrap size bounds, with no server: a payload of 0 bytes, the
 * largest one, one more and SIZE_MAX. The payload ends at a guard page and so
 * does out at its cap, so a read past the payload or a write past cap faults
 * instead of passing unseen. Includes psnr.c to build a client in place. */
#include <assert.h>
#include <stdint.h>
#include <sys/mman.h>
#include <unistd.h>
#ifndef PSNR_SOURCE
#define PSNR_SOURCE "psnr.c"
#endif
#include PSNR_SOURCE

#define MAX_OUT (17 + PSNR_RELAY_MAX_PAYLOAD) /* 65507: one IPv4 UDP datagram */

/* n writable bytes that end exactly where a PROT_NONE page begins. */
static uint8_t* guarded(size_t n)
{
    size_t page = (size_t)sysconf(_SC_PAGESIZE);
    size_t pages = (n + page - 1) / page;
    uint8_t* base = mmap(NULL, (pages + 1) * page, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    assert(base != MAP_FAILED);
    assert(mprotect(base + pages * page, page, PROT_NONE) == 0);
    return base + pages * page - n;
}

static void check_header(const uint8_t* out, const psnr_client* c, uint32_t to_user)
{
    assert(memcmp(out, "PSNR\x03", 5) == 0);
    assert(psnr_get32(out + 5) == c->user_id);
    assert(psnr_get32(out + 9) == c->token);
    assert(psnr_get32(out + 13) == to_user);
}

static int untouched(const uint8_t* out, size_t n)
{
    for (size_t i = 0; i < n; ++i)
        if (out[i] != 0x5A) return 0;
    return 1;
}

int main(void)
{
    psnr_client c;
    memset(&c, 0, sizeof c);
    c.user_id = 0x01020304u;
    c.token = 0xA5B6C7D8u;

    /* 0 bytes: the header alone fits in 17, and nothing is written into 16. */
    uint8_t one = 0xEE;
    uint8_t* out = guarded(17);
    assert(psnr_relay_wrap(&c, 7, &one, 0, out, 17) == 17);
    check_header(out, &c, 7);
    memset(out, 0x5A, 17);
    assert(psnr_relay_wrap(&c, 7, &one, 0, out, 16) == 0);
    assert(untouched(out, 17));

    /* The largest payload fills one IPv4 UDP datagram exactly. */
    uint8_t* payload = guarded(PSNR_RELAY_MAX_PAYLOAD);
    for (size_t i = 0; i < PSNR_RELAY_MAX_PAYLOAD; ++i) payload[i] = (uint8_t)(i * 7 + 1);
    out = guarded(MAX_OUT);
    assert(psnr_relay_wrap(&c, 9, payload, PSNR_RELAY_MAX_PAYLOAD, out, MAX_OUT) == MAX_OUT);
    check_header(out, &c, 9);
    assert(memcmp(out + 17, payload, PSNR_RELAY_MAX_PAYLOAD) == 0);
    memset(out, 0x5A, MAX_OUT);
    assert(psnr_relay_wrap(&c, 9, payload, PSNR_RELAY_MAX_PAYLOAD, out, MAX_OUT - 1) == 0);
    assert(untouched(out, MAX_OUT));

    /* One more than the largest, SIZE_MAX, and the lengths whose 17 + len
     * wraps to a small number: refused before anything is read or written,
     * however much room cap claims. The payload is still the guarded one of
     * PSNR_RELAY_MAX_PAYLOAD bytes, so a read of len bytes would fault. */
    const size_t bad[] = { PSNR_RELAY_MAX_PAYLOAD + 1, SIZE_MAX, SIZE_MAX - 16, SIZE_MAX - 17, SIZE_MAX / 2 };
    const size_t caps[] = { MAX_OUT, SIZE_MAX, 16, 0 };
    for (size_t i = 0; i < sizeof bad / sizeof bad[0]; ++i)
        for (size_t j = 0; j < sizeof caps / sizeof caps[0]; ++j) {
            assert(psnr_relay_wrap(&c, 9, payload, bad[i], out, caps[j]) == 0);
            assert(untouched(out, MAX_OUT));
        }

    printf("relay wrap bounds passed max=%d\n", PSNR_RELAY_MAX_PAYLOAD);
    return 0;
}
