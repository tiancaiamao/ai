package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	if err := AtomicWrite(path, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != `{"hello":"world"}` {
		t.Fatalf("unexpected content: %s", data)
	}
}

func TestAtomicWriteOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	AtomicWrite(path, []byte("first"))
	AtomicWrite(path, []byte("second"))

	data, _ := os.ReadFile(path)
	if string(data) != "second" {
		t.Fatalf("expected 'second', got '%s'", data)
	}
}

func TestAtomicWriteJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	type Foo struct{ A string `json:"a"` }
	if err := AtomicWriteJSON(path, Foo{A: "bar"}); err != nil {
		t.Fatalf("AtomicWriteJSON: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "{\n  \"a\": \"bar\"\n}" {
		t.Fatalf("unexpected: %s", data)
	}
}

func TestReadStatus(t *testing.T) {
	tmpDir := t.TempDir()

	// No status file
	if s := ReadStatus(tmpDir); s != "unknown" {
		t.Fatalf("expected unknown, got %s", s)
	}

	// With status file
	os.WriteFile(filepath.Join(tmpDir, "status"), []byte("running\n"), 0644)
	if s := ReadStatus(tmpDir); s != "running" {
		t.Fatalf("expected running, got %s", s)
	}
}

func TestInit(t *testing.T) {
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(origDir)

	// Reset BaseDir to default in case another test changed it
	BaseDir = ".ag"

	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, dir := range []string{"agents", "channels", "tasks"} {
		if _, err := os.Stat(filepath.Join(BaseDir, dir)); os.IsNotExist(err) {
			t.Errorf("%s dir not created", dir)
		}
	}
}