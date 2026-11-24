package webui

import (
	"errors"
	"net"
	"sort"
	"strings"
)

// maxInviteAddresses bounds the list shown on the Host page; a machine with many virtual interfaces would otherwise
// bury the few addresses that matter.
const maxInviteAddresses = 12

// addrLister returns this computer's interface addresses. It is a field on Server so tests can inject a table; the
// real one only reads the operating system's interface list and contacts nothing.
type addrLister func() ([]net.IP, error)

// InviteAddress is one candidate address with the host's port. Kind is "loopback", "link-local", "private", "shared", "other" or "unknown".
type InviteAddress struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`
}

// Invite is what the Host page shows for a running owned host. It is advisory: the candidates are a guess made from the
// interface list, and nothing here says which address (if any) is reachable from outside.
type Invite struct {
	// Bind is the listen address as configured.
	Bind string `json:"bind"`
	Port string `json:"port"`
	// LoopbackOnly means only this computer can connect.
	LoopbackOnly bool `json:"loopback_only"`
	// AllInterfaces means the bind is a wildcard, so the candidates come from the interface list.
	AllInterfaces bool            `json:"all_interfaces"`
	Addresses     []InviteAddress `json:"addresses"`
	// Truncated means more candidates existed than are listed.
	Truncated bool `json:"truncated"`
	// ListFailed means the interface list could not be read.
	ListFailed bool `json:"list_failed"`
}

// loopbackBind reports whether a host:port bind accepts only connections from this computer.
func loopbackBind(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// addressKind sorts an address by who can reach it. Shared address space (100.64/10) is "shared"; documentation ranges are
// "other", which the page words as "may be reachable from outside"; an address that did not parse is "unknown".
func addressKind(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsLinkLocalUnicast():
		return "link-local"
	case ip.IsPrivate():
		return "private"
	case ip == nil:
		return "unknown"
	case sharedSpace.Contains(ip):
		return "shared"
	}
	return "other"
}

// sharedSpace is 100.64.0.0/10, the shared address space used by carrier-grade NAT and by overlay networks such as
// Tailscale: not reachable from the internet, and only by others on the same overlay.
var sharedSpace = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

var kindRank = map[string]int{"private": 0, "shared": 1, "other": 2, "unknown": 3, "link-local": 4, "loopback": 5}

// buildInvite describes a bind for the Host page. A specific bind lists only itself; a wildcard bind lists the
// lister's addresses (IPv4 only for 0.0.0.0), sorted, de-duplicated and capped.
func buildInvite(bind string, list addrLister) (Invite, error) {
	host, port, err := net.SplitHostPort(bind)
	if err != nil || port == "" {
		return Invite{}, errors.New("The listen address is not host:port")
	}
	out := Invite{Bind: bind, Port: port, LoopbackOnly: loopbackBind(bind), Addresses: []InviteAddress{}}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsUnspecified() {
		out.Addresses = append(out.Addresses, InviteAddress{net.JoinHostPort(host, port), addressKind(ip)})
		if ip == nil && out.LoopbackOnly {
			out.Addresses[0].Kind = "loopback"
		}
		return out, nil
	}
	out.AllInterfaces = true
	if list == nil {
		out.ListFailed = true
		return out, nil
	}
	ips, err := list()
	if err != nil {
		out.ListFailed = true
		return out, nil
	}
	v4Only := ip.To4() != nil
	seen := map[string]bool{}
	for _, a := range ips {
		if a == nil || a.IsUnspecified() || a.IsMulticast() {
			continue
		}
		if v4 := a.To4(); v4 != nil {
			a = v4
		} else if v4Only {
			continue
		}
		addr := net.JoinHostPort(a.String(), port)
		if seen[addr] {
			continue
		}
		seen[addr] = true
		out.Addresses = append(out.Addresses, InviteAddress{addr, addressKind(a)})
	}
	sort.SliceStable(out.Addresses, func(i, j int) bool { return kindRank[out.Addresses[i].Kind] < kindRank[out.Addresses[j].Kind] })
	if len(out.Addresses) > maxInviteAddresses {
		out.Addresses, out.Truncated = out.Addresses[:maxInviteAddresses], true
	}
	return out, nil
}

// interfaceAddresses is the real lister. It skips interfaces that are down and loopback interfaces, which are noise
// when choosing an address for other computers. It reads the local interface table only.
func interfaceAddresses() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				out = append(out, v.IP)
			case *net.IPAddr:
				out = append(out, v.IP)
			}
		}
	}
	return out, nil
}
