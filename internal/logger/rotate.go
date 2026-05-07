package logger

import (
	"bytes"
	"os"
	"strings"
)

const (
	logMaxBytes = 10 * 1024 * 1024
	logMaxRuns  = 5
)

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
	var runs [][]byte
	var cur []byte
	for _, line := range lines {
		if strings.Contains(string(line), sentinel) && len(cur) > 0 {
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
