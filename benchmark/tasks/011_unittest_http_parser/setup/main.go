package main

import (
	"flag"
	"fmt"
	"httpfileparser/httpfile"
	"net/http/httputil"
	"os"
)

func main() {
	overrides := flag.String("overrides", "", "Path to JSON overrides file (optional)")
	keepAlive := flag.Bool("keepalive", false, "Add Connection: keep-alive header")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <file.http>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	httpFilePath := args[0]

	requests, err := httpfile.HTTPFileParser(httpFilePath, *overrides, *keepAlive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing HTTP file: %v\n", err)
		os.Exit(1)
	}

	if len(requests) == 0 {
		fmt.Println("No requests found in file.")
		return
	}

	for i, req := range requests {
		if i > 0 {
			fmt.Println("\n###")
		}
		fmt.Printf("Request %d:\n", i+1)
		fmt.Println("---")

		dump, err := httputil.DumpRequest(&req, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error dumping request %d: %v\n", i+1, err)
			continue
		}
		fmt.Println(string(dump))
	}
}
