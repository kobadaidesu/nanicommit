package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kobadaidesu/hook-test/internal/event"
	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/storage"
)

// buildSnapshot is shared by export and the hook, so both produce the same
// document for the same commit.
func buildSnapshot(ctx context.Context, repo *gitrepo.Repo, oid string) ([]byte, *event.Event, error) {
	ev, err := event.Build(ctx, repo, oid, event.DefaultLimits, time.Now())
	if err != nil {
		return nil, nil, err
	}
	data, err := event.Marshal(ev)
	if err != nil {
		return nil, nil, err
	}
	return data, ev, nil
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("export [--commit REV] [--output PATH|-]",
		"Write the snapshot of a commit as one JSON object. The hook does not need to be installed.", stderr)
	rev := fs.String("commit", "HEAD", "commit to export (anything git rev-parse accepts)")
	output := fs.String("output", "-", `where to write the JSON; "-" means standard output`)
	timeout := fs.Duration("timeout", defaultExportTimeout, "give up after this long")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if *output == "" {
		fmt.Fprintln(stderr, `commitcoach export: --output must be a file path or "-"`)
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return fail(stderr, err)
	}
	oid, err := repo.ResolveCommit(ctx, *rev)
	if err != nil {
		return fail(stderr, err)
	}
	data, ev, err := buildSnapshot(ctx, repo, oid)
	if err != nil {
		return fail(stderr, err)
	}
	// Nothing is written until the whole document is ready, so a failure
	// never leaves partial JSON on stdout or in the file.
	if *output == "-" {
		if _, err := stdout.Write(data); err != nil {
			return fail(stderr, fmt.Errorf("writing to standard output: %w", err))
		}
	} else {
		if err := storage.WriteFileAtomic(*output, data); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stderr, "commitcoach: wrote the snapshot of %s to %s\n", oid, *output)
	}
	if n := len(ev.Warnings); n > 0 {
		fmt.Fprintf(stderr, "commitcoach: the snapshot has %d warning(s); see its \"warnings\" field\n", n)
	}
	return exitOK
}

func runHook(args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "post-commit" {
		fmt.Fprintln(stderr, "Usage: commitcoach hook post-commit\n\nRun by the post-commit hook that \"commitcoach init\" installs; only post-commit is supported.")
		return exitUsage
	}
	fs := newFlagSet("hook post-commit", "Record the snapshot of HEAD in the git directory. Run by the post-commit hook.", stderr)
	timeout := fs.Duration("timeout", defaultHookTimeout, "give up after this long")
	if code, ok := parseFlags(fs, args[1:]); !ok {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	oid, path, err := recordHead(ctx)
	if err != nil {
		// The commit exists regardless; say so, so nobody retries it.
		fmt.Fprintf(stderr, "commitcoach: error: the commit was created, but its snapshot could not be recorded: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stderr, "commitcoach: recorded the snapshot of %s in %s\n", oid, path)
	return exitOK
}

// recordHead resolves HEAD once, at the start, and from then on only uses
// that object name, so later changes to HEAD cannot mix into the snapshot.
func recordHead(ctx context.Context) (oid, path string, err error) {
	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return "", "", err
	}
	if repo.Bare {
		return "", "", errors.New("this is a bare repository; the post-commit hook is not supported here")
	}
	if oid, err = repo.ResolveCommit(ctx, "HEAD"); err != nil {
		return "", "", err
	}
	data, _, err := buildSnapshot(ctx, repo, oid)
	if err != nil {
		return oid, "", err
	}
	if path, err = storage.SaveEvent(repo.CommonDir, oid, data); err != nil {
		return oid, "", fmt.Errorf("saving the snapshot: %w", err)
	}
	return oid, path, nil
}
