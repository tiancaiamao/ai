package channel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/genius/ag/internal/storage"
)

// Create creates a new named channel directory.
func Create(name string) error {
	if name == "" {
		return fmt.Errorf("channel name is required")
	}
	chDir := storage.ChannelDir(name)
	if storage.Exists(chDir) {
		return fmt.Errorf("channel already exists: %s", name)
	}
	return os.MkdirAll(chDir, 0755)
}

var msgSeq atomic.Int64

// Send appends a message to a channel or an agent's inbox.
// target is either a channel name or an agent id.
func Send(target string, data []byte, isFile bool) error {
	var queueDir string

	// Check if target is an agent
	agentDir := storage.AgentDir(target)
	if storage.Exists(agentDir) {
		queueDir = filepath.Join(agentDir, "inbox")
	} else {
		// Treat as channel — auto-create if needed
		queueDir = storage.ChannelDir(target)
		if err := os.MkdirAll(queueDir, 0755); err != nil {
			return fmt.Errorf("create channel %s: %w", target, err)
		}
	}

	if err := os.MkdirAll(queueDir, 0755); err != nil {
		return err
	}

	// Write atomically: temp file then rename
	tmp, err := os.CreateTemp(queueDir, ".send-tmp-")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	if isFile {
		// data contains a file path
		f, err := os.Open(string(data))
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("open file: %w", err)
		}
		defer f.Close()
		buf := make([]byte, 4096)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				tmp.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
	} else {
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return err
		}
	}
	tmp.Close()

	// Use atomic counter + pid + timestamp for unique naming
	seq := msgSeq.Add(1)
	finalName := filepath.Join(queueDir, fmt.Sprintf("%d-%d-%03d.msg",
		time.Now().UnixNano(), os.Getpid(), seq))

	if err := os.Rename(tmpName, finalName); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// Recv reads and removes the first (oldest) message from a channel or agent output.
// For headless agents that are done, it reads the output file.
// For channels, it reads the oldest .msg file (sorted by name).
func Recv(source string, wait bool, timeoutSec int, all bool) ([]byte, error) {
	// Check if source is an agent
	agentDir := storage.AgentDir(source)
	if storage.Exists(agentDir) {
		status := storage.ReadStatus(agentDir)

		// For done agents, return output directly
		if status == "done" {
			return os.ReadFile(filepath.Join(agentDir, "output"))
		}

		// For running/other agents, read from inbox
		queueDir := filepath.Join(agentDir, "inbox")
		return recvFromQueue(queueDir, wait, timeoutSec, all)
	}

	// Channel
	queueDir := storage.ChannelDir(source)
	if !storage.Exists(queueDir) {
		return nil, fmt.Errorf("no agent or channel found: %s", source)
	}
	return recvFromQueue(queueDir, wait, timeoutSec, all)
}

func recvFromQueue(queueDir string, wait bool, timeoutSec int, all bool) ([]byte, error) {
	if wait {
		deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
		for time.Now().Before(deadline) {
			msgs, _ := listMessages(queueDir)
			if len(msgs) > 0 {
				return readAndRemove(queueDir, msgs, all)
			}
			// Sleep 200ms between checks — responsive but not busy-wait
			time.Sleep(200 * time.Millisecond)
		}
		return nil, fmt.Errorf("timeout after %ds", timeoutSec)
	}

	msgs, err := listMessages(queueDir)
	if err != nil || len(msgs) == 0 {
		return nil, fmt.Errorf("no messages")
	}
	return readAndRemove(queueDir, msgs, all)
}

// listMessages returns .msg files sorted by name (oldest first).
// Since names contain timestamp+pid+seq, sorting gives FIFO order.
func listMessages(queueDir string) ([]string, error) {
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		return nil, err
	}
	var msgs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".msg") {
			msgs = append(msgs, e.Name())
		}
	}
	sort.Strings(msgs)
	return msgs, nil
}

func readAndRemove(queueDir string, msgs []string, all bool) ([]byte, error) {
	if all {
		var result []byte
		for _, m := range msgs {
			data, err := os.ReadFile(filepath.Join(queueDir, m))
			if err != nil {
				continue
			}
			result = append(result, data...)
			result = append(result, '\n')
			os.Remove(filepath.Join(queueDir, m))
		}
		return result, nil
	}

	// Read first (oldest) message
	data, err := os.ReadFile(filepath.Join(queueDir, msgs[0]))
	if err != nil {
		return nil, err
	}
	os.Remove(filepath.Join(queueDir, msgs[0]))
	return data, nil
}

// List returns all channels with their message counts.
func List() ([]struct {
	Name     string
	Messages int
}, error) {
	_, channelsDir, _ := storage.Paths()
	entries, err := os.ReadDir(channelsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []struct {
		Name     string
		Messages int
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		msgs, _ := listMessages(filepath.Join(channelsDir, name))
		result = append(result, struct {
			Name     string
			Messages int
		}{name, len(msgs)})
	}

	return result, nil
}

// Remove deletes a channel directory.
func Remove(name string) error {
	chDir := storage.ChannelDir(name)
	if !storage.Exists(chDir) {
		return fmt.Errorf("channel not found: %s", name)
	}
	return os.RemoveAll(chDir)
}