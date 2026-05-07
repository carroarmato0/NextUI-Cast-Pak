// internal/discovery/scanner_test.go
package discovery_test

import (
	"testing"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/discovery"
)

func TestScanner_CacheStartsEmpty(t *testing.T) {
	s := discovery.NewScanner(func(service string, timeout time.Duration) ([]discovery.Device, error) {
		return nil, nil
	})
	devs := s.Cached()
	if len(devs) != 0 {
		t.Errorf("fresh cache should be empty, got %d", len(devs))
	}
}

func TestScanner_ScanPopulatesCache(t *testing.T) {
	want := []discovery.Device{{Name: "TV", Addr: "192.168.1.5:8009", Model: "Chromecast"}}
	s := discovery.NewScanner(func(service string, timeout time.Duration) ([]discovery.Device, error) {
		return want, nil
	})
	if err := s.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := s.Cached()
	if len(got) != 1 || got[0].Name != "TV" {
		t.Errorf("cache = %+v, want %+v", got, want)
	}
}

func TestScanner_FindByName(t *testing.T) {
	devs := []discovery.Device{
		{Name: "Living Room", Addr: "192.168.1.5:8009"},
		{Name: "Bedroom TV", Addr: "192.168.1.6:8009"},
	}
	s := discovery.NewScanner(func(service string, timeout time.Duration) ([]discovery.Device, error) {
		return devs, nil
	})
	if err := s.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	found, ok := s.FindByName("Bedroom TV")
	if !ok {
		t.Fatal("FindByName should find existing device")
	}
	if found.Addr != "192.168.1.6:8009" {
		t.Errorf("wrong addr: %q", found.Addr)
	}

	_, ok = s.FindByName("Kitchen")
	if ok {
		t.Error("FindByName should return false for unknown device")
	}
}
