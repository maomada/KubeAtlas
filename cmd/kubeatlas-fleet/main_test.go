package main

import "testing"

func TestIsNonLoopback(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1:9291":  false,
		"[::1]:9291":      false,
		"localhost:9291":  false,
		"0.0.0.0:9291":    true,
		":9291":           true,
		"192.0.2.10:9291": true,
		"invalid":         true,
	} {
		if got := isNonLoopback(address); got != want {
			t.Errorf("isNonLoopback(%q) = %v, want %v", address, got, want)
		}
	}
}
