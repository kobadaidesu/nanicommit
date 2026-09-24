package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/kobadaidesu/hook-test/internal/event"
	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/repoid"
	"github.com/kobadaidesu/hook-test/internal/storage"
)

// buildPayload is shared by export and the hook, so both produce the same
// document for the same commit. notes are for stderr and never contain file
// contents.
func buildPayload(ctx context.Context, repo *gitrepo.Repo, oid, repositoryID string) (data []byte, notes []string, err error) {
	ev, err := event.Build(ctx, repo, oid, event.DefaultLimits)
	if err != nil {
		return nil, nil, err
	}
	p, notes, err := event.NewPayload(repositoryID, ev)
	if err != nil {
		return nil, nil, err
	}
	data, err = event.Marshal(p)
	if err != nil {
		return nil, nil, err
	}
	return data, notes, nil
}

func printNotes(stderr io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintf(stderr, "commitcoach: note: %s\n", n)
	}
}

// explain adds what happened to the output when err is a limit error.
func explain(err error) error {
	if errors.Is(err, event.ErrLimitExceeded) {
		return fmt.Errorf("%w; no JSON was written, because a partial diff would look complete", err)
	}
	return err
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("export [--commit REV] [--output PATH|-] [--repository-id UUID]",
		"Write the JSON of a commit (one object). The hook does not need to be installed.", stderr)
	rev := fs.String("commit", "HEAD", "commit to export (anything git rev-parse accepts)")
	output := fs.String("output", "-", `where to write the JSON; "-" means standard output`)
	flagID := fs.String("repository-id", "", "repository UUID for this run only (default: the one saved by init)")
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
	id, err := repoid.Resolve(ctx, repo, *flagID)
	if err != nil {
		return fail(stderr, err)
	}
	data, notes, err := buildPayload(ctx, repo, oid, id)
	if err != nil {
		return fail(stderr, explain(err))
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
	printNotes(stderr, notes)
	return exitOK
}

func runHook(args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "post-commit" {
		fmt.Fprintln(stderr, "Usage: commitcoach hook post-commit\n\nRun by the post-commit hook that \"commitcoach init\" installs; only post-commit is supported.")
		return exitUsage
	}
	fs := newFlagSet("hook post-commit", "Record the JSON of HEAD in the git directory. Run by the post-commit hook.", stderr)
	timeout := fs.Duration("timeout", defaultHookTimeout, "give up after this long")
	if code, ok := parseFlags(fs, args[1:]); !ok {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	oid, path, notes, err := recordHead(ctx)
	if err != nil {
		// The commit exists regardless; say so, so nobody retries it.
		fmt.Fprintf(stderr, "commitcoach: error: the commit was created, but its snapshot could not be recorded: %v\n", explain(err))
		return exitError
	}
	fmt.Fprintf(stderr, "commitcoach: recorded the snapshot of %s in %s\n", oid, path)
	printNotes(stderr, notes)
	return exitOK
}

// recordHead resolves HEAD once, at the start, and from then on only uses
// that object name, so later changes to HEAD cannot mix into the payload.
func recordHead(ctx context.Context) (oid, path string, notes []string, err error) {
	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return "", "", nil, err
	}
	if repo.Bare {
		return "", "", nil, errors.New("this is a bare repository; the post-commit hook is not supported here")
	}
	if oid, err = repo.ResolveCommit(ctx, "HEAD"); err != nil {
		return "", "", nil, err
	}
	id, err := repoid.Load(ctx, repo)
	if err != nil {
		return oid, "", nil, err
	}
	data, notes, err := buildPayload(ctx, repo, oid, id)
	if err != nil {
		return oid, "", nil, err
	}
	if path, err = storage.SaveEvent(repo.CommonDir, oid, data); err != nil {
		return oid, "", nil, fmt.Errorf("saving the snapshot: %w", err)
	}
	return oid, path, notes, nil
}
