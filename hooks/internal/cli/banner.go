package cli

import (
	"fmt"
	"io"
	"os"
)

// blockBanner is what the pre-push hook shows first when it blocks a push:
// the character "Ponta" saying it will not let the push through, repeated
// blockBannerLines times, one per line.
const (
	blockBanner      = "絶対に逃さないポン"
	blockBannerLines = 9
)

// printBlockBanner writes the banner lines in bold red when w is a terminal,
// and as plain text otherwise (a GUI client, a pipe, or NO_COLOR set), so that
// escape codes never show up as garbage.
func printBlockBanner(w io.Writer) {
	format := "%s\n"
	if colorEnabled(w) {
		format = "\x1b[1;31m%s\x1b[0m\n"
	}
	for range blockBannerLines {
		fmt.Fprintf(w, format, blockBanner)
	}
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
