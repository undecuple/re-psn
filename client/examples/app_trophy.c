/* Synthetic integration example; supply an app-issued private grant file.
 * cc -std=c99 -Iclient client/psnr_app.c client/examples/app_trophy.c -o app_trophy
 * On Windows link ws2_32. Invoke: app_trophy GRANT_FILE unlock|status TROPHY_ID
 */
#include "psnr_app.h"
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char** argv)
{
    psnr_app_client app;
    psnr_app_trophy trophy;
    unsigned long long id;
    char* end;
    int rc;
    if (argc != 4 || (strcmp(argv[2], "unlock") && strcmp(argv[2], "status"))) return 2;
    errno = 0;
    if (*argv[3] < '0' || *argv[3] > '9') return 2;
    id = strtoull(argv[3], &end, 10);
    if (errno || *end || id > UINT32_MAX) return 2;
    rc = psnr_app_load_grant(&app, argv[1]);
    if (rc) { fprintf(stderr, "grant unavailable (%d)\n", rc); return 1; }
    rc = strcmp(argv[2], "unlock") == 0 ? psnr_app_unlock(&app, (uint32_t)id, &trophy)
                                         : psnr_app_status(&app, (uint32_t)id, &trophy);
    if (rc) { fprintf(stderr, "app request failed (%d)\n", rc); return 1; }
    printf("%u %d %llu\n", (unsigned int)id, trophy.unlocked, (unsigned long long)trophy.unlocked_at);
    return 0;
}
