#ifndef _WIN32
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif
#endif
#include "psnr_app.h"
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0600
#endif
#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
typedef SOCKET app_socket;
#define APP_INVALID INVALID_SOCKET
#define app_close closesocket
#else
#include <arpa/inet.h>
#include <fcntl.h>
#include <poll.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
typedef int app_socket;
#define APP_INVALID (-1)
#define app_close close
#endif

static int app_hex(char c)
{
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

int psnr_app_load_grant(psnr_app_client* client, const char* path)
{
    char buf[513], *p, *end;
    size_t n, i;
    unsigned long port;
    psnr_app_client parsed;
    const char prefix[] = "re-psn-app-grant-v1\nport=";
    if (!client || !path) return PSNR_APP_INVALID_ARGUMENT;
    memset(client, 0, sizeof(*client));
#ifdef _WIN32
    {
        HANDLE file;
        BY_HANDLE_FILE_INFORMATION info;
        DWORD count;
        WCHAR wide[32768];
        if ((path[0] == '\\' || path[0] == '/') && (path[1] == '\\' || path[1] == '/'))
            return PSNR_APP_INVALID_ARGUMENT;
        if (!MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide, 32768))
            return PSNR_APP_INVALID_ARGUMENT;
        file = CreateFileW(wide, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING,
                           FILE_FLAG_OPEN_REPARSE_POINT, NULL);
        if (file == INVALID_HANDLE_VALUE) return PSNR_APP_UNAVAILABLE;
        if (GetFileType(file) != FILE_TYPE_DISK || !GetFileInformationByHandle(file, &info) ||
            (info.dwFileAttributes & (FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT)) ||
            info.nFileSizeHigh || info.nFileSizeLow > 512) {
            CloseHandle(file); return PSNR_APP_INVALID_ARGUMENT;
        }
        if (!ReadFile(file, buf, sizeof(buf), &count, NULL)) { CloseHandle(file); return PSNR_APP_UNAVAILABLE; }
        n = count;
        CloseHandle(file);
    }
#else
    {
        int flags = O_RDONLY | O_NONBLOCK, fd;
        struct stat info;
#ifdef O_NOFOLLOW
        flags |= O_NOFOLLOW;
#endif
#ifdef O_CLOEXEC
        flags |= O_CLOEXEC;
#endif
        fd = open(path, flags);
        if (fd < 0) return PSNR_APP_UNAVAILABLE;
        if (fstat(fd, &info) || !S_ISREG(info.st_mode) || info.st_size < 0 || info.st_size > 512) {
            close(fd); return PSNR_APP_INVALID_ARGUMENT;
        }
        n = 0;
        while (n < sizeof(buf)) {
            ssize_t got = read(fd, buf+n, sizeof(buf)-n);
            if (got > 0) n += (size_t)got;
            else if (got == 0) break;
            else if (errno != EINTR) { close(fd); return PSNR_APP_UNAVAILABLE; }
        }
        close(fd);
    }
#endif
    if (n >= sizeof(buf) || n < sizeof(prefix) || memchr(buf, 0, n)) return PSNR_APP_INVALID_ARGUMENT;
    buf[n] = 0;
    if (strncmp(buf, prefix, sizeof(prefix)-1)) return PSNR_APP_INVALID_ARGUMENT;
    p = buf + sizeof(prefix)-1;
    if (*p < '0' || *p > '9') return PSNR_APP_INVALID_ARGUMENT;
    errno = 0;
    port = strtoul(p, &end, 10);
    if (errno || port == 0 || port > 65535 || strncmp(end, "\ntoken=", 7)) return PSNR_APP_INVALID_ARGUMENT;
    p = end + 7;
    if (strlen(p) != 65 || p[64] != '\n') return PSNR_APP_INVALID_ARGUMENT;
    memset(&parsed, 0, sizeof(parsed));
    parsed.port = (uint16_t)port;
    parsed.timeout_ms = 2000;
    for (i = 0; i < 32; i++) {
        int a = app_hex(p[2*i]), b = app_hex(p[2*i+1]);
        if (a < 0 || b < 0) return PSNR_APP_INVALID_ARGUMENT;
        parsed.capability[i] = (uint8_t)((a << 4) | b);
    }
    *client = parsed;
    return PSNR_APP_OK;
}

static uint64_t app_now(void)
{
#ifdef _WIN32
    return GetTickCount64();
#else
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) return 0;
    return (uint64_t)ts.tv_sec * 1000 + (uint64_t)ts.tv_nsec / 1000000;
#endif
}

static int app_retry(void)
{
#ifdef _WIN32
    int e = WSAGetLastError();
    return e == WSAEINTR || e == WSAEWOULDBLOCK || e == WSAEINPROGRESS;
#else
    return errno == EINTR || errno == EWOULDBLOCK || errno == EAGAIN || errno == EINPROGRESS;
#endif
}

static int app_wait(app_socket fd, int writing, uint64_t deadline)
{
    for (;;) {
        uint64_t now = app_now();
        int n, left;
        if (!now || now >= deadline) return PSNR_APP_TIMEOUT;
        left = (int)(deadline-now);
#ifdef _WIN32
        WSAPOLLFD p;
        p.fd = fd; p.events = writing ? POLLWRNORM : POLLRDNORM; p.revents = 0;
        n = WSAPoll(&p, 1, left);
#else
        struct pollfd p;
        p.fd = fd; p.events = writing ? POLLOUT : POLLIN; p.revents = 0;
        n = poll(&p, 1, left);
#endif
        if (n > 0) return PSNR_APP_OK;
        if (n == 0) return PSNR_APP_TIMEOUT;
        if (!app_retry()) return PSNR_APP_UNAVAILABLE;
    }
}

static int app_transfer(app_socket fd, uint8_t* data, size_t len, int writing, uint64_t deadline)
{
    size_t done = 0;
    while (done < len) {
        int n, rc = app_wait(fd, writing, deadline);
        if (rc) return rc;
#ifdef _WIN32
        n = writing ? send(fd, (const char*)data+done, (int)(len-done), 0)
                    : recv(fd, (char*)data+done, (int)(len-done), 0);
#else
        int flags = 0;
#ifdef MSG_NOSIGNAL
        if (writing) flags = MSG_NOSIGNAL;
#endif
        n = (int)(writing ? send(fd, data+done, len-done, flags) : recv(fd, data+done, len-done, 0));
#endif
        if (n > 0) done += (size_t)n;
        else if (n == 0) return PSNR_APP_INVALID_RESPONSE;
        else if (!app_retry()) return PSNR_APP_UNAVAILABLE;
    }
    return PSNR_APP_OK;
}

static int app_call(const psnr_app_client* client, uint8_t operation, uint32_t id, psnr_app_trophy* result)
{
    app_socket fd = APP_INVALID;
    struct sockaddr_in addr;
    uint8_t request[48] = {'R','P','T','1'}, response[12], value[9];
    uint64_t now, deadline, timestamp = 0;
    int rc = PSNR_APP_UNAVAILABLE, n;
#ifdef _WIN32
    WSADATA wsa;
    u_long nonblock = 1;
    int error_len;
#else
    int flags;
    socklen_t error_len;
#endif
    if (result) memset(result, 0, sizeof(*result));
    if (!client || !result || !client->port || client->timeout_ms < 1 || client->timeout_ms > 60000)
        return PSNR_APP_INVALID_ARGUMENT;
    now = app_now();
    if (!now) return PSNR_APP_UNAVAILABLE;
    deadline = now + (unsigned int)client->timeout_ms;
#ifdef _WIN32
    if (WSAStartup(MAKEWORD(2,2), &wsa)) return PSNR_APP_UNAVAILABLE;
#endif
    fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd == APP_INVALID) goto cleanup;
#ifdef _WIN32
    if (ioctlsocket(fd, FIONBIO, &nonblock)) goto cleanup;
#else
    flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) < 0) goto cleanup;
#ifdef SO_NOSIGPIPE
    { int yes = 1; if (setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof(yes))) goto cleanup; }
#endif
#endif
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_port = htons(client->port);
    addr.sin_addr.s_addr = htonl(0x7f000001UL);
    if (connect(fd, (struct sockaddr*)&addr, sizeof(addr)) != 0) {
        int error = 0;
        if (!app_retry()) goto cleanup;
        rc = app_wait(fd, 1, deadline);
        if (rc) goto cleanup;
        error_len = sizeof(error);
        if (getsockopt(fd, SOL_SOCKET, SO_ERROR, (void*)&error, &error_len) || error) {
            rc = PSNR_APP_UNAVAILABLE; goto cleanup;
        }
    }
    request[4] = operation;
    request[11] = 36;
    memcpy(request+12, client->capability, 32);
    for (n = 0; n < 4; n++) request[44+n] = (uint8_t)(id >> (24-8*n));
    rc = app_transfer(fd, request, sizeof(request), 1, deadline);
    if (rc) goto cleanup;
#ifdef _WIN32
    if (shutdown(fd, SD_SEND)) { rc = PSNR_APP_UNAVAILABLE; goto cleanup; }
#else
    if (shutdown(fd, SHUT_WR)) { rc = PSNR_APP_UNAVAILABLE; goto cleanup; }
#endif
    rc = app_transfer(fd, response, sizeof(response), 0, deadline);
    if (rc) goto cleanup;
    if (memcmp(response, "RPR1", 4) || response[5] || response[6] || response[7] ||
        response[8] || response[9] || response[10] || response[4] > PSNR_APP_RATE_LIMITED ||
        response[11] != (response[4] == 0 ? 9 : 0)) {
        rc = PSNR_APP_INVALID_RESPONSE; goto cleanup;
    }
    rc = response[4];
    if (rc) goto cleanup;
    rc = app_transfer(fd, value, sizeof(value), 0, deadline);
    if (rc) goto cleanup;
    for (n = 0; n < 8; n++) timestamp = (timestamp << 8) | value[n+1];
    if (value[0] > 1 || (operation == 1 && value[0] != 1) ||
        (value[0] == 0 && timestamp != 0) || (value[0] != 0 && timestamp == 0)) {
        rc = PSNR_APP_INVALID_RESPONSE; goto cleanup;
    }
    /* One response per connection. Require EOF before publishing a success. */
    for (;;) {
        uint8_t extra;
        rc = app_wait(fd, 0, deadline);
        if (rc) goto cleanup;
#ifdef _WIN32
        n = recv(fd, (char*)&extra, 1, 0);
#else
        n = (int)recv(fd, &extra, 1, 0);
#endif
        if (n == 0) break;
        if (n > 0) { rc = PSNR_APP_INVALID_RESPONSE; goto cleanup; }
        if (!app_retry()) { rc = PSNR_APP_UNAVAILABLE; goto cleanup; }
    }
    result->unlocked = value[0];
    result->unlocked_at = timestamp;
cleanup:
    if (fd != APP_INVALID) app_close(fd);
#ifdef _WIN32
    WSACleanup();
#endif
    return rc;
}

int psnr_app_unlock(const psnr_app_client* client, uint32_t id, psnr_app_trophy* result)
{ return app_call(client, 1, id, result); }

int psnr_app_status(const psnr_app_client* client, uint32_t id, psnr_app_trophy* result)
{ return app_call(client, 2, id, result); }
