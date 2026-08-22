package tui

import (
	"fmt"
	"net"

	"github.com/skip2/go-qrcode"
)

// localIPv4 guesses the address a phone on the same LAN would use to reach
// this server (Server.Status().ListenAddr is just the wildcard bind
// address, 0.0.0.0:port, and isn't dialable). It asks the OS which local
// address it'd use to reach the internet — a UDP "connect" sends nothing,
// it's just a routing-table lookup — rather than taking the first
// non-loopback interface, whose enumeration order is meaningless and
// happily returns Docker/VPN/Hyper-V adapters ahead of the real NIC.
//
// Still a guess: it's wrong when something NATs between this box and the
// LAN (VPN split tunnel, container, VM/WSL in NAT mode) — no local API can
// see past that. alts lists every other candidate address as a manual
// fallback for that case; the human picks, since they're the one who knows
// which network the phone is actually on.
func localIPv4() (primary string, alts []string, err error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", nil, fmt.Errorf("tui: determining outbound-facing address: %w", err)
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", nil, fmt.Errorf("tui: unexpected local address type %T", conn.LocalAddr())
	}
	primary = addr.IP.String()

	addrs, _ := net.InterfaceAddrs() // best-effort: alts is a fallback, not load-bearing
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.String() == primary {
			continue
		}
		alts = append(alts, ip4.String())
	}
	return primary, alts, nil
}

// connectQR renders a terminal QR code encoding "ip:port" — the exact
// string connect_screen.dart's manual entry already expects, so scanning
// it (once the app grows a scanner) needs no new parsing on either side.
// Medium error-correction: enough resilience for a phone camera at a
// normal angle without inflating the code's terminal footprint the way
// High/Highest would.
func connectQR(addr string) (qr string, err error) {
	code, err := qrcode.New(addr, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("tui: generating QR code: %w", err)
	}
	return code.ToSmallString(false), nil
}
