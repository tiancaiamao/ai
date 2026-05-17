package agent

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestIsProcessAlive_DetectsRunning tests that IsProcessAlive returns true for a running process.
func TestIsProcessAlive_DetectsRunning(t *testing.T) {
	// Start a long-lived process
	cmd := exec.Command("sleep", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	pid := cmd.Process.Pid
	if !IsProcessAlive(pid) {
		t.Errorf("IsProcessAlive(%d) = false, want true for running process", pid)
	}
}

// TestIsProcessAlive_DetectsExited tests that IsProcessAlive returns false for an exited process.
func TestIsProcessAlive_DetectsExited(t *testing.T) {
	// Start and immediately kill a process
	cmd := exec.Command("sleep", "0.01")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Process.Kill()
	cmd.Wait()

	// Wait briefly for the process to fully exit
	time.Sleep(100 * time.Millisecond)

	pid := cmd.Process.Pid
	if IsProcessAlive(pid) {
		t.Errorf("IsProcessAlive(%d) = true, want false for killed process", pid)
	}
}

// TestIsProcessAlive_NegativePID tests edge case.
func TestIsProcessAlive_NegativePID(t *testing.T) {
	if IsProcessAlive(-1) {
		t.Error("IsProcessAlive(-1) = true, want false")
	}
	if IsProcessAlive(0) {
		t.Error("IsProcessAlive(0) = true, want false")
	}
}

// TestIsProcessAlive_NonexistentPID tests a PID that definitely doesn't exist.
func TestIsProcessAlive_NonexistentPID(t *testing.T) {
	// PID 999999 is very unlikely to exist
	if IsProcessAlive(999999) {
		t.Error("IsProcessAlive(999999) = true, want false for nonexistent PID")
	}
}

// TestIsZombieFromPS tests the zombie detection via ps.
func TestIsZombieFromPS_NonexistentProcess(t *testing.T) {
	if isZombieFromPS(999999) {
		t.Error("isZombieFromPS(999999) = true, want false for nonexistent PID")
	}
}