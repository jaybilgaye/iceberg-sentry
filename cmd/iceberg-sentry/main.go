package main

import (
	"fmt"
	"os"

	"github.com/jaybilgaye/iceberg-sentry/internal/cli"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
)

// Populated by the release build via -ldflags (see Makefile / .goreleaser.yaml).
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	code, err := cli.Execute(os.Args[1:], cli.BuildInfo{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	if code == 0 && err != nil {
		code = exitcode.ConfigError
	}
	os.Exit(code)
}
