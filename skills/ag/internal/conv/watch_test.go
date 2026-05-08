package conv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchFileStopsOnDeletion(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "events.jsonl")

		// Write some initial content so the first read sets offset > 0
	os.WriteFile(fpath, []byte(`{"type":"agent_start"}`+"\n"), 0644)

	stopCh := make(chan struct{})
	resultCh := make(chan error, 1)

	go func() {
		err := WatchFile(fpath, 50*time.Millisecond, stopCh)
		resultCh <- err
	}()

	// Let it read once to set offset > 0
	time.Sleep(150 * time.Millisecond)

	// Delete the file
	os.Remove(fpath)

	// WatchFile should return shortly after
	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("expected nil error on file deletion, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchFile did not return after file deletion")
		close(stopCh)
	}
}

func TestWatchFileStopsOnStopChannel(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "events.jsonl")
	os.WriteFile(fpath, []byte(""), 0644)

	stopCh := make(chan struct{})
	resultCh := make(chan error, 1)

	go func() {
		err := WatchFile(fpath, 50*time.Millisecond, stopCh)
		resultCh <- err
	}()

	// Close stop channel
	close(stopCh)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchFile did not return after stop channel closed")
	}
}