package stream

import (
	"fmt"
	"image"
	"os"
	"strings"
)

func ReadNativeResolution(sysPath string) image.Point {
	data, err := os.ReadFile(sysPath)
	if err != nil {
		return image.Point{X: 1280, Y: 720}
	}
	s := strings.TrimSpace(string(data))
	var w, h int
	if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return image.Point{X: 1280, Y: 720}
	}
	return image.Point{X: w, Y: h}
}
