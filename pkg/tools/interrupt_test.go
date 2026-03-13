package tools

import (
	"sync"
	"testing"
)

func TestDefaultInterruptManager_ConcurrentRegistration(t *testing.T) {
	mgr := NewDefaultInterruptManager()
	
	var wg sync.WaitGroup
	numGoroutines := 10
	results := make([]string, numGoroutines)
	
	// Concurrently register interrupt files
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := GenerateInterruptFilePath()
			id := mgr.RegisterInterruptFile(path)
			results[idx] = id
		}(i)
	}
	
	wg.Wait()
	
	// All IDs should be unique
	ids := make(map[string]bool)
	for _, id := range results {
		if id == "" {
			t.Error("Got empty ID")
		}
		if ids[id] {
			t.Errorf("Duplicate ID: %s", id)
		}
		ids[id] = true
	}
	
	// Should have all files registered
	files := mgr.GetAllInterruptFiles()
	if len(files) != numGoroutines {
		t.Errorf("Expected %d files, got %d", numGoroutines, len(files))
	}
}

func TestDefaultInterruptManager_Unregister(t *testing.T) {
	mgr := NewDefaultInterruptManager()
	
	id := mgr.RegisterInterruptFile("/tmp/test-interrupt")
	
	files := mgr.GetAllInterruptFiles()
	if len(files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(files))
	}
	
	mgr.UnregisterInterruptFile(id)
	
	files = mgr.GetAllInterruptFiles()
	if len(files) != 0 {
		t.Errorf("Expected 0 files after unregister, got %d", len(files))
	}
}

func TestIsSubagentWaitCommand(t *testing.T) {
	tests := []struct {
		command  string
		expected bool
	}{
		// Should match - direct invocations
		{"~/.ai/skills/subagent/bin/subagent_wait.sh abc123", true},
		{"/home/user/.ai/skills/subagent/bin/subagent_wait.sh abc123", true},
		{"subagent_wait abc123", true},
		{"subagent_wait.sh abc123", true},
		{"./subagent_wait.sh abc123", true},
		{"/usr/local/bin/subagent_wait abc123", true},
		
		// Should match - with env vars
		{"PATH=/usr/bin subagent_wait.sh abc123", true},
		{"DEBUG=1 ~/.ai/skills/subagent/bin/subagent_wait.sh abc123", true},
		
		// Should NOT match - false positives (the main issue we're fixing)
		{"echo \"subagent_wait\"", false},
		{"echo subagent_wait", false},
		{"echo subagent_wait.sh", false},
		{"grep subagent_wait file.txt", false},
		{"cat file | grep subagent_wait", false},
		{"# subagent_wait command", false},
		{"# using subagent_wait.sh to monitor", false},
		{"ls -la", false},
		{"bash script.sh # contains subagent_wait", false},
		{"cat ~/.ai/skills/subagent/bin/subagent_wait.sh", false},  // cat, not executing
		{"less subagent_wait.sh", false},
		{"vim subagent_wait.sh", false},
		{"chmod +x subagent_wait.sh", false},
		
		// Edge cases
		{"", false},
		{"   ", false},
		{"| subagent_wait", false},  // After pipe, not first command
		{"echo hi | subagent_wait", false},  // After pipe
		{"subagent_wait.sh", true},  // No args, still valid
	}
	
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			result := isSubagentWaitCommand(tt.command)
			if result != tt.expected {
				t.Errorf("isSubagentWaitCommand(%q) = %v, want %v", tt.command, result, tt.expected)
			}
		})
	}
}

func TestExtractFirstCommandToken(t *testing.T) {
	tests := []struct {
		command  string
		expected string
	}{
		// Simple commands
		{"ls -la", "ls"},
		{"cat file.txt", "cat"},
		
		// With paths
		{"~/bin/script.sh", "~/bin/script.sh"},
		{"/usr/bin/cat file", "/usr/bin/cat"},
		{"./script.sh arg", "./script.sh"},
		
		// With env vars
		{"PATH=/usr/bin ls", "ls"},
		{"DEBUG=1 VERBOSE=1 cmd", "cmd"},
		{"VAR=\"value with space\" cmd", "cmd"},
		
		// Quoted commands
		{"\"my script.sh\" arg", "my script.sh"},
		{"'~/my script.sh' arg", "~/my script.sh"},
		
		// Edge cases
		{"", ""},
		{"   ", ""},
		{"# comment", ""},
		{"| pipe", ""},
		{"$(cmd)", ""},
		
		// Unclosed quotes - should return empty to avoid infinite loop
		{"VAR=\"unclosed cmd", ""},
		{"FOO='bar cmd", ""},
		{"A=\"b cmd", ""},
	}
	
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			result := extractFirstCommandToken(tt.command)
			if result != tt.expected {
				t.Errorf("extractFirstCommandToken(%q) = %q, want %q", tt.command, result, tt.expected)
			}
		})
	}
}