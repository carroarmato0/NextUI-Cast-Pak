// internal/stream/alsa_test.go
package stream_test

import (
	"fmt"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

func TestProbeALSA_FirstDeviceWorks(t *testing.T) {
	runner := func(name string, args ...string) error { return nil }
	got := stream.ProbeALSA(runner)
	if got != "hw:0,0" {
		t.Errorf("got %q, want %q", got, "hw:0,0")
	}
}

func TestProbeALSA_FallsThrough(t *testing.T) {
	calls := 0
	runner := func(name string, args ...string) error {
		calls++
		if calls < 2 {
			return fmt.Errorf("fail")
		}
		return nil
	}
	got := stream.ProbeALSA(runner)
	if got != "default" {
		t.Errorf("got %q, want %q", got, "default")
	}
}

func TestProbeALSA_AllFail(t *testing.T) {
	runner := func(name string, args ...string) error { return fmt.Errorf("fail") }
	got := stream.ProbeALSA(runner)
	if got != "" {
		t.Errorf("all fail: got %q, want empty", got)
	}
}
