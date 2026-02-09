package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/sminez/ad/win/pkg/ad"
	"github.com/sminez/ad/win/pkg/repl"
	"github.com/tiancaiamao/ai/internal/winai"
)

const (
	sendPrefix = ";; "
)

func main() {
	windowName := flag.String("name", "", "window name (default +ai)")
	debug := flag.Bool("debug", false, "enable debug logging")
	aiCmd := flag.String("ai-cmd", "ai", "ai executable")
	aiArgs := flag.String("ai-args", "", "extra args for ai (space-separated)")
	flag.Parse()

	if *debug {
		log.SetFlags(log.Ltime | log.Lshortfile)
		log.Println("Starting win-ai REPL with debug logging")
	}

	client, err := ad.NewClient()
	if err != nil {
		log.Fatalf("unable to connect to ad: %v", err)
	}
	defer func() {
		if *debug {
			log.Println("Closing client connection")
		}
		client.Close()
	}()

	if *debug {
		log.Println("Connected to ad successfully")
	}

	extraArgs := strings.Fields(*aiArgs)
	interpreter := winai.NewAiInterpreter(*aiCmd, extraArgs, *debug)
	interpreter.SetAdClient(client) // Set ad client for minibuffer interactions
	defer interpreter.Stop()

	name := *windowName
	if name == "" {
		name = "+ai"
	}

	config := repl.Config{
		Prompt:     "",
		WindowName: name,
		WelcomeMessage: `# Ai REPL
#
# Use send-to-win to send prompts (prefix ";; ").
# Controls: use win-ctl or send /command via send-to-win.
#
`,
		SendPrefix:            sendPrefix,
		InputPrefix:           "",
		EchoSendInput:         true,
		EnableKeyboardExecute: false,
		EnableExecute:         false,
		Debug:                 *debug,
		LogPath:               "/tmp/ai-repl.log",
	}

	handler, err := repl.NewHandler(config, client, interpreter)
	if err != nil {
		log.Fatalf("unable to create REPL handler: %v", err)
	}

	if *debug {
		log.Println("Starting REPL...")
	}

	if err := handler.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "REPL error: %v\n", err)
		os.Exit(1)
	}
}
