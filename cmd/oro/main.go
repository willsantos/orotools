// Command oro is the Orotools CLI entrypoint.
package main

import (
	"os"

	"oroborus.dev/orotools/internal/cli"
)

// version is overridable at build time via ldflags:
//
//	go build -ldflags="-X main.version=v0.1.0" ./cmd/oro
var version = "dev"

func main() {
	if err := cli.Run(version, os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
