package logger

import (
	"bytes"
	"os"
)

const (
	logMaxBytes = 10 * 1024 * 1024
	logMaxRuns  = 5 // keep 4 old + 1 new = 5 total
)

// RotateLog trims path so that at most logMaxRuns sentinel-delimited runs are
// retained and the file does not exceed logMaxBytes.
//
// WARNING: startSentinel must not appear literally inside any log message body;
// every occurrence is treated as the start of a new run.
func RotateLog(path, startSentinel string) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	runs := splitRuns(data, startSentinel)
	if len(runs) > logMaxRuns-1 {
		runs = runs[len(runs)-(logMaxRuns-1):]
	}
	total := 0
	for _, r := range runs {
		total += len(r)
	}
	for total > logMaxBytes && len(runs) > 1 {
		total -= len(runs[0])
		runs = runs[1:]
	}
	trimmed := bytes.Join(runs, nil)
	if !bytes.Equal(trimmed, data) {
		_ = os.WriteFile(path, trimmed, 0644)
	}
}

func splitRuns(data []byte, sentinel string) [][]byte {
	lines := bytes.SplitAfter(data, []byte("\n"))
	sentinelBytes := []byte(sentinel)
	var runs [][]byte
	var cur []byte
	for _, line := range lines {
		if bytes.Contains(line, sentinelBytes) && len(cur) > 0 {
			runs = append(runs, cur)
			cur = append([]byte(nil), line...)
		} else {
			cur = append(cur, line...)
		}
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	return runs
}
