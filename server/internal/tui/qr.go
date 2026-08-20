package tui

import (
	"fmt"
	"net"

	"github.com/skip2/go-qrcode"
)

// localIPv4 finds the address a phone on the same LAN would actually use to
// reach this server, unlike Server.Status().ListenAddr, which reports the
// wildcard bind address (0.0.0.0:port) and isn't itself dialable.
//
// Deliberately not "the first non-loopback IPv4 on an up interface" —
// net.InterfaceAddrs' enumeration order isn't meaningful, and a Windows box
// running WSL2 (this project's actual deployment target) typically has
// several virtual adapters (vEthernet (WSL), Hyper-V Default Switch, a VPN
// client) alongside the real Wi-Fi/Ethernet one; picking whichever came
// first is the leading suspect for a real report of "scanned the QR, app
// sits on Connecting... forever" — the symptom matches an unreachable
// address rather than a protocol/parsing bug. Instead this asks the OS
// which address it would
// actually use to reach the internet (a UDP "connect" doesn't transmit
// anything — it's connectionless — so this is purely a routing-table
// lookup, no packet sent to 8.8.8.8 or anywhere else): that's the same
// adapter a phone on a normal home/office LAN would use to reach this
// machine, since it's the one with a real default-gateway route rather
// than an internal-only virtual switch.
func localIPv4() (string, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("tui: determining outbound-facing address: %w", err)
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", fmt.Errorf("tui: unexpected local address type %T", conn.LocalAddr())
	}
	return addr.IP.String(), nil
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
