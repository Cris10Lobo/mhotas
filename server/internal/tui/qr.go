package tui

import (
	"fmt"
	"net"

	"github.com/skip2/go-qrcode"
)

// localIPv4 guesses the LAN address a phone would use. A UDP dial sends
// nothing; it only asks the routing table, which avoids picking a
// Docker/VPN/Hyper-V adapter the way interface enumeration order can. It is
// wrong behind NAT (VPN split tunnel, VM/WSL NAT), so alts lists the other
// candidates for the user to try.
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

// connectQR renders addr ("ip:port") as a terminal QR code. Medium error
// correction keeps the terminal footprint small.
func connectQR(addr string) (qr string, err error) {
	code, err := qrcode.New(addr, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("tui: generating QR code: %w", err)
	}
	return code.ToSmallString(false), nil
}
