package stream

import (
	"fmt"
	"image"
	"os"
	"strings"
)

// ReadNativeResolution reads the physical display resolution from the Linux
// framebuffer sysfs modes file (normally /sys/class/graphics/fb0/modes).
// The file contains lines like "U:1024x768p-60"; only the first line is used.
// Note: virtual_size gives the allocated framebuffer size (multiple buffer
// pages for page-flipping) and must NOT be used for the display resolution.
// Falls back to 1280x720 on any read or parse error.
func ReadNativeResolution(sysPath string) image.Point {
	data, err := os.ReadFile(sysPath)
	if err != nil {
		return image.Point{X: 1280, Y: 720}
	}
	// Take only the first line in case the file has multiple modes.
	line := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
	var w, h int
	if _, err := fmt.Sscanf(line, "U:%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return image.Point{X: 1280, Y: 720}
	}
	return image.Point{X: w, Y: h}
}
