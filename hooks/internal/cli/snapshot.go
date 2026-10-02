package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/kobadaidesu/hook-test/internal/backend"
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
		fmt.Fprintf(stderr, "nanicommit: note: %s\n", n)
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
		fmt.Fprintf(stderr, "nanicommit: wrote the snapshot of %s to %s\n", oid, *output)
	}
	printNotes(stderr, notes)
	return exitOK
}

func runHook(args []string, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "post-commit" && args[0] != "pre-push") {
		fmt.Fprintln(stderr, "Usage: commitcoach hook {post-commit|pre-push}\n\nRun by the hooks that \"commitcoach init\" installs.")
		return exitUsage
	}
	if args[0] == "pre-push" {
		// Git passes the remote name and URL as arguments and the refs on
		// stdin; the arguments are not needed for the check.
		return runHookPrePush(args[1:], os.Stdin, stderr)
	}
	fs := newFlagSet("hook post-commit", "Record the JSON of HEAD in the git directory, then send it to the learning backend\n"+
		"when one is configured. Run by the post-commit hook.", stderr)
	timeout := fs.Duration("timeout", defaultHookTimeout, "give up after this long")
	if code, ok := parseFlags(fs, args[1:]); !ok {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	repo, oid, data, path, notes, err := recordHead(ctx)
	if err != nil {
		// The commit exists regardless; say so, so nobody retries it.
		fmt.Fprintf(stderr, "nanicommit: error: the commit was created, but its snapshot could not be recorded: %v\n", explain(err))
		return exitError
	}
	fmt.Fprintf(stderr, "nanicommit: recorded the snapshot of %s in %s\n", oid, path)
	printNotes(stderr, notes)
	sendRecorded(ctx, repo, oid, data, stderr)
	return exitOK
}

// sendRecorded sends a freshly recorded commit to the backend. Failures
// only warn: the snapshot is saved, so the commit can be resent later, and
// a backend outage must never make committing feel broken.
func sendRecorded(ctx context.Context, repo *gitrepo.Repo, oid string, data []byte, stderr io.Writer) {
	cfg, err := backend.LoadConfig(ctx, repo)
	if errors.Is(err, backend.ErrNotConfigured) {
		return // recording-only setup; nothing to send to
	}
	if err != nil {
		fmt.Fprintf(stderr, "nanicommit: warning: %v\n", err)
		return
	}
	fmt.Fprintf(stderr, "nanicommit: sending %s to %s and waiting for its quiz ...\n", short(oid), cfg.BaseURL)
	res, err := backend.New(cfg).SendCommit(ctx, data)
	if err != nil {
		fmt.Fprintf(stderr, "nanicommit: warning: the commit was recorded, but not sent: %v\n", err)
		fmt.Fprintf(stderr, "nanicommit: resend it later with: commitcoach send --commit %s\n", short(oid))
		return
	}
	printQuiz(stderr, oid, res)
}

// recordHead resolves HEAD once, at the start, and from then on only uses
// that object name, so later changes to HEAD cannot mix into the payload.
func recordHead(ctx context.Context) (repo *gitrepo.Repo, oid string, data []byte, path string, notes []string, err error) {
	repo, err = gitrepo.Open(ctx, "")
	if err != nil {
		return nil, "", nil, "", nil, err
	}
	if repo.Bare {
		return nil, "", nil, "", nil, errors.New("this is a bare repository; the post-commit hook is not supported here")
	}
	if oid, err = repo.ResolveCommit(ctx, "HEAD"); err != nil {
		return repo, "", nil, "", nil, err
	}
	id, err := repoid.Load(ctx, repo)
	if err != nil {
		return repo, oid, nil, "", nil, err
	}
	data, notes, err = buildPayload(ctx, repo, oid, id)
	if err != nil {
		return repo, oid, nil, "", nil, err
	}
	if path, err = storage.SaveEvent(repo.CommonDir, oid, data); err != nil {
		return repo, oid, data, "", nil, fmt.Errorf("saving the snapshot: %w", err)
	}
	return repo, oid, data, path, notes, nil
}
