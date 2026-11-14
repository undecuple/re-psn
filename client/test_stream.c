/* Relay setup against the synthetic responders in server/client_stream_test.go. */
#include "psnr.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#  include <winsock2.h>
#  include <windows.h>
typedef SOCKET stream_socket;
#  define stream_close closesocket
#else
#  include <sys/socket.h>
#  include <unistd.h>
typedef int stream_socket;
#  define stream_close close
#endif

int main(int argc, char** argv)
{
    psnr_client* c;
    int64_t stream;
    stream_socket s;
    char byte;
    assert(argc == 5);
    c = psnr_connect_timeout("127.0.0.1", (uint16_t)atoi(argv[1]), "STREAM-TEST", "stream-test", 0,
                             NULL, NULL, 2000);
    assert(c);
    if (strcmp(argv[2], "accept") == 0)
        stream = psnr_stream_accept(c, 123, atoi(argv[3]));
    else
        stream = psnr_stream_connect(c, 2, 7, atoi(argv[3]));
    printf("result=%s\n", stream < 0 ? "failure" : "success");
    if (strcmp(argv[4], "success") == 0) {
        assert(stream >= 0);
        s = (stream_socket)stream;
        /* The responder delays this byte: recv must block rather than report would-block. */
        assert(recv(s, &byte, 1, 0) == 1 && byte == 'x');
        assert(send(s, "y", 1, 0) == 1);
        stream_close(s);
    } else {
        if (stream >= 0) stream_close((stream_socket)stream);
    }
    psnr_close(c);
    return 0;
}
