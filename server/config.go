package main

// Every flag can also be set from the environment as RE_PSN_<FLAG>, with the
// dashes as underscores (-max-rooms is RE_PSN_MAX_ROOMS). The old name,
// PSNR_<FLAG>, still works when the new one is not set. A flag on the command
// line wins over both. A variable that does not parse stops the server at
// start instead of being ignored.

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/sp00nznet/psnr/server/store"
)

func envName(flag string) string {
	return "RE_PSN_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// oldEnvName is the name before the rename, still read as an alias.
func oldEnvName(flag string) string {
	return "PSNR_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// envNotice is a warning about an old variable name. It holds names only,
// never a value: some of these variables carry tokens.
type envNotice struct{ msg, old, new string }

// envNotices collects the notices while the flags are read, before the logger
// exists; logEnvNotices writes them once it does.
var envNotices []envNotice

// lookupEnv returns the variable for flag and the name it came from:
// RE_PSN_<FLAG> if set (even empty), else PSNR_<FLAG>.
func lookupEnv(flag string) (string, string, bool) {
	name, old := envName(flag), oldEnvName(flag)
	v, ok := os.LookupEnv(name)
	ov, oldOK := os.LookupEnv(old)
	switch {
	case ok && oldOK && v != ov:
		envNotices = append(envNotices, envNotice{"both the new and the old variable are set and differ; using the new one", old, name})
	case !ok && oldOK:
		envNotices = append(envNotices, envNotice{"old variable name; it still works, rename it when convenient", old, name})
		return ov, old, true
	}
	return v, name, ok
}

func logEnvNotices(l *slog.Logger) {
	for _, n := range envNotices {
		l.Warn(n.msg, "old", n.old, "new", n.new)
	}
	envNotices = nil
}

// envStr returns the variable for flag, or def when it is not set.
func envStr(flag, def string) string {
	if v, _, ok := lookupEnv(flag); ok {
		return v
	}
	return def
}

func envInt(flag string, def int) (int, error) {
	v, name, ok := lookupEnv(flag)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: not a number", name)
	}
	return n, nil
}

func envBool(flag string, def bool) (bool, error) {
	v, name, ok := lookupEnv(flag)
	if !ok {
		return def, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false, fmt.Errorf("%s: not true or false", name)
	}
	return b, nil
}

// listenAddrs picks the TCP and UDP addresses: -tcp-addr and -udp-addr when
// set, else -addr for both.
func listenAddrs(addr, tcp, udp string) (string, string) {
	if tcp == "" {
		tcp = addr
	}
	if udp == "" {
		udp = addr
	}
	return tcp, udp
}

// setting is a value with the name it was read under (-db, RE_PSN_DB,
// PSNR_DB), for errors that name the setting but not its value.
type setting struct {
	val, name string
	set       bool
}

// lookupSetting reads flag from the environment, then lets the command line
// win. The flag's own default stays fixed so -h never shows the variable:
// -db can hold a password and -http-token is one.
func lookupSetting(flag, val string, onCmdLine bool) setting {
	v, name, ok := lookupEnv(flag)
	if onCmdLine {
		return setting{val, "-" + flag, true}
	}
	return setting{v, name, ok}
}

// dbSetting returns the storage setting (-db, or the contents of the file
// -db-file names) and the name to put in its errors. Neither error nor name
// holds the setting or the file's contents; only the file's path.
func dbSetting(db, file setting) (string, string, error) {
	switch {
	case db.set && file.set:
		return "", "", fmt.Errorf("%s and %s are both set: use one", db.name, file.name)
	case file.set:
		b, err := os.ReadFile(file.val)
		if err != nil {
			reason := "error"
			if pe, ok := err.(*os.PathError); ok {
				reason = pe.Err.Error()
			}
			return "", "", fmt.Errorf("%s: could not read the file %q: %s", file.name, file.val, reason)
		}
		return string(b), file.name + " (the file's contents)", nil
	case db.set:
		return db.val, db.name, nil
	}
	return "off", "-db", nil
}

// openStore opens the score store the settings name.
func openStore(db, file setting) (store.Store, error) {
	v, name, err := dbSetting(db, file)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return st, nil
}

// Parse this flag after selection so neither flag.Parse nor -h can print its
// environment/command-line value in an error or default. Only its name leaves
// this function. Command-line and new-name precedence apply before validation.
func parseUDPRate(s setting) (int, error) {
	if !s.set {
		return defaultUDPRate, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s.val))
	if err != nil {
		return 0, fmt.Errorf("%s: not a number", s.name)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be zero or positive", s.name)
	}
	return n, nil
}
