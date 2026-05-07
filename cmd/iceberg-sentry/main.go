package main

import (
	"fmt"
	"os"

	"github.com/jaybilgaye/iceberg-sentry/internal/cli"
	"github.com/jaybilgaye/iceberg-sentry/internal/exitcode"
)

var version = "dev"

func main() {
	code, err := cli.Execute(os.Args[1:], version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	if code == 0 && err != nil {
		code = exitcode.ConfigError
	}
	os.Exit(code)
}
