package cast

import "time"

// Backoff returns the wait duration for a given retry attempt (0-indexed).
// Sequence: 1s, 2s, 4s, 8s, then capped at 30s.
func Backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > 8*time.Second {
		return 30 * time.Second
	}
	return d
}
