package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// noBrowserEnv turns off opening the browser after a commit when it is set
// to anything non-empty (for CI, tests, or people who do not want it).
const noBrowserEnv = "NANICOMMIT_NO_BROWSER"

// openTalk opens the page where the user looks back on the commit with
// ぽんた. It never fails the hook: when the browser cannot be opened, the
// URL is printed so it can be opened by hand.
func openTalk(stderr io.Writer, url string) {
	if url == "" {
		return
	}
	if os.Getenv(noBrowserEnv) != "" {
		printTalk(stderr, url)
		return
	}
	fmt.Fprintf(stderr, "nanicommit: opening %s\n", url)
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(stderr, "nanicommit: warning: could not open the browser: %v; open it yourself: %s\n", err, url)
	}
}

func printTalk(stderr io.Writer, url string) {
	if url != "" {
		fmt.Fprintf(stderr, "nanicommit: look back on it with ぽんた: %s\n", url)
	}
}

// openBrowser starts the default browser and does not wait for it, so the
// commit finishes right away. The child gets no stdout or stderr, so git
// does not wait for it either.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
