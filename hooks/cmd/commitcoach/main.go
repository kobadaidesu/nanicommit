// Command commitcoach turns Git commits into JSON for the learning backend.
//
// See README.md for usage.
package main

import (
	"os"

	"github.com/kobadaidesu/hook-test/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
