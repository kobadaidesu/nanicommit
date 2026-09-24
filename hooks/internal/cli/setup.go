package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/hooks"
	"github.com/kobadaidesu/hook-test/internal/repoid"
	"github.com/kobadaidesu/hook-test/internal/storage"
)

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("init [--repository-id UUID]", "Install a post-commit hook in the current repository that runs this commitcoach binary,\n"+
		"and save the repository ID in the local git config ("+repoid.ConfigKey+").\n"+
		"Nothing is changed if core.hooksPath is set or another post-commit hook exists.", stderr)
	flagID := fs.String("repository-id", "", "UUID of this repository in the backend (required unless already saved)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	exe, err := installableExecutable()
	if err != nil {
		return fail(stderr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return fail(stderr, err)
	}
	// Check the ID before changing anything.
	id, err := repoid.Resolve(ctx, repo, *flagID)
	if err != nil {
		return fail(stderr, err)
	}
	saved, _ := repoid.Load(ctx, repo) // "" when not set (or not valid)

	res, err := hooks.Install(ctx, repo, exe)
	var conflict *hooks.ConflictError
	if errors.As(err, &conflict) {
		fmt.Fprintf(stderr, "commitcoach: not installed: %s.\n", conflict.Reason)
		for _, d := range conflict.Details {
			fmt.Fprintf(stderr, "  %s\n", d)
		}
		fmt.Fprintf(stderr, "No file or setting was changed.\n\n"+
			"To run commitcoach from your own hook, add this line to %s\n"+
			"(create the file with \"#!/bin/sh\" as its first line and make it executable if it does not exist):\n\n    %s\n",
			conflict.HookFile, conflict.Line)
		if id != saved {
			fmt.Fprintf(stderr, "\nand save the repository ID:\n\n    git config --local %s %s\n", repoid.ConfigKey, id)
		}
		return exitError
	}
	if err != nil {
		return fail(stderr, err)
	}

	switch res.Action {
	case hooks.Created:
		fmt.Fprintf(stdout, "commitcoach: installed the post-commit hook: %s\n", res.HookFile)
	case hooks.Updated:
		fmt.Fprintf(stdout, "commitcoach: updated the post-commit hook: %s\n  previous executable: %s\n", res.HookFile, res.PreviousExecutable)
	case hooks.Unchanged:
		fmt.Fprintf(stdout, "commitcoach: the post-commit hook is already installed: %s\n", res.HookFile)
	}
	fmt.Fprintf(stdout, "  runs:      %s hook post-commit\n", exe)
	fmt.Fprintf(stdout, "  snapshots: %s\n", storage.EventsDir(repo.CommonDir))
	if id != saved {
		if err := repoid.Save(ctx, repo, id); err != nil {
			return fail(stderr, fmt.Errorf("the hook is installed, but %w; run init again", err))
		}
		fmt.Fprintf(stdout, "  repository id: %s (saved as %s in the local git config)\n", id, repoid.ConfigKey)
	} else {
		fmt.Fprintf(stdout, "  repository id: %s\n", id)
	}
	if repo.GitDir != repo.CommonDir {
		fmt.Fprintln(stdout, "  note:      hooks are shared by all worktrees of this repository")
	}
	return exitOK
}

// installableExecutable returns the absolute path of the running binary,
// which the hook will call. A binary made by "go run" lives in a temporary
// build directory that Go deletes, so it is refused.
func installableExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find the path of this commitcoach binary: %w", err)
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return "", err
	}
	if isGoRunBinary(exe) {
		return "", fmt.Errorf("this binary (%s) is a temporary file made by \"go run\" and will disappear; "+
			"build it first (go build -o bin/commitcoach ./cmd/commitcoach) and run init with that binary", exe)
	}
	return exe, nil
}

// goRunBinaryRE matches where "go run" puts binaries:
// $GOTMPDIR/go-build<digits>/b<digits>/exe/<name>.
var goRunBinaryRE = regexp.MustCompile(`/go-build[0-9]+/b[0-9]+/exe/[^/]+$`)

func isGoRunBinary(exe string) bool {
	return goRunBinaryRE.MatchString(filepath.ToSlash(exe))
}

func runUninstall(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("uninstall", "Remove the post-commit hook if commitcoach installed it and it has not been edited.\n"+
		"Other hooks and settings are never touched, and recorded snapshots are kept.", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return fail(stderr, err)
	}
	res, err := hooks.Uninstall(ctx, repo)
	var modified *hooks.ModifiedHookError
	if errors.As(err, &modified) {
		fmt.Fprintf(stderr, "commitcoach: warning: %v\n", err)
		return exitError
	}
	if err != nil {
		return fail(stderr, err)
	}
	switch res.Action {
	case hooks.Removed:
		fmt.Fprintf(stdout, "commitcoach: removed the post-commit hook: %s\n", res.HookFile)
	case hooks.Absent:
		fmt.Fprintf(stdout, "commitcoach: no post-commit hook is installed (%s does not exist); nothing to do\n", res.HookFile)
	case hooks.LeftAlone:
		fmt.Fprintf(stdout, "commitcoach: %s was not created by commitcoach; it was left unchanged\n", res.HookFile)
	}
	if n, err := storage.CountEvents(repo.CommonDir); err == nil && n > 0 {
		fmt.Fprintf(stdout, "  %d snapshot(s) were kept in %s; delete that directory yourself if you no longer need them\n",
			n, storage.EventsDir(repo.CommonDir))
	}
	if id, err := repoid.Load(ctx, repo); err == nil {
		fmt.Fprintf(stdout, "  the repository ID (%s) was kept; remove it with: git config --local --unset %s\n", id, repoid.ConfigKey)
	}
	return exitOK
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("status", "Show the repository, the hook setup and where snapshots are stored. Nothing is changed.", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()
	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return fail(stderr, err)
	}
	st, err := hooks.Inspect(ctx, repo)
	if err != nil {
		return fail(stderr, err)
	}
	self, _ := os.Executable()

	out := func(label, format string, a ...any) {
		fmt.Fprintf(stdout, "%-18s %s\n", label+":", fmt.Sprintf(format, a...))
	}
	out("repository", "%s", repo.DisplayName())
	if repo.WorkTree != "" {
		out("working tree", "%s", repo.WorkTree)
	} else if repo.Bare {
		out("working tree", "none (bare repository: git commit and hooks do not run here)")
	}
	out("git directory", "%s", repo.GitDir)
	if repo.GitDir != repo.CommonDir {
		out("common directory", "%s (linked worktree; hooks and snapshots are shared)", repo.CommonDir)
	}
	out("git", "%s", repo.GitVersion)
	if id, err := repoid.Load(ctx, repo); err == nil {
		out("repository id", "%s (%s, local git config)", id, repoid.ConfigKey)
	} else if errors.Is(err, repoid.ErrNotSet) {
		out("repository id", "not set (run \"commitcoach init --repository-id <UUID>\"; the hook fails without it)")
	} else {
		out("repository id", "INVALID: %v", err)
	}

	if len(st.HooksPath) > 0 {
		out("hooks directory", "%s (set by core.hooksPath)", st.EffectiveHooksDir)
	} else {
		out("hooks directory", "%s (Git default)", st.EffectiveHooksDir)
	}

	h := st.Hook
	switch h.State {
	case hooks.NotInstalled:
		out("post-commit hook", "not installed (run \"commitcoach init\")")
	case hooks.Installed:
		out("post-commit hook", "installed by commitcoach, unmodified")
	case hooks.Modified:
		out("post-commit hook", "created by commitcoach but edited since (init and uninstall leave it alone)")
	case hooks.Foreign:
		out("post-commit hook", "exists but was not created by commitcoach")
	}
	if h.State != hooks.NotInstalled {
		out("  hook file", "%s", h.Path)
	}
	if h.Executable != "" {
		out("  executable", "%s (%s)", h.Executable, describeExecutable(h.Executable, self))
	}

	out("snapshots", "%s", describeEvents(repo.CommonDir))

	conflicts := statusConflicts(st)
	if len(conflicts) == 0 {
		out("conflicts", "none")
	} else {
		out("conflicts", "%s", conflicts[0])
		for _, c := range conflicts[1:] {
			fmt.Fprintf(stdout, "%-18s %s\n", "", c)
		}
	}
	return exitOK
}

func statusConflicts(st *hooks.Status) []string {
	var list []string
	for _, v := range st.HooksPath {
		list = append(list, fmt.Sprintf("core.hooksPath = %s (%s scope, %s)", v.Value, v.Scope, v.Origin))
	}
	if len(st.HooksPath) > 0 && st.Hook.State == hooks.Installed {
		list = append(list, "the commitcoach hook in "+st.HooksDir+" does not run because core.hooksPath points elsewhere")
	}
	if eh := st.EffectiveHook; eh != nil {
		switch {
		case eh.State == hooks.NotInstalled:
			list = append(list, "no post-commit hook in "+st.EffectiveHooksDir)
		case eh.CallsCommitcoach:
			list = append(list, eh.Path+" exists and appears to call commitcoach (manual integration)")
		default:
			list = append(list, eh.Path+" exists and is not managed by commitcoach")
		}
	}
	switch st.Hook.State {
	case hooks.Foreign:
		if st.Hook.CallsCommitcoach {
			list = append(list, st.Hook.Path+" is not managed by commitcoach but appears to call it (manual integration)")
		} else {
			list = append(list, st.Hook.Path+" is someone else's hook; add the commitcoach call to it by hand (see \"commitcoach init\")")
		}
	case hooks.Modified:
		list = append(list, st.Hook.Path+" was edited after commitcoach created it")
	}
	return list
}

func describeExecutable(exe, self string) string {
	fi, err := os.Stat(exe)
	var s string
	switch {
	case err != nil:
		return "MISSING: commits will print a warning and no snapshot is recorded; rebuild it or run init with the new binary"
	case !fi.Mode().IsRegular():
		return "not a regular file"
	case fi.Mode()&0o111 == 0:
		return "not executable"
	default:
		s = "ok"
	}
	if self != "" {
		if a, err := os.Stat(self); err == nil && os.SameFile(a, fi) {
			s += ", the binary you are running"
		} else {
			s += ", not the binary you are running (" + self + ")"
		}
	}
	return s
}

func describeEvents(commonDir string) string {
	dir := storage.EventsDir(commonDir)
	n, err := storage.CountEvents(commonDir)
	switch {
	case err != nil:
		return fmt.Sprintf("%s (cannot read: %v)", dir, err)
	case n == 0:
		if _, err := os.Stat(dir); err != nil {
			return dir + " (not created yet)"
		}
		return dir + " (empty)"
	default:
		return fmt.Sprintf("%s (%d JSON file(s))", dir, n)
	}
}
