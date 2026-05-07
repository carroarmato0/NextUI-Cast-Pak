package discovery

import (
	"fmt"
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
				devs = append(devs, Device{
					Name:  entry.Name,
					Addr:  fmt.Sprintf("%s:%d", entry.AddrV4, entry.Port),
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
		close(ch)
		<-done
		return devs, err
	})
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
