package main

import (
	"fmt"
	"os"

	"github.com/faizmokh/skmr/internal/cli"
	"github.com/faizmokh/skmr/internal/terminal"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := cli.New(cli.BuildInfo{Version: version, Commit: commit, Date: date}).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", terminal.Safe(err.Error()))
		os.Exit(1)
	}
}
