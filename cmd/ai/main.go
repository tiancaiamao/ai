package main

import (
	"os"

	"github.com/tiancaiamao/ai/pkg/cli"
)

func main() {
	cli.Dispatch(os.Args[0])
}
