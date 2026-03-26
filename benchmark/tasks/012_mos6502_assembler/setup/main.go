package main

import (
	"encoding/json"
	"fmt"
	"os"

	"asm/asm"
)

// TODO: Implement the MOS6502 assembler
// Read requirements.md for the full specification
// Use asm.OpcodeXXX for opcode definitions

func main() {
	debug := false
	filename := ""

	// Parse arguments
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-debug" {
			debug = true
		} else {
			filename = os.Args[i]
		}
	}

	if filename == "" {
		fmt.Println("Usage: asm [-debug] <filename.asm>")
		os.Exit(1)
	}

	// Read the source file
	source, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Assemble the source
	output, err := asm.Assemble(string(source))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Assembly error: %v\n", err)
		os.Exit(1)
	}

	// Output JSON
	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(jsonBytes))

	if debug {
		fmt.Fprintf(os.Stderr, "Debug: Assembled successfully\n")
	}
}