#ifndef PSNR_APP_H
#define PSNR_APP_H

/* Local trophy SDK. The app explicitly issues a private, title-scoped grant.
 * No account key, relay authentication or background thread is used here.
 * Unlock/status use one absolute timeout_ms network deadline. Grant loading
 * reads a bounded local regular file. Call from outside shared runtime locks.
 */
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

enum {
    PSNR_APP_OK = 0,
    PSNR_APP_UNAUTHORIZED = 1,
    PSNR_APP_UNKNOWN_TROPHY = 2,
    PSNR_APP_BUSY = 3,
    PSNR_APP_MALFORMED = 4,
    PSNR_APP_STORAGE_ERROR = 5,
    PSNR_APP_RATE_LIMITED = 6,
    PSNR_APP_UNAVAILABLE = 100,
    PSNR_APP_INVALID_ARGUMENT = 101,
    PSNR_APP_INVALID_RESPONSE = 102,
    PSNR_APP_TIMEOUT = 103
};

typedef struct psnr_app_client {
    uint16_t port;
    uint8_t capability[32];
    int timeout_ms; /* 1..60000; grant loading sets 2000 */
} psnr_app_client;

typedef struct psnr_app_trophy {
    int unlocked;
    uint64_t unlocked_at; /* app receipt time, Unix milliseconds; zero if locked */
} psnr_app_trophy;

/* Reads only an explicitly supplied grant descriptor, at most 512 bytes.
 * The descriptor is a credential: keep it private and do not log its contents.
 */
int psnr_app_load_grant(psnr_app_client* client, const char* path);
int psnr_app_unlock(const psnr_app_client* client, uint32_t trophy_id, psnr_app_trophy* result);
int psnr_app_status(const psnr_app_client* client, uint32_t trophy_id, psnr_app_trophy* result);

#ifdef __cplusplus
}
#endif
#endif
