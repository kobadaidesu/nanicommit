// Command commitcoach records Git commits as structured JSON snapshots.
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
