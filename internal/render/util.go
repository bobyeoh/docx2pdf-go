package render

import (
	"os"
	"strconv"
	"strings"
)

func twipsToPt(t int) float64 {
	if t == 0 {
		return 0
	}
	return float64(t) / 20.0
}

func parseHexColor(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return uint8(r), uint8(g), uint8(b)
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// FileExists is a tiny helper kept here so the CLI can validate paths
// without pulling os into main.
func FileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
