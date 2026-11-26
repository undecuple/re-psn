/* SPDX-License-Identifier: MIT (see LICENSE) */
#ifndef PSNR_APP_H
#define PSNR_APP_H

/**
 * @file psnr_app.h
 * @brief Local trophy SDK.
 *
 * The app explicitly issues a private, title-scoped grant.
 * No account key, relay authentication or background thread is used here.
 * Unlock/status use one absolute timeout_ms network deadline. Grant loading
 * reads a bounded local regular file. Call from outside shared runtime locks.
 */

/**
 * @defgroup psnr_app App framework
 * @brief Local trophy SDK: load a grant, then unlock a trophy or read its status.
 */

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @addtogroup psnr_app
 * @{
 */

/** @brief Result codes returned by the psnr_app_* functions (0 is success). */
enum {
    PSNR_APP_OK = 0,                 /**< success */
    PSNR_APP_UNAUTHORIZED = 1,       /**< status 1 from the app: unauthorized */
    PSNR_APP_UNKNOWN_TROPHY = 2,     /**< status 2 from the app: unknown trophy */
    PSNR_APP_BUSY = 3,               /**< status 3 from the app: busy */
    PSNR_APP_MALFORMED = 4,          /**< status 4 from the app: malformed */
    PSNR_APP_STORAGE_ERROR = 5,      /**< status 5 from the app: storage error */
    PSNR_APP_RATE_LIMITED = 6,       /**< status 6 from the app: rate limited */
    PSNR_APP_UNAVAILABLE = 100,      /**< local failure: unavailable (not reached, or a file or socket error) */
    PSNR_APP_INVALID_ARGUMENT = 101, /**< local failure: invalid argument (also an oversize, non-regular or malformed grant file) */
    PSNR_APP_INVALID_RESPONSE = 102, /**< local failure: invalid response (also a connection closed early) */
    PSNR_APP_TIMEOUT = 103           /**< local failure: the timeout_ms deadline passed */
};

/** @brief A loaded grant: where to reach the app, the capability bytes, and the network deadline. */
typedef struct psnr_app_client {
    uint16_t port;           /**< the port the app listens on, on this machine (loopback, 127.0.0.1) */
    uint8_t capability[32];  /**< the capability bytes from the grant */
    int timeout_ms; /**< 1..60000; grant loading sets 2000 */
} psnr_app_client;

/** @brief The state of one trophy. */
typedef struct psnr_app_trophy {
    int unlocked;            /**< nonzero if the trophy is unlocked */
    uint64_t unlocked_at; /**< app receipt time, Unix milliseconds; zero if locked */
} psnr_app_trophy;

/**
 * @brief Loads a grant descriptor.
 *
 * Reads only an explicitly supplied grant descriptor, at most 512 bytes.
 * The descriptor is a credential: keep it private and do not log its contents.
 *
 * @param client Filled in from the grant file; zeroed on any failure after the argument check.
 * @param path   Path of the grant descriptor file.
 * @return PSNR_APP_OK, or a PSNR_APP_* error code.
 * @note The descriptor is a credential; do not log its contents.
 */
int psnr_app_load_grant(psnr_app_client* client, const char* path);

/**
 * @brief Unlocks a trophy.
 *
 * Uses one absolute timeout_ms network deadline (the client's timeout_ms).
 *
 * @param client    A client filled by psnr_app_load_grant.
 * @param trophy_id The trophy to unlock.
 * @param result    Filled with the trophy's state.
 * @return PSNR_APP_OK, or a PSNR_APP_* error code.
 */
int psnr_app_unlock(const psnr_app_client* client, uint32_t trophy_id, psnr_app_trophy* result);

/**
 * @brief Reads a trophy's status.
 *
 * Uses one absolute timeout_ms network deadline (the client's timeout_ms).
 *
 * @param client    A client filled by psnr_app_load_grant.
 * @param trophy_id The trophy to read.
 * @param result    Filled with the trophy's state.
 * @return PSNR_APP_OK, or a PSNR_APP_* error code.
 */
int psnr_app_status(const psnr_app_client* client, uint32_t trophy_id, psnr_app_trophy* result);

/** @} */

#ifdef __cplusplus
}
#endif
#endif
