// Package hooks installs, inspects and removes the post-commit hook that
// runs "commitcoach hook post-commit".
//
// The hook is a thin shell script that only calls the commitcoach binary by
// absolute path. It carries a marker line and the binary path, so a hook
// written by commitcoach can be told apart from other hooks, and an edited
// one can be detected by regenerating the script and comparing bytes.
// commitcoach never changes Git configuration and never touches a hook it
// did not write or that has been edited since.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
)

// HookName is the only hook commitcoach installs.
const HookName = "post-commit"

const (
	markerLine = "# commitcoach-managed-hook: v1"
	exePrefix  = "# commitcoach-executable: "
	hookPerm   = fs.FileMode(0o755)
	// Hook files larger than this are not read; they cannot be ours.
	maxHookBytes = 1 << 20
)

// Script returns the hook script that runs exe, an absolute path.
func Script(exe string) []byte {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(markerLine + "\n")
	b.WriteString(exePrefix + strconv.Quote(exe) + "\n")
	b.WriteString(`#
# Created by "commitcoach init". Remove it with "commitcoach uninstall".
# If you edit this file, commitcoach will no longer update or remove it.
#
# The commit already exists when this hook runs. Recording its snapshot may
# fail, but that never undoes or blocks the commit, so this hook exits 0.
`)
	b.WriteString("commitcoach_bin=" + shellQuote(exe) + "\n")
	b.WriteString(`if [ -x "$commitcoach_bin" ]; then
	"$commitcoach_bin" hook post-commit ||
		printf 'commitcoach: warning: the commit was created, but its snapshot was not recorded (exit status %s)\n' "$?" >&2
else
	printf 'commitcoach: warning: the commit was created, but the commitcoach executable is missing: %s\n' "$commitcoach_bin" >&2
	printf 'commitcoach: build or install it again and rerun "commitcoach init", or delete this hook: %s\n' "$0" >&2
fi
exit 0
`)
	return []byte(b.String())
}

// ManualLine is a shell line that users can add to their own post-commit
// hook to call commitcoach.
func ManualLine(exe string) string {
	return shellQuote(exe) + ` hook post-commit || echo 'commitcoach: warning: the commit was created, but its snapshot was not recorded' >&2`
}

// shellQuote quotes s for POSIX sh. Inside single quotes nothing is special
// except the single quote itself, which is written as '"'"'.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// State classifies a post-commit hook file.
type State string

const (
	NotInstalled State = "not_installed" // no hook file
	Installed    State = "installed"     // written by commitcoach and unchanged
	Modified     State = "modified"      // written by commitcoach, edited since
	Foreign      State = "foreign"       // not written by commitcoach
)

// HookFile describes one post-commit hook file.
type HookFile struct {
	Path  string
	State State
	// Executable is the binary a commitcoach hook runs ("" if unknown).
	Executable string
	Mode       fs.FileMode
	// CallsCommitcoach reports that a foreign hook appears to call
	// "commitcoach hook post-commit" (manual integration).
	CallsCommitcoach bool
}

func inspectFile(path string) (HookFile, error) {
	h := HookFile{Path: path, State: NotInstalled}
	fi, err := os.Lstat(path) // never follow a symlink: it is someone else's setup
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	h.Mode = fi.Mode()
	h.State = Foreign
	if !fi.Mode().IsRegular() || fi.Size() > maxHookBytes {
		return h, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return h, err
	}
	h.State, h.Executable = classify(data)
	h.CallsCommitcoach = h.State == Foreign && bytes.Contains(data, []byte("hook post-commit")) && bytes.Contains(data, []byte("commitcoach"))
	return h, nil
}

func classify(data []byte) (State, string) {
	if !bytes.Contains(data, []byte("\n"+markerLine+"\n")) {
		return Foreign, ""
	}
	exe, ok := parseExecutable(data)
	if !ok {
		return Modified, ""
	}
	if !bytes.Equal(data, Script(exe)) {
		return Modified, exe
	}
	return Installed, exe
}

// parseExecutable reads the binary path from the comment line. The path is
// Go-quoted there, so it is a single line even if the path has newlines.
func parseExecutable(data []byte) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, exePrefix); ok {
			exe, err := strconv.Unquote(rest)
			return exe, err == nil && exe != ""
		}
	}
	return "", false
}

// Status is the hook setup of a repository.
type Status struct {
	// HooksDir is the repository's own hooks directory (<common dir>/hooks),
	// where init installs.
	HooksDir string
	// EffectiveHooksDir is where git actually runs hooks from; it differs
	// from HooksDir when core.hooksPath is set.
	EffectiveHooksDir string
	// HooksPath lists every core.hooksPath setting, from any scope.
	HooksPath []gitrepo.ConfigValue
	// Hook is <HooksDir>/post-commit.
	Hook HookFile
	// EffectiveHook is <EffectiveHooksDir>/post-commit when that is a
	// different directory; nil otherwise.
	EffectiveHook *HookFile
}

// Inspect reads the hook setup without changing anything.
func Inspect(ctx context.Context, repo *gitrepo.Repo) (*Status, error) {
	hooksPath, err := repo.ConfigValues(ctx, "core.hooksPath")
	if err != nil {
		return nil, fmt.Errorf("reading core.hooksPath: %w", err)
	}
	effective, err := repo.HooksDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding the hooks directory: %w", err)
	}
	st := &Status{
		HooksDir:          filepath.Join(repo.CommonDir, "hooks"),
		EffectiveHooksDir: effective,
		HooksPath:         hooksPath,
	}
	if st.Hook, err = inspectFile(filepath.Join(st.HooksDir, HookName)); err != nil {
		return nil, err
	}
	if !sameDir(st.HooksDir, effective) {
		h, err := inspectFile(filepath.Join(effective, HookName))
		if err != nil {
			return nil, err
		}
		st.EffectiveHook = &h
	}
	return st, nil
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// ConflictError means init left everything unchanged because an existing
// setting or file is in the way. It explains how to integrate manually.
type ConflictError struct {
	Reason   string
	Details  []string
	HookFile string // where the user can add Line
	Line     string
}

func (e *ConflictError) Error() string { return e.Reason }

// Action is what Install or Uninstall did.
type Action string

const (
	Created   Action = "created"
	Updated   Action = "updated"   // a pristine commitcoach hook now runs a different binary
	Unchanged Action = "unchanged" // the same hook was already installed
	Removed   Action = "removed"
	Absent    Action = "absent"       // uninstall: there was no hook file
	LeftAlone Action = "left_foreign" // uninstall: the hook is not commitcoach's
)

// Result reports what Install or Uninstall did.
type Result struct {
	Action   Action
	HookFile string
	// PreviousExecutable is the binary the hook ran before an update.
	PreviousExecutable string
}

var errHookAppeared = errors.New("a post-commit hook appeared while installing; it was left unchanged")

// Install writes the post-commit hook that runs exe (an absolute path).
// Running it again is harmless. It returns a *ConflictError, without changing
// anything, when core.hooksPath is set or another post-commit hook exists.
func Install(ctx context.Context, repo *gitrepo.Repo, exe string) (*Result, error) {
	if repo.Bare {
		return nil, errors.New("this is a bare repository: it has no working tree, so git commit and the post-commit hook never run in it")
	}
	if repo.WorkTree == "" {
		return nil, errors.New("run commitcoach init from the working tree of the repository, not from inside its git directory")
	}
	if !filepath.IsAbs(exe) {
		return nil, fmt.Errorf("executable path %q is not absolute", exe)
	}
	if fi, err := os.Stat(exe); err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("%s is not an executable file", exe)
	}
	st, err := Inspect(ctx, repo)
	if err != nil {
		return nil, err
	}

	if len(st.HooksPath) > 0 {
		c := &ConflictError{
			Reason:   "core.hooksPath is set, so Git runs hooks from " + st.EffectiveHooksDir + " instead of " + st.HooksDir + "; commitcoach does not change this setting or that directory",
			HookFile: filepath.Join(st.EffectiveHooksDir, HookName),
			Line:     ManualLine(exe),
		}
		for _, v := range st.HooksPath {
			c.Details = append(c.Details, fmt.Sprintf("core.hooksPath = %s (%s scope, %s)", v.Value, v.Scope, v.Origin))
		}
		return nil, c
	}
	if !sameDir(st.HooksDir, st.EffectiveHooksDir) {
		return nil, fmt.Errorf("git runs hooks from %s, not from %s as expected; nothing was changed", st.EffectiveHooksDir, st.HooksDir)
	}

	h := st.Hook
	switch h.State {
	case NotInstalled:
		if err := os.MkdirAll(st.HooksDir, 0o755); err != nil {
			return nil, err
		}
		if err := createHook(h.Path, Script(exe)); err != nil {
			if errors.Is(err, errHookAppeared) {
				return nil, &ConflictError{Reason: err.Error(), HookFile: h.Path, Line: ManualLine(exe)}
			}
			return nil, fmt.Errorf("writing %s: %w", h.Path, err)
		}
		return &Result{Action: Created, HookFile: h.Path}, nil

	case Installed:
		if h.Executable == exe {
			if h.Mode.Perm() != hookPerm {
				if err := os.Chmod(h.Path, hookPerm); err != nil {
					return nil, err
				}
			}
			return &Result{Action: Unchanged, HookFile: h.Path}, nil
		}
		if err := replaceHook(h.Path, Script(exe)); err != nil {
			return nil, fmt.Errorf("writing %s: %w", h.Path, err)
		}
		return &Result{Action: Updated, HookFile: h.Path, PreviousExecutable: h.Executable}, nil

	case Modified:
		return nil, &ConflictError{
			Reason:   "the post-commit hook was created by commitcoach but has been edited since; it was left unchanged",
			HookFile: h.Path,
			Line:     ManualLine(exe),
		}
	default:
		return nil, &ConflictError{
			Reason:   "a post-commit hook that was not created by commitcoach already exists; it was left unchanged",
			HookFile: h.Path,
			Line:     ManualLine(exe),
		}
	}
}

// createHook writes a new hook file without ever replacing an existing one:
// the script is written to a temporary file and hard-linked into place,
// which fails if the name exists. Where hard links are not supported, an
// exclusive create is used instead.
func createHook(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".commitcoach-"+HookName+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeAndClose(tmp, content); err != nil {
		return err
	}
	err = os.Link(tmp.Name(), path)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return errHookAppeared
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, hookPerm)
	if errors.Is(err, fs.ErrExist) {
		return errHookAppeared
	}
	if err != nil {
		return err
	}
	if err := writeAndClose(f, content); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// replaceHook atomically replaces a hook file that commitcoach wrote.
func replaceHook(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".commitcoach-"+HookName+"-*")
	if err != nil {
		return err
	}
	if err := writeAndClose(tmp, content); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func writeAndClose(f *os.File, content []byte) error {
	_, err := f.Write(content)
	if err == nil {
		err = f.Chmod(hookPerm) // not subject to the umask
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ModifiedHookError means uninstall found a commitcoach hook that has been
// edited, and left it in place.
type ModifiedHookError struct{ Path string }

func (e *ModifiedHookError) Error() string {
	return e.Path + " was created by commitcoach but has been edited since; it was not removed. Review it and delete it yourself if it is no longer needed"
}

// Uninstall removes the post-commit hook if, and only if, commitcoach wrote
// it and it is unchanged. Snapshots are kept.
func Uninstall(ctx context.Context, repo *gitrepo.Repo) (*Result, error) {
	st, err := Inspect(ctx, repo)
	if err != nil {
		return nil, err
	}
	h := st.Hook
	switch h.State {
	case NotInstalled:
		return &Result{Action: Absent, HookFile: h.Path}, nil
	case Foreign:
		return &Result{Action: LeftAlone, HookFile: h.Path}, nil
	case Modified:
		return nil, &ModifiedHookError{Path: h.Path}
	}
	if err := os.Remove(h.Path); err != nil {
		return nil, err
	}
	return &Result{Action: Removed, HookFile: h.Path, PreviousExecutable: h.Executable}, nil
}
