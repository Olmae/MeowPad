package main

import "math"

func volumeReport(level float32, muted bool) [33]byte {
	percent := byte(math.Round(math.Max(0, math.Min(1, float64(level))) * 100))
	report := [33]byte{0, 0xAB, 'M', 'P', 1, percent}
	if muted {
		report[6] = 1
	}
	return report
}
