/* Linux connect-wait regression fixture. Link with --wrap for deterministic
 * EINTR/time injection; the signal case uses the real socket and clock calls. */
#include <assert.h>
#include <signal.h>
#include <sys/wait.h>
#ifndef PSNR_SOURCE
#define PSNR_SOURCE "psnr.c"
#endif
#include PSNR_SOURCE

static int injected, armed, connecting, connect_polls, reply_polls;
static int real_connect_interruptions;
static int negative, expires, repeated;
static uint64_t fake_ms;

int __real_connect(int, const struct sockaddr*, socklen_t);
int __real_poll(struct pollfd*, nfds_t, int);
int __real_clock_gettime(clockid_t, struct timespec*);

int __wrap_clock_gettime(clockid_t id, struct timespec* ts)
{
    if (!armed || id != CLOCK_MONOTONIC) return __real_clock_gettime(id, ts);
    ts->tv_sec = (time_t)(fake_ms / 1000);
    ts->tv_nsec = (long)(fake_ms % 1000) * 1000000;
    return 0;
}

int __wrap_connect(int s, const struct sockaddr* a, socklen_t n)
{
    int result = __real_connect(s, a, n);
    if (!armed) {
        connecting = result < 0 && errno == EINPROGRESS;
        return result;
    }
    assert(result == 0 || errno == EINPROGRESS);
    connecting = 1;
    if (expires) fake_ms += 120;
    /* Loopback may connect immediately; force the same pending-connect wait. */
    errno = EINPROGRESS;
    return -1;
}

int __wrap_poll(struct pollfd* pf, nfds_t n, int timeout)
{
    int r;
    if (!armed) {
        r = __real_poll(pf, n, timeout);
        if (connecting && r < 0 && errno == EINTR) ++real_connect_interruptions;
        if (r > 0) connecting = 0;
        return r;
    }
    assert(n == 1);
    if (connecting) {
        ++connect_polls;
        assert(!expires); /* No wait is allowed after the shared budget expires. */
        assert(timeout == (negative ? -1 : 120 - injected * 40));
        if (injected < (repeated || negative ? 3 : 1)) {
            ++injected;
            fake_ms += 40;
            errno = EINTR;
            return -1;
        }
        r = __real_poll(pf, n, timeout);
        if (r > 0) connecting = 0;
        return r;
    }
    if (pf->events & POLLIN) {
        ++reply_polls;
        /* psnr_call's HELLO loop uses 10 ms slices. Stream setup must retain
         * the 40 ms spent interrupted in the preceding connect wait. */
        assert(timeout == 10 || timeout == (negative ? -1 : 80));
    }
    return __real_poll(pf, n, timeout);
}

static volatile sig_atomic_t signals_seen;
static void harmless(int sig) { (void)sig; ++signals_seen; }

static void read_exact(int s, uint8_t* p, size_t n)
{
    while (n) {
        ssize_t k = recv(s, p, n, 0);
        if (k < 0 && errno == EINTR) continue;
        assert(k > 0);
        p += k;
        n -= (size_t)k;
    }
}

static void respond(int listener, int control)
{
    uint8_t header[3], body[64];
    int s = accept(listener, NULL, NULL);
    assert(s >= 0);
    read_exact(s, header, sizeof(header));
    assert(psnr_get16(header + 1) <= sizeof(body));
    read_exact(s, body, psnr_get16(header + 1));
    /* Ensure the client's post-connect wait is exercised too. */
    usleep(20000);
    if (control) {
        uint8_t ack[] = {PSNR_HELLO_ACK, 0, 12, 0, 0, 0, 1, 0, 0, 0, 1, 127, 0, 0, 1};
        assert(header[0] == PSNR_HELLO);
        assert(send(s, ack, sizeof(ack), 0) == (ssize_t)sizeof(ack));
    } else {
        uint8_t ready[] = {PSNR_STREAM_READY, 0, 4, 0, 0, 0, 1};
        assert(header[0] == PSNR_STREAM_CONNECT || header[0] == PSNR_STREAM_ACCEPT);
        assert(send(s, ready, sizeof(ready), 0) == (ssize_t)sizeof(ready));
    }
    close(s);
}

int main(int argc, char** argv)
{
    struct sockaddr_in a = {0};
    socklen_t alen = sizeof(a);
    struct sigaction sa = {0};
    psnr_client c = {0}, *control = NULL;
    int listener, fill = -1, status, signal_case, success;
    int64_t stream = -1;
    pid_t child, parent = getpid();
    assert(argc == 3);
    signal_case = strcmp(argv[2], "signal") == 0 || strcmp(argv[2], "signal-negative") == 0;
    negative = strcmp(argv[2], "negative") == 0 || strcmp(argv[2], "signal-negative") == 0;
    expires = strcmp(argv[2], "expires") == 0;
    repeated = strcmp(argv[2], "repeated") == 0;
    success = !expires && !repeated;
    alarm(8);
    listener = socket(AF_INET, SOCK_STREAM, 0);
    assert(listener >= 0);
    a.sin_family = AF_INET;
    a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    assert(bind(listener, (struct sockaddr*)&a, sizeof(a)) == 0);
    assert(listen(listener, 0) == 0);
    assert(getsockname(listener, (struct sockaddr*)&a, &alen) == 0);
    if (signal_case) {
        fill = socket(AF_INET, SOCK_STREAM, 0);
        assert(fill >= 0 && connect(fill, (struct sockaddr*)&a, sizeof(a)) == 0);
        sa.sa_handler = harmless;
        sa.sa_flags = SA_RESTART; /* poll still reports EINTR with SA_RESTART. */
        sigemptyset(&sa.sa_mask);
        assert(sigaction(SIGUSR1, &sa, NULL) == 0);
    }
    child = fork();
    assert(child >= 0);
    if (child == 0) {
        alarm(5);
        if (signal_case) {
            int s;
            close(fill);
            usleep(100000);
            assert(kill(parent, SIGUSR1) == 0);
            usleep(200000);
            s = accept(listener, NULL, NULL);
            assert(s >= 0);
            close(s);
        }
        respond(listener, strcmp(argv[1], "control") == 0);
        _exit(0);
    }
    if (!signal_case) { armed = 1; fake_ms = 100000; }
    if (strcmp(argv[1], "control") == 0) {
        control = psnr_connect_timeout("127.0.0.1", ntohs(a.sin_port), "WAIT-TEST", "wait-test", 0,
                                       NULL, NULL, 120);
        assert((control != NULL) == success);
    } else {
        memcpy(c.server_ip, &a.sin_addr, 4);
        c.server_port = ntohs(a.sin_port);
        if (strcmp(argv[1], "accept") == 0)
            stream = psnr_stream_accept(&c, 123, negative ? -1 : signal_case ? 3000 : 120);
        else
            stream = psnr_stream_connect(&c, 2, 7, negative ? -1 : signal_case ? 3000 : 120);
        assert((stream >= 0) == success);
    }
    if (signal_case) assert(signals_seen == 1 && real_connect_interruptions == 1);
    else {
        assert(injected == (expires ? 0 : repeated || negative ? 3 : 1));
        assert(connect_polls == (expires ? 0 : repeated ? 3 : negative ? 4 : 2));
        if (success) assert(reply_polls > 0);
    }
    if (stream >= 0) close((int)stream);
    if (control) psnr_close(control);
    if (!success) assert(kill(child, SIGTERM) == 0);
    assert(waitpid(child, &status, 0) == child);
    if (success) assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);
    if (fill >= 0) close(fill);
    close(listener);
    printf("%s/%s: passed (%d injected, %d signal-interrupted connect waits)\n",
           argv[1], argv[2], injected, real_connect_interruptions);
    return 0;
}
