// Package cli implements the commitcoach subcommands. The internal packages
// return errors; only this package prints messages and picks exit codes.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const (
	// defaultHookTimeout bounds a whole post-commit run, so a hung git never
	// blocks the terminal after a commit.
	defaultHookTimeout = 30 * time.Second
	// defaultExportTimeout bounds a manual export.
	defaultExportTimeout = 2 * time.Minute
	// setupTimeout bounds init, status and uninstall.
	setupTimeout = 30 * time.Second
)

const usageText = `commitcoach records Git commits as structured JSON snapshots.

Usage:
  commitcoach init                  install the post-commit hook in this repository
  commitcoach status                show the hook and snapshot setup of this repository
  commitcoach uninstall             remove the hook that commitcoach installed
  commitcoach export [--commit REV] [--output PATH|-]
                                    write the snapshot of a commit (default HEAD) as JSON
  commitcoach hook post-commit      run by the post-commit hook (internal)

Run "commitcoach <command> -h" for the options of a command.
`

// Run runs the command line args (without the program name) and returns the
// process exit code. JSON goes to stdout only from export; everything else,
// including all diagnostics of export and hook, goes to stderr, except the
// reports of init, status and uninstall, which go to stdout.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr)
	case "export":
		return runExport(args[1:], stdout, stderr)
	case "hook":
		return runHook(args[1:], stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	default:
		fmt.Fprintf(stderr, "commitcoach: unknown command %q\n\n%s", args[0], usageText)
		return exitUsage
	}
}

func newFlagSet(name, summary string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("commitcoach "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: commitcoach %s\n\n%s\n", name, summary)
		if hasFlags(fs) {
			fmt.Fprintln(stderr, "\nOptions:")
			fs.PrintDefaults()
		}
	}
	return fs
}

func hasFlags(fs *flag.FlagSet) bool {
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	return n > 0
}

// parseFlags parses args. When ok is false the command must return code.
func parseFlags(fs *flag.FlagSet, args []string) (code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK, false
		}
		return exitUsage, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		fs.Usage()
		return exitUsage, false
	}
	return exitOK, true
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "commitcoach: error: %v\n", err)
	return exitError
}
