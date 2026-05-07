package wifi_test

import (
	"net"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

func makeIfaces(entries []struct {
	name  string
	flags net.Flags
	addrs []string
}) func() ([]net.Interface, error) {
	return func() ([]net.Interface, error) {
		var ifaces []net.Interface
		for _, e := range entries {
			ifaces = append(ifaces, net.Interface{Name: e.name, Flags: e.flags})
		}
		return ifaces, nil
	}
}

func makeAddrs(m map[string][]string) func(net.Interface) ([]net.Addr, error) {
	return func(iface net.Interface) ([]net.Addr, error) {
		var addrs []net.Addr
		for _, s := range m[iface.Name] {
			// ParseCIDR masks the host bits; preserve the host IP as iface.Addrs() does.
			ip, ipnet, _ := net.ParseCIDR(s)
			ipnet.IP = ip
			addrs = append(addrs, ipnet)
		}
		return addrs, nil
	}
}

func TestHasWiFi_NoInterfaces(t *testing.T) {
	fn := makeIfaces(nil)
	if wifi.HasWiFi(fn, nil) {
		t.Error("expected false with no interfaces")
	}
}

func TestHasWiFi_LoopbackOnly(t *testing.T) {
	fn := makeIfaces([]struct {
		name  string
		flags net.Flags
		addrs []string
	}{{"lo", net.FlagLoopback | net.FlagUp, nil}})
	if wifi.HasWiFi(fn, nil) {
		t.Error("loopback-only should return false")
	}
}

func TestHasWiFi_WlanWithIP(t *testing.T) {
	ifaces := makeIfaces([]struct {
		name  string
		flags net.Flags
		addrs []string
	}{{"wlan0", net.FlagUp, nil}})
	addrs := makeAddrs(map[string][]string{"wlan0": {"192.168.1.10/24"}})
	if !wifi.HasWiFi(ifaces, addrs) {
		t.Error("wlan0 with IP should return true")
	}
}

func TestLocalIP_ReturnsWlanIP(t *testing.T) {
	ifaces := makeIfaces([]struct {
		name  string
		flags net.Flags
		addrs []string
	}{{"wlan0", net.FlagUp, nil}})
	addrs := makeAddrs(map[string][]string{"wlan0": {"192.168.1.10/24"}})
	ip := wifi.LocalIP(ifaces, addrs)
	if ip != "192.168.1.10" {
		t.Errorf("LocalIP = %q, want %q", ip, "192.168.1.10")
	}
}
