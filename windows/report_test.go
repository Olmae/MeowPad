package main

import "testing"

func TestWindowsHIDReport(t *testing.T) {
	want := [33]byte{0, 0xAB, 'M', 'P', 1, 42, 1}
	if got := volumeReport(0.419, true); got != want {
		t.Fatalf("report = %v, want %v", got, want)
	}
	if got := volumeReport(1.01, false); got[5] != 100 || got[6] != 0 {
		t.Fatalf("out of range report = %v", got)
	}
	if got := volumeReport(-0.01, false); got[5] != 0 {
		t.Fatalf("negative level encoded as %d", got[5])
	}
}
