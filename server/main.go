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
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

var version = "0.4.0"

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
	db := flag.String("db", "off", "score storage: off or memory (both keep scores in memory; no database backend is built yet)")
	dbFile := flag.String("db-file", "", "read the -db setting from this file instead (it may hold a password)")
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
	if *udpAny {
		logger.Warn("-udp-any-source: UDP probes and relay packets are accepted from any address that has the right token")
	}
	s.maxPlayerRooms, s.maxPerAddr, s.maxReqRate = *playerRooms, *perAddr, *reqRate
	s.maxUDPRate = udpRateN
	tcpA, udpA := listenAddrs(*addr, *tcpAddr, *udpAddr)
	ln, pc, err := listenGame(logger, tcpA, udpA, *replace)
	if err != nil {
		fatal(err)
	}
	pidPath := writePidFile(logger, portOf(ln.Addr().String()), ln.Addr().String())

	// SIGINT or SIGTERM stops the server; the listeners closing then is not
	// an error. Anything else a serving goroutine returns is fatal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, stopSignals()...)
	defer signal.Stop(stop)
	errc := make(chan error, 3)
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
	if hs != nil {
		if err := hs.Shutdown(ctx); err != nil {
			hs.Close()
		}
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
