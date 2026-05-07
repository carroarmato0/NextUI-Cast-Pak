package wifi

import (
	"net"
	"strings"
)

type InterfacesFn func() ([]net.Interface, error)
type AddrsFn func(net.Interface) ([]net.Addr, error)

func defaultAddrs(iface net.Interface) ([]net.Addr, error) {
	return iface.Addrs()
}

// HasWiFi returns true if any non-loopback, up interface with a name starting
// with "wlan" or "wl" has an IPv4 address. Pass nil for addrsFn to use the
// real implementation.
func HasWiFi(ifacesFn InterfacesFn, addrsFn AddrsFn) bool {
	return LocalIP(ifacesFn, addrsFn) != ""
}

// LocalIP returns the first IPv4 address of a non-loopback, up wireless
// interface (name prefix "wlan" or "wl"), or "" if none found.
func LocalIP(ifacesFn InterfacesFn, addrsFn AddrsFn) string {
	if ifacesFn == nil {
		ifacesFn = net.Interfaces
	}
	if addrsFn == nil {
		addrsFn = defaultAddrs
	}
	ifaces, err := ifacesFn()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if !strings.HasPrefix(iface.Name, "wlan") && !strings.HasPrefix(iface.Name, "wl") {
			continue
		}
		addrs, err := addrsFn(iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}
