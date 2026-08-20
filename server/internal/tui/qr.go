package tui

import (
	"fmt"
	"net"

	"github.com/skip2/go-qrcode"
)

// localIPv4 picks the first non-loopback IPv4 address on an up interface —
// the address a phone on the same LAN would actually use to reach this
// server, unlike Server.Status().ListenAddr, which reports the wildcard
// bind address (0.0.0.0:port) and isn't itself dialable.
func localIPv4() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("tui: listing interface addresses: %w", err)
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("tui: no non-loopback IPv4 address found")
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
