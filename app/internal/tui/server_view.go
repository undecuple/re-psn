package tui

import (
	"fmt"
	"strings"
)

const notReported = "not reported by this server"

// humanBytes writes n in binary units, with the exact count for anything of 1 KiB or more.
func humanBytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v, unit := float64(n), ""
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		v /= 1024
		unit = u
		if v < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s (%d B)", v, unit, n)
}

func humanRate(r float64) string {
	if r < 1024 {
		return fmt.Sprintf("%.0f B/s", r)
	}
	return fmt.Sprintf("%.1f KiB/s", r/1024)
}

func limitText(p *uint64) string {
	switch {
	case p == nil:
		return notReported
	case *p == 0:
		return "none"
	}
	return fmt.Sprint(*p)
}

// countList writes name/count pairs; a group the server did not report at all says so once.
func countList(names []string, vals []*uint64) string {
	any := false
	for _, v := range vals {
		any = any || v != nil
	}
	if !any {
		return notReported
	}
	var b []string
	for i, v := range vals {
		if v == nil {
			b = append(b, names[i]+" "+notReported)
		} else {
			b = append(b, fmt.Sprintf("%s %d", names[i], *v))
		}
	}
	return strings.Join(b, ", ")
}

// serverLines is the read-only Server tab: what the status page reports about the server itself.
func (m Model) serverLines() []string {
	b := []string{m.tabs(), "", m.st.muted.Render("Read-only. Nothing here changes the server."), ""}
	if m.set.StatusURL == "" {
		return append(b, m.st.text.Render("Set a status URL in Settings to see the server's own figures."))
	}
	b = append(b, m.connection()...)
	h := m.httpStats()
	if h == nil {
		return append(b, "", m.st.muted.Render("No status data yet: press r."))
	}
	s := h.Server
	b = append(b, "")
	if !s.Reported() {
		b = append(b, m.st.warn.Render("This server does not report version, relay state, limits or counters (an older server)."), "")
	}
	row := func(label, v string) { b = append(b, m.row12(label, v)) }
	if s.Version != nil {
		row("Version", httpText(*s.Version))
	} else {
		row("Version", notReported)
	}
	row("Uptime", httpText(h.Uptime))
	if s.Relay != nil {
		row("Relay", onOff(*s.Relay))
	} else {
		row("Relay", notReported)
	}
	b = append(b, "", m.st.heading.Render("Limits in force (none = no limit)"))
	l := s.Limits
	row("Rooms", limitText(l.MaxRooms))
	row("Player rooms", limitText(l.MaxPlayerRooms))
	row("Conns/address", limitText(l.MaxConnsPerAddr))
	row("TCP req/s", limitText(l.MaxReqRate))
	row("UDP rate", limitText(l.MaxUDPRate))
	b = append(b, "", m.st.heading.Render("Refusals and drops since start"))
	row("TCP refused", countList([]string{"per address", "request rate"}, []*uint64{s.TCP.ConnsPerAddr, s.TCP.ReqRate}))
	u := s.UDP
	row("UDP dropped", countList(
		[]string{"rate limit", "source capacity", "malformed", "unauthorized", "source mismatch", "relay disabled", "write error", "discover refused"},
		[]*uint64{u.RateLimit, u.SourceCapacity, u.Malformed, u.Unauthorized, u.SourceMismatch, u.RelayDisabled, u.WriteError, u.DiscoverRefused}))
	b = append(b, "", m.st.heading.Render("Relay traffic"))
	if s.RelayBytes == nil {
		row("Relay bytes", notReported)
	} else {
		row("Relay bytes", humanBytes(*s.RelayBytes)+" since start")
		switch t := m.track; {
		case t.restarted:
			row("Relay rate", "-")
		case t.hasRate:
			row("Relay rate", humanRate(t.rate)+" since the last refresh")
		default:
			row("Relay rate", "- (needs a second refresh: press r)")
		}
	}
	if m.track.restarted {
		b = append(b, "", m.st.warn.Render("Server restarted: a counter went down since the last refresh. The rate starts again from this refresh."))
	}
	return append(b, "", m.st.muted.Render("Relay bytes and rate are the payload carried by the relay, not network bandwidth or interface traffic."))
}

func (m Model) row12(label, value string) string {
	return m.st.muted.Render(fmt.Sprintf("%-14s", label)) + " " + m.st.text.Render(value)
}
