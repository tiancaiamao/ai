package cmd

import "fmt"

// RunBridge is kept only for backward CLI compatibility.
// The bridge runtime has been removed in favor of ai serve/send/watch.
func RunBridge(id string) error {
	return fmt.Errorf("legacy bridge mode has been removed; use 'ag agent spawn' with backend=ai")
}
