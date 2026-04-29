package main

import (
	"os"
	"testing"
)

func TestProcessAlive(t *testing.T) {
	// Current process should be alive.
	if !processAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}

	// A PID that almost certainly doesn't exist.
	if processAlive(999999999) {
		t.Error("non-existent PID should not be alive")
	}
}