// psnr: a stand-in for PSN's matchmaking and leaderboard services, for
// statically recompiled PS3 titles. The runtime's sceNp modules talk to it
// over the protocol in docs/api.md; peers then talk to each other directly.
// See docs/architecture.md.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

var version = "0.5.0"

func main() {
	var bad error
	num := func(name string, def int, usage string) *int {
		n, err := envInt(name, def)
		if err != nil {
			bad = err
		}
		return flag.Int(name, n, usage)
	}
	toggle := func(name string, def bool, usage string) *bool {
		v, err := envBool(name, def)
		if err != nil {
			bad = err
		}
		return flag.Bool(name, v, usage)
	}
	addr := flag.String("addr", envStr("addr", ":36100"), "TCP and UDP address for game clients")
	tcpAddr := flag.String("tcp-addr", envStr("tcp-addr", ""), "TCP address for game clients (default: -addr)")
	udpAddr := flag.String("udp-addr", envStr("udp-addr", ""), "UDP address for game clients (default: -addr)")
	httpAddr := flag.String("http", envStr("http", ""), "HTTP status page, /api/stats and /healthz, off unless set (for example 127.0.0.1:36101)")
	adminAddr := flag.String("admin", envStr("admin", ""), "EXPERIMENTAL read-only admin API on this address, off unless set; needs -admin-read-token-file, and an address that is not loopback also needs -admin-tls-cert and -admin-tls-key (see docs/admin-api.md)")
	adminRead := flag.String("admin-read-token-file", envStr("admin-read-token-file", ""), "file holding the admin read token (at least 32 characters, mode 0600); read again on SIGHUP")
	adminCert := flag.String("admin-tls-cert", envStr("admin-tls-cert", ""), "certificate file for the admin listener (TLS 1.2 or newer)")
	adminKey := flag.String("admin-tls-key", envStr("admin-tls-key", ""), "private key file for the admin listener")
	httpToken := flag.String("http-token", "", "require this token (Authorization: Bearer, or ?token=) on the status page; set it when -http is not on localhost")
	maxRooms := num("max-rooms", 1000, "room limit across all titles")
	playerRooms := num("max-player-rooms", 8, "rooms one player may be in at once (0 for no limit)")
	perAddr := num("max-conns-per-addr", 32, "open connections per remote address (0 for no limit)")
	reqRate := num("max-req-rate", 200, "requests per second per connection before it is closed (0 for no limit)")
	udpRate := flag.String("max-udp-rate", "1000", "UDP datagrams per second per source (IPv4 address or IPv6 /64), with the same burst allowance (0 for no limit)")
	verbose := toggle("v", false, "same as -log-level debug")
	logLevel := flag.String("log-level", envStr("log-level", "info"), "log level: debug (room events), info, warn or error")
	logFormat := flag.String("log-format", envStr("log-format", "text"), "log format: text or json")
	logFile := flag.String("log-file", envStr("log-file", ""), "also append the log to this file (mode 0600)")
	trustedList := flag.String("trusted-proxies", envStr("trusted-proxies", ""), "comma-separated CIDRs or addresses of reverse proxies allowed to name the real client (empty: none)")
	proxyProto := toggle("proxy-protocol", false, "require a PROXY protocol v1/v2 header on the game TCP port from a trusted proxy (needs -trusted-proxies)")
	udpAny := toggle("udp-any-source", false, "accept UDP probes and relay packets from an address other than the player's TCP address (for players whose UDP leaves from another address, such as some multi-WAN or carrier NAT setups; weakens the check against spoofed or stolen-token probes)")
	relay := toggle("relay", false, "carry traffic between players who can't reach each other directly (uses this server's bandwidth)")
	relayForce := toggle("relay-force", false, "send every stream through the relay, whether or not the players could connect directly (for testing the relay from one network; needs -relay; uses this server's bandwidth)")
	lanPublic := flag.String("lan-public-addr", "", "IPv4 address to give players outside the LAN as the address of players who connected over a private, CGNAT or link-local address (the host must forward each such player's P2P port to them; off unless set)")
	discover := toggle("discover", false, "answer LAN discovery broadcasts on the game UDP port, from private-network senders only (off by default)")
	name := flag.String("name", "", "server name shown in LAN discovery, at most 24 printable ASCII bytes (default: none)")
	db := flag.String("db", "off", "score storage: off or memory (both keep scores in memory; no database backend is built yet)")
	dbFile := flag.String("db-file", "", "read the -db setting from this file instead (it may hold a password)")
	medius := toggle("medius", false, "enable Medius compatibility (off by default). No Medius protocol is implemented yet: this opens the -medius-port listener, which closes every connection. See docs/medius-scope.md")
	mediusPort := num("medius-port", defaultMediusPort, "TCP port of the Medius listener (a value other than the default needs -medius)")
	mediusAddr := flag.String("medius-addr", envStr("medius-addr", defaultMediusAddr), "address of the Medius listener: a loopback, private or link-local IP address, or 0.0.0.0 or :: inside a container (a value other than the default needs -medius)")
	replace := toggle("replace", false, "if the game port is already in use by an earlier instance of this server, ask it to stop, wait for the port, then start (Linux only)")
	flag.Parse()
	if bad != nil {
		log.Fatal(bad)
	}
	if *replace && !replaceSupported {
		log.Fatalf("-replace is not supported on %s", runtime.GOOS)
	}
	onCmdLine := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { onCmdLine[f.Name] = true })
	udpRateN, err := parseUDPRate(lookupSetting("max-udp-rate", *udpRate, onCmdLine["max-udp-rate"]))
	if err != nil {
		log.Fatal(err)
	}
	lanSet := lookupSetting("lan-public-addr", *lanPublic, onCmdLine["lan-public-addr"])
	lanPublicIP, err := parseLANPublic(lanSet.val)
	if err != nil {
		log.Fatal(err)
	}
	tokenSet := lookupSetting("http-token", *httpToken, onCmdLine["http-token"])
	statusToken := tokenSet.val
	dbSet := lookupSetting("db", *db, onCmdLine["db"])
	dbFileSet := lookupSetting("db-file", *dbFile, onCmdLine["db-file"])

	level, format, err := parseLogFlags(*logLevel, *logFormat)
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		level = slog.LevelDebug
	}
	var out io.Writer = os.Stderr
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			log.Fatal(err)
		}
		out = io.MultiWriter(os.Stderr, f)
	}
	logger := newLogger(out, level, format)
	logEnvNotices(logger)
	fatal := func(err error) {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}

	scores, err := openStore(dbSet, dbFileSet)
	if err != nil {
		fatal(err)
	}
	logger.Info("storage", "backend", scores.Name())

	s := NewServer(*maxRooms, *verbose)
	s.log = logger
	s.scores = scores
	s.relay = *relay
	if *relayForce && !*relay {
		fatal(errors.New("-relay-force needs -relay"))
	}
	s.relayForce = *relayForce
	s.lanPublic = lanPublicIP
	if lanPublicIP != ([4]byte{}) {
		logger.Info("lan public address", "addr", ipString(lanPublicIP))
	}
	if *relayForce {
		logger.Warn("-relay-force: every stream goes through the relay; use it for testing only, it costs this server's bandwidth")
	}
	trusted, err := parseTrusted(*trustedList)
	if err != nil {
		fatal(err)
	}
	if *proxyProto && len(trusted) == 0 {
		fatal(errors.New("-proxy-protocol needs -trusted-proxies: a header is only believed from a listed proxy"))
	}
	for _, p := range trusted {
		if p.Bits() == 0 {
			logger.Warn("trusted proxies include every address; any client can claim to be anyone", "prefix", p.String())
		}
	}
	s.trustedProxies, s.proxyProto = trusted, *proxyProto
	s.udpAnySource = *udpAny
	nameSet := lookupSetting("name", *name, onCmdLine["name"])
	if err := validServerName(nameSet.val); err != nil {
		fatal(err)
	}
	s.discover, s.name = *discover, nameSet.val
	if *udpAny {
		logger.Warn("-udp-any-source: UDP probes and relay packets are accepted from any address that has the right token")
	}
	s.maxPlayerRooms, s.maxPerAddr, s.maxReqRate = *playerRooms, *perAddr, *reqRate
	s.maxUDPRate = udpRateN
	var med *mediusListener
	if *medius {
		mediusListen, wide, err := parseMediusAddr(*mediusAddr, *mediusPort)
		if err != nil {
			log.Fatal(err)
		}
		med, err = newMediusListener(mediusListen, logger)
		if err != nil {
			log.Fatal(err)
		}
		logger.Warn("Medius compatibility is on, but no Medius protocol is implemented: every connection to the Medius port is closed", "addr", med.Addr())
		if wide {
			logger.Warn("the Medius listener accepts connections on every interface; publish its port only to a private network")
		}
	} else if *mediusPort != defaultMediusPort || *mediusAddr != defaultMediusAddr {
		log.Fatal("a -medius-port or -medius-addr other than the default does nothing without -medius")
	}
	tcpA, udpA := listenAddrs(*addr, *tcpAddr, *udpAddr)
	ln, pc, err := listenGame(logger, tcpA, udpA, *replace)
	if err != nil {
		fatal(err)
	}
	s.tcpPort = uint16(ln.Addr().(*net.TCPAddr).Port)
	if s.discover {
		logDiscoveryOn(logger, s.name)
	}
	pidPath := writePidFile(logger, portOf(ln.Addr().String()), ln.Addr().String())

	// SIGINT or SIGTERM stops the server; the listeners closing then is not
	// an error. Anything else a serving goroutine returns is fatal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, stopSignals()...)
	defer signal.Stop(stop)
	errc := make(chan error, 5)
	go func() { errc <- s.ServeUDP(pc) }()
	logger.Info("started", "version", version, "clients", ln.Addr().String(), "udp", pc.LocalAddr().String(), "relay", *relay)
	var hs *http.Server
	if *httpAddr != "" {
		s.httpToken = statusToken
		logger.Info("status page", "addr", *httpAddr, "token", statusToken != "")
		if reachable(*httpAddr) && statusToken == "" {
			logger.Warn("the status page lists every online ID and has no token; set -http-token or bind -http to localhost")
		}
		hs = &http.Server{Addr: *httpAddr, Handler: s.HTTP()}
		go func() { errc <- hs.ListenAndServe() }()
	} else {
		logger.Debug("status page off (set -http to turn it on)")
	}
	var as *adminServer
	if *adminAddr != "" {
		as, err = newAdminServer(s, adminConfig{Addr: *adminAddr, ReadTokenFile: *adminRead, TLSCert: *adminCert, TLSKey: *adminKey,
			StatusAddr: *httpAddr, StatusToken: statusToken})
		if err != nil {
			removePidFile(pidPath)
			fatal(err)
		}
		logger.Warn("admin API is experimental: read-only, one token, no actions", "addr", as.Addr(), "tls", as.tls)
		go func() { errc <- as.Serve() }()
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		defer signal.Stop(hup)
		go func() {
			for range hup {
				if err := as.reload(); err != nil {
					logger.Warn("admin token reload failed; the previous token stays in force", "err", err)
				} else {
					logger.Info("admin token reloaded")
				}
			}
		}()
	} else if *adminRead != "" || *adminCert != "" || *adminKey != "" {
		removePidFile(pidPath)
		fatal(errors.New("-admin-read-token-file, -admin-tls-cert and -admin-tls-key do nothing without -admin"))
	}
	if med != nil {
		go func() { errc <- med.Serve() }()
	}
	go func() { errc <- s.Serve(ln) }()
	var serveErr error
	select {
	case serveErr = <-errc:
	case <-stop:
	}
	logger.Info("stopping")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		logger.Warn("game shutdown deadline", "err", err)
	}
	// A serving goroutine may not have registered its listener before a
	// startup failure, so also close the descriptors opened by main.
	ln.Close()
	pc.Close()
	if med != nil {
		med.Shutdown()
	}
	if hs != nil {
		if err := hs.Shutdown(ctx); err != nil {
			hs.Close()
		}
	}
	if as != nil {
		as.Shutdown(ctx)
	}
	removePidFile(pidPath)
	if serveErr != nil {
		fatal(serveErr)
	}
}

// stopSignals are the signals that stop the server: Windows has only Ctrl-C.
func stopSignals() []os.Signal {
	if runtime.GOOS == "windows" {
		return []os.Signal{os.Interrupt}
	}
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// logDiscoveryOn logs that LAN discovery is on. The name goes in as a plain
// string; the log handler quotes and escapes it as its format needs.
func logDiscoveryOn(logger *slog.Logger, name string) {
	logger.Info("lan discovery on", "name", name)
}
