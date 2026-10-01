package cli

import (
	"fmt"
	"io"
	"os"
)

// blockBanner is the first line the pre-push hook shows when it blocks a
// push: the character "Ponta" saying it will not let the push through.
const blockBanner = "絶対に逃さないポン"

// printBlockBanner writes blockBanner in bold red when w is a terminal, and
// as plain text otherwise (a GUI client, a pipe, or NO_COLOR set), so that
// escape codes never show up as garbage.
func printBlockBanner(w io.Writer) {
	if colorEnabled(w) {
		fmt.Fprintf(w, "\x1b[1;31m%s\x1b[0m\n", blockBanner)
		return
	}
	fmt.Fprintln(w, blockBanner)
}

// colorEnabled reports whether w is a terminal and NO_COLOR
// (https://no-color.org) is not set.
func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
