package main


import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: evolve-mini <command> [args]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  snapshot extract <sessions-dir> <output-dir>  Extract snapshots from session files")
		fmt.Fprintln(os.Stderr, "  snapshot list <suite-dir>                     List snapshots in a suite")
		fmt.Fprintln(os.Stderr, "  snapshot describe <suite-dir>                 LLM-generate descriptions for snapshots")
		fmt.Fprintln(os.Stderr, "  score <worker-binary> <suite-dir>             Score a worker against a snapshot suite")
		fmt.Fprintln(os.Stderr, "  run <suite-dir> [max-generations]             Run the full evolution loop")
		fmt.Fprintln(os.Stderr, "  status <evolve-dir>                           Show evolution status")
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "snapshot":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: evolve-mini snapshot <extract|list|describe> ...")
			os.Exit(1)
		}
		sub := args[0]
		subArgs := args[1:]
		switch sub {
		case "extract":
			if len(subArgs) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: evolve-mini snapshot extract <sessions-dir> <output-dir>")
				os.Exit(1)
			}
			if err := runSnapshotExtract(subArgs[0], subArgs[1]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		case "list":
			if len(subArgs) < 1 {
				fmt.Fprintln(os.Stderr, "Usage: evolve-mini snapshot list <suite-dir>")
				os.Exit(1)
			}
			if err := runSnapshotList(subArgs[0]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Fprintf(os.Stderr, "Unknown snapshot subcommand: %s\n", sub)
			os.Exit(1)
		}

	case "score":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: evolve-mini score <worker-binary> <suite-dir>")
			os.Exit(1)
		}
		if err := runScore(args[0], args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "run":
		maxGen := 20
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: evolve-mini run <suite-dir> [max-generations]")
			os.Exit(1)
		}
		if len(args) >= 2 {
			fmt.Sscanf(args[1], "%d", &maxGen)
		}
		fmt.Fprintln(os.Stderr, "Error: run command not implemented (Phase 4)")
		os.Exit(1)

	case "status":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: evolve-mini status <evolve-dir>")
			os.Exit(1)
		}
		if err := runStatus(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
