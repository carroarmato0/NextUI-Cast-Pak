package discovery

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

type QueryFn func(service string, timeout time.Duration) ([]Device, error)

type Scanner struct {
	queryFn QueryFn
	mu      sync.RWMutex
	cache   []Device
}

func NewScanner(queryFn QueryFn) *Scanner {
	return &Scanner{queryFn: queryFn}
}

// NewRealScanner returns a Scanner that performs real mDNS queries.
func NewRealScanner() *Scanner {
	return NewScanner(func(service string, timeout time.Duration) ([]Device, error) {
		ch := make(chan *mdns.ServiceEntry, 20)
		var devs []Device
		done := make(chan struct{})
		go func() {
			defer close(done)
			for entry := range ch {
				// Filter audio-only devices: VIDEO_OUT is bit 0x01.
				if ca, ok := capabilitiesFromInfoFields(entry.InfoFields); ok && (ca&0x01) == 0 {
					continue
				}

				ip := entry.AddrV4.To4()
				if ip == nil {
					ip = entry.AddrV4
				}
				if ip == nil {
					continue // no usable IPv4 address
				}

				name := friendlyNameFromInfoFields(entry.InfoFields)
				if name == "" {
					// Strip mDNS suffix; raw name looks like "SomeName._googlecast._tcp.local."
					name = strings.SplitN(entry.Name, ".", 2)[0]
				}

				devs = append(devs, Device{
					Name:  name,
					Addr:  fmt.Sprintf("%s:%d", ip, entry.Port),
					Model: modelFromInfoFields(entry.InfoFields),
				})
			}
		}()
		params := &mdns.QueryParam{
			Service: service,
			Timeout: timeout,
			Entries: ch,
		}
		err := mdns.Query(params)
		// mdns.Query does not close Entries; we close it here after the query completes.
		close(ch)
		<-done
		return devs, err
	})
}

// friendlyNameFromInfoFields extracts the "fn" TXT record (user-visible device name).
func friendlyNameFromInfoFields(fields []string) string {
	for _, f := range fields {
		if strings.HasPrefix(f, "fn=") {
			return strings.TrimPrefix(f, "fn=")
		}
	}
	return ""
}

// modelFromInfoFields extracts the "md" value from mDNS TXT record fields.
// Each field is expected to be in "key=value" format.
func modelFromInfoFields(fields []string) string {
	for _, f := range fields {
		if strings.HasPrefix(f, "md=") {
			return strings.TrimPrefix(f, "md=")
		}
	}
	return ""
}

// capabilitiesFromInfoFields extracts the "ca" capabilities bitmask.
// Returns (value, true) when present, (0, false) when absent.
func capabilitiesFromInfoFields(fields []string) (uint32, bool) {
	for _, f := range fields {
		if strings.HasPrefix(f, "ca=") {
			v, err := strconv.ParseUint(strings.TrimPrefix(f, "ca="), 10, 32)
			if err == nil {
				return uint32(v), true
			}
		}
	}
	return 0, false
}

func (s *Scanner) Scan() error {
	devs, err := s.queryFn("_googlecast._tcp", 5*time.Second)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cache = devs
	s.mu.Unlock()
	return nil
}

func (s *Scanner) Cached() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Device, len(s.cache))
	copy(out, s.cache)
	return out
}

func (s *Scanner) FindByName(name string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.cache {
		if d.Name == name {
			return d, true
		}
	}
	return Device{}, false
}
