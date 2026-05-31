package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"os"
	"time"

	stream "github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

// benchWriter implements io.Writer and records benchmark metrics while
// discarding the actual data.
type benchWriter struct {
	start       time.Time
	firstByteMs int64
	totalBytes  int64
	gotFirst    bool
}

func (w *benchWriter) Write(p []byte) (int, error) {
	if !w.gotFirst {
		w.firstByteMs = time.Since(w.start).Milliseconds()
		w.gotFirst = true
	}
	w.totalBytes += int64(len(p))
	return len(p), nil
}

// result holds the outcome of a single encoder benchmark run.
type result struct {
	name           string
	firstByteMs    int64
	throughputKbps float64
	totalBytes     int64
	actualDuration time.Duration
	err            error
}

func runBench(name string, enc stream.Encoder, quality string, duration time.Duration) result {
	startTime := time.Now()
	w := &benchWriter{start: startTime, firstByteMs: -1}

	if err := enc.Start(w); err != nil {
		return result{name: name, firstByteMs: -1, err: err}
	}

	time.Sleep(duration)
	enc.Stop()
	waitErr := enc.Wait()

	actualDuration := time.Since(startTime)

	firstByteMs := int64(-1)
	if w.gotFirst {
		firstByteMs = w.firstByteMs
	}

	var throughputKbps float64
	if actualDuration.Seconds() > 0 {
		throughputKbps = float64(w.totalBytes*8) / actualDuration.Seconds() / 1000
	}

	return result{
		name:           name,
		firstByteMs:    firstByteMs,
		throughputKbps: throughputKbps,
		totalBytes:     w.totalBytes,
		actualDuration: actualDuration,
		err:            waitErr,
	}
}

func printResult(r result) {
	fmt.Printf("Encoder:     %s\n", r.name)
	fmt.Printf("Duration:    %s\n", r.actualDuration.Round(time.Millisecond))
	if r.firstByteMs >= 0 {
		fmt.Printf("First byte:  %dms\n", r.firstByteMs)
	} else {
		fmt.Printf("First byte:  n/a\n")
	}
	fmt.Printf("Throughput:  %.1f kbps\n", r.throughputKbps)
	fmt.Printf("Total bytes: %d\n", r.totalBytes)
	if r.err != nil {
		fmt.Printf("Error:       %v\n", r.err)
	}
}

func main() {
	encoderFlag := flag.String("encoder", "both", "which encoder(s) to benchmark: cedar, ffmpeg, or both")
	durationFlag := flag.Duration("duration", 10*time.Second, "how long to run each encoder")
	qualityFlag := flag.String("quality", "high", "quality preset: low, medium, high, ultra")
	flag.Parse()

	native := stream.ReadNativeResolution("/sys/class/graphics/fb0/modes")
	if native.X == 0 || native.Y == 0 {
		native = image.Point{X: 480, Y: 272}
	}

	cfg := stream.FFmpegConfig{
		Quality:    *qualityFlag,
		Audio:      false,
		Resolution: native,
	}

	runCedar := *encoderFlag == "cedar" || *encoderFlag == "both"
	runFFmpeg := *encoderFlag == "ffmpeg" || *encoderFlag == "both"

	var cedarResult, ffmpegResult *result
	printed := false

	if runCedar {
		enc, err := stream.NewCedarEncoder(cfg)
		if errors.Is(err, stream.ErrNotSupported) {
			fmt.Println("Cedar: not available on this platform")
			if *encoderFlag == "cedar" {
				fmt.Fprintf(os.Stderr, "cedar: %v\n", err)
				os.Exit(1)
			}
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "cedar: %v\n", err)
			if *encoderFlag == "cedar" {
				os.Exit(1)
			}
		} else {
			fmt.Printf("Benchmarking cedar (%s quality, %s)...\n\n", *qualityFlag, *durationFlag)
			r := runBench("cedar", enc, *qualityFlag, *durationFlag)
			cedarResult = &r
		}
	}

	if runFFmpeg {
		enc := stream.NewFFmpegEncoder(cfg)
		fmt.Printf("Benchmarking ffmpeg (%s quality, %s)...\n\n", *qualityFlag, *durationFlag)
		r := runBench("ffmpeg", enc, *qualityFlag, *durationFlag)
		ffmpegResult = &r
	}

	if cedarResult != nil {
		fmt.Printf("Quality:     %s\n", *qualityFlag)
		printResult(*cedarResult)
		printed = true
	}

	if ffmpegResult != nil {
		if printed {
			fmt.Println("---")
		}
		fmt.Printf("Quality:     %s\n", *qualityFlag)
		printResult(*ffmpegResult)
	}
}
