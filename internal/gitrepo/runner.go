// Package gitrepo locates a Git repository and reads commits and diffs from
// it by running the user's git executable. It never re-implements Git's data
// structures and never modifies the repository.
//
// Every git invocation goes through Runner: the program name and each
// argument are handed to os/exec separately (never through a shell), color,
// pagers, external diff drivers and textconv filters are disabled, and a
// context bounds how long git may run.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// gitGlobalArgs are placed before the subcommand of every git invocation.
var gitGlobalArgs = []string{
	"--no-pager",
	"-c", "color.ui=never",
	// Keep non-ASCII paths readable in patch headers. Paths that contain
	// control characters, double quotes or backslashes are still C-quoted by
	// git; diff.go relies on that.
	"-c", "core.quotePath=false",
	// Never start a file system monitor hook or daemon from here.
	"-c", "core.fsmonitor=false",
}

// gitEnv is appended to the inherited environment of every git invocation.
// The inherited GIT_DIR, GIT_INDEX_FILE, ... are kept on purpose: inside a
// hook they point git at the repository that is committing.
var gitEnv = []string{
	"GIT_TERMINAL_PROMPT=0", // never prompt for credentials
	"GIT_OPTIONAL_LOCKS=0",  // we only read; do not take optional locks
	"GIT_NO_LAZY_FETCH=1",   // do not fetch missing objects of partial clones (Git 2.44+)
}

const (
	maxStderrBytes = 16 << 10
	// waitDelay bounds how long Wait blocks for pipes that a killed git (or
	// one of its children) left open.
	waitDelay = 2 * time.Second
)

// Runner runs git commands in a directory.
type Runner struct {
	// Dir is the working directory of git; "" means the current directory.
	Dir string
}

func (r *Runner) command(ctx context.Context, env, args []string) *exec.Cmd {
	full := make([]string, 0, len(gitGlobalArgs)+len(args))
	full = append(full, gitGlobalArgs...)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), gitEnv...), env...)
	cmd.WaitDelay = waitDelay
	return cmd
}

// Output runs git and returns its standard output. It is meant for commands
// with small output; use Start for diffs.
func (r *Runner) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := r.command(ctx, nil, args)
	var stdout bytes.Buffer
	stderr := &boundedBuffer{max: maxStderrBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, newError(ctx, args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// Stream is a running git command whose standard output is read
// incrementally.
type Stream struct {
	ctx    context.Context // the caller's context, used to report timeouts
	cancel context.CancelFunc
	args   []string
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *boundedBuffer
}

// Start starts git and returns a Stream for reading its standard output.
// env holds extra environment variables for this command only. The caller
// must end the stream with Finish or Abort.
func (r *Runner) Start(ctx context.Context, env []string, args ...string) (*Stream, error) {
	cctx, cancel := context.WithCancel(ctx)
	cmd := r.command(cctx, env, args)
	stderr := &boundedBuffer{max: maxStderrBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, newError(ctx, args, err, "")
	}
	// If the context ends while the reader is blocked (a hung git, or a child
	// process that keeps the pipe open), closing the pipe unblocks it.
	context.AfterFunc(cctx, func() { stdout.Close() })
	return &Stream{ctx: ctx, cancel: cancel, args: args, cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func (s *Stream) Read(p []byte) (int, error) { return s.stdout.Read(p) }

// Finish closes the output and waits for git to exit. It returns an *Error
// when git failed, or an error wrapping context.DeadlineExceeded when the
// context expired. If the caller stopped reading early, git is terminated by
// SIGPIPE and Finish reports that as a failure; use Abort in that case.
func (s *Stream) Finish() error {
	s.stdout.Close()
	err := s.cmd.Wait()
	s.cancel()
	if err != nil {
		return newError(s.ctx, s.args, err, s.stderr.String())
	}
	return nil
}

// Abort kills git and waits for it. It is used when the caller already has
// all the output it needs, so git's exit status is irrelevant.
func (s *Stream) Abort() {
	s.cancel()
	_ = s.cmd.Wait()
}

// Stderr returns what git wrote to standard error (possibly shortened).
// It is complete only after Finish or Abort.
func (s *Stream) Stderr() string { return s.stderr.String() }

// Error describes a git command that did not succeed.
type Error struct {
	Args     []string // subcommand and its arguments
	ExitCode int      // exit status, or -1 if git did not exit normally
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("git")
	if len(e.Args) > 0 {
		b.WriteString(" " + e.Args[0])
	}
	if e.ExitCode >= 0 {
		fmt.Fprintf(&b, " exited with status %d", e.ExitCode)
	} else {
		fmt.Fprintf(&b, " failed: %v", e.Err)
	}
	if s := strings.TrimSpace(e.Stderr); s != "" {
		b.WriteString(": " + s)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// exitedWithFailure reports whether err is git exiting on its own with a
// non-zero status (as opposed to being killed or timing out).
func exitedWithFailure(err error) bool {
	var gerr *Error
	return errors.As(err, &gerr) && gerr.ExitCode > 0
}

// hasExitCode reports whether err is git exiting with the given status.
func hasExitCode(err error, code int) bool {
	var gerr *Error
	return errors.As(err, &gerr) && gerr.ExitCode == code
}

func newError(ctx context.Context, args []string, err error, stderr string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return fmt.Errorf("git %s timed out: %w", sub, ctxErr)
		}
		return fmt.Errorf("git %s was canceled: %w", sub, ctxErr)
	}
	code := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	return &Error{Args: args, ExitCode: code, Stderr: stderr, Err: err}
}

// boundedBuffer keeps the first max bytes written to it.
type boundedBuffer struct {
	mu      sync.Mutex
	buf     []byte
	max     int
	dropped bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - len(b.buf)
	if len(p) > room {
		b.dropped = true
		if room > 0 {
			b.buf = append(b.buf, p[:room]...)
		}
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := string(b.buf)
	if b.dropped {
		s += "\n[stderr shortened]"
	}
	return s
}
