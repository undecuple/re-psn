# Static library, headers and pkg-config file for the C client.
#   make                      build libpsnr.a
#   make install PREFIX=/usr/local [DESTDIR=...]
# The client uses POSIX declarations, so it builds with the compiler's gnu11 mode,
# not strict -std=c11. Windows builds use MSVC or CMake; see docs/sdk.md.
PREFIX  ?= /usr/local
CC      ?= cc
AR      ?= ar
CFLAGS  ?= -O2
VERSION := $(shell cat ../VERSION 2>/dev/null || echo 0)
OBJS    := psnr.o psnr_app.o

all: libpsnr.a psnr.pc

%.o: %.c psnr.h psnr_app.h
	$(CC) -std=gnu11 -Wall -Wextra $(CFLAGS) -c $< -o $@

libpsnr.a: $(OBJS)
	$(AR) rcs $@ $(OBJS)

# Always rewritten, so a changed PREFIX or VERSION never leaves a stale file behind.
# The values go through printf, so characters such as & and | in PREFIX are kept as they are.
# A space in PREFIX is not supported (pkg-config needs it escaped); neither is a $ in it.
SQ := '
shq = '$(subst $(SQ),'\'',$1)'
psnr.pc:
	printf '%s\n' 'prefix='$(call shq,$(PREFIX)) 'libdir=$${prefix}/lib' 'includedir=$${prefix}/include' '' \
	  'Name: psnr' 'Description: C client for the re-psn server (unofficial fan work)' \
	  'Version: '$(call shq,$(VERSION)) 'Libs: -L$${libdir} -lpsnr' 'Cflags: -I$${includedir}' > $@

install: all
	install -d $(DESTDIR)$(PREFIX)/lib/pkgconfig $(DESTDIR)$(PREFIX)/include
	install -m 644 libpsnr.a $(DESTDIR)$(PREFIX)/lib/
	install -m 644 psnr.h psnr_app.h $(DESTDIR)$(PREFIX)/include/
	install -m 644 psnr.pc $(DESTDIR)$(PREFIX)/lib/pkgconfig/

clean:
	rm -f $(OBJS) libpsnr.a psnr.pc

.PHONY: all install clean psnr.pc
