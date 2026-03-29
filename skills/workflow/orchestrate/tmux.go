package orchestrate

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// TmuxManager manages tmux sessions for workers
type TmuxManager struct {
	sessionName string
	cwd         string
}

// NewTmuxManager creates a new tmux manager
func NewTmuxManager(sessionName, cwd string) *TmuxManager {
	return &TmuxManager{
		sessionName: sessionName,
		cwd:         cwd,
	}
}

// SessionExists checks if tmux session exists
func (t *TmuxManager) SessionExists() bool {
	cmd := exec.Command("tmux", "has-session", "-t", t.sessionName)
	cmd.Env = os.Environ()
	cmd.Dir = t.cwd
	return cmd.Run() == nil
}

// CreateSession creates a new tmux session
func (t *TmuxManager) CreateSession() error {
	if t.SessionExists() {
		return nil
	}
	
	// Create detached session
	cmd := exec.Command("tmux", "new-session", "-d", "-s", t.sessionName, "-x", "200", "-y", "50")
	cmd.Env = os.Environ()
	cmd.Dir = t.cwd
	return cmd.Run()
}

// KillSession kills the tmux session
func (t *TmuxManager) KillSession() error {
	if !t.SessionExists() {
		return nil
	}
	cmd := exec.Command("tmux", "kill-session", "-t", t.sessionName)
	return cmd.Run()
}

// NewWindow creates a new window for a worker
func (t *TmuxManager) NewWindow(windowName string) error {
	// Check if window exists
	cmd := exec.Command("tmux", "list-windows", "-t", t.sessionName, "-F", "#{window_name}")
	output, err := cmd.Output()
	if err == nil && strings.Contains(string(output), windowName) {
		// Window exists, kill it first
		t.KillWindow(windowName)
	}
	
	// Create new window
	cmd = exec.Command("tmux", "new-window", "-t", t.sessionName, "-n", windowName)
	cmd.Env = os.Environ()
	cmd.Dir = t.cwd
	return cmd.Run()
}

// KillWindow kills a specific window
func (t *TmuxManager) KillWindow(windowName string) error {
	cmd := exec.Command("tmux", "kill-window", "-t", t.sessionName+":"+windowName)
	return cmd.Run()
}

// SendKeys sends keys to a window
func (t *TmuxManager) SendKeys(windowName, command string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", t.sessionName+":"+windowName, command, "Enter")
	cmd.Env = os.Environ()
	cmd.Dir = t.cwd
	return cmd.Run()
}

// CapturePane captures the pane content
func (t *TmuxManager) CapturePane(windowName string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", t.sessionName+":"+windowName, "-p")
	cmd.Env = os.Environ()
	cmd.Dir = t.cwd
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// StartWorker starts a worker in a tmux window using headless mode
func (t *TmuxManager) StartWorker(workerName, taskID, claimToken string) error {
	if err := t.NewWindow(workerName); err != nil {
		return fmt.Errorf("failed to create window: %w", err)
	}
	
	// Read inbox content for the prompt
	inboxPath := fmt.Sprintf("%s/.ai/team/workers/%s/inbox.md", t.cwd, workerName)
	
	// Use headless mode with inbox content as prompt
	// The ai command will read the task from inbox and execute it
	commands := []string{
		fmt.Sprintf("cd %s", t.cwd),
		fmt.Sprintf("export AI_TEAM_WORKER=%s", workerName),
		fmt.Sprintf("export AI_TEAM_TASK_ID=%s", taskID),
		fmt.Sprintf("export AI_TEAM_CLAIM_TOKEN=%s", claimToken),
		fmt.Sprintf("ai --mode headless --timeout 60m \"$(cat %s)\"", inboxPath),
	}
	
	for _, cmd := range commands {
		if err := t.SendKeys(workerName, cmd); err != nil {
			return fmt.Errorf("failed to send command: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	
	return nil
}

// ListWindows lists all windows in the session
func (t *TmuxManager) ListWindows() ([]string, error) {
	cmd := exec.Command("tmux", "list-windows", "-t", t.sessionName, "-F", "#{window_name}")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	
	var windows []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			windows = append(windows, line)
		}
	}
	return windows, nil
}

// IsTmuxAvailable checks if tmux is available
func IsTmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}