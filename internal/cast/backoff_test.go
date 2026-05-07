// internal/cast/backoff_test.go
package cast_test

import (
	"testing"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/cast"
)

func TestBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 30 * time.Second},
		{10, 30 * time.Second},
	}
	for _, tc := range cases {
		got := cast.Backoff(tc.attempt)
		if got != tc.want {
			t.Errorf("Backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}
