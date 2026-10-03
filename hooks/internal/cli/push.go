package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kobadaidesu/hook-test/internal/backend"
	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/repoid"
)

// maxPushCheckSHAs mirrors the backend's limit on one push check.
const maxPushCheckSHAs = 1000

// zeroOID reports the all-zero object name git uses for "no commit"
// (a new branch's old side, or a deletion's new side).
func zeroOID(oid string) bool {
	return strings.Trim(oid, "0") == ""
}

// runSend sends one commit to the backend and prints the quiz it got back.
func runSend(args []string, stderr io.Writer) int {
	fs := newFlagSet("send [--commit REV] [--repository-id UUID]",
		"Send the JSON of a commit (default HEAD) to the learning backend and print the quiz URL.\n"+
			"Use it to resend a commit whose earlier send failed.", stderr)
	rev := fs.String("commit", "HEAD", "commit to send (anything git rev-parse accepts)")
	flagID := fs.String("repository-id", "", "repository UUID for this run only (default: the one saved by init)")
	timeout := fs.Duration("timeout", defaultExportTimeout, "give up after this long")
	if code, ok := parseFlags(fs, args); !ok {
		return code
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
	cfg, err := backend.LoadConfig(ctx, repo)
	if err != nil {
		return fail(stderr, err)
	}
	data, notes, err := buildPayload(ctx, repo, oid, id)
	if err != nil {
		return fail(stderr, explain(err))
	}
	printNotes(stderr, notes)
	fmt.Fprintf(stderr, "nanicommit: sending %s to %s ...\n", short(oid), cfg.BaseURL)
	res, err := backend.New(cfg).SendCommit(ctx, data)
	if err != nil {
		return fail(stderr, err)
	}
	printQuiz(stderr, oid, res)
	printTalk(stderr, res.TalkURL)
	return exitOK
}

func printQuiz(stderr io.Writer, oid string, res *backend.CommitResult) {
	switch {
	case res.Status == "passed":
		fmt.Fprintf(stderr, "nanicommit: %s has already passed its quiz\n", short(oid))
	case res.AlreadyRegistered:
		fmt.Fprintf(stderr, "nanicommit: %s was already sent; its quiz is waiting: %s\n", short(oid), res.QuizURL)
	default:
		fmt.Fprintf(stderr, "nanicommit: %d questions are ready for %s: %s\n", res.QuestionCount, short(oid), res.QuizURL)
	}
}

func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// runHookPrePush is run by the pre-push hook. Git passes the remote name
// and URL as arguments and one line per ref on stdin:
//
//	<local ref> SP <local oid> SP <remote ref> SP <remote oid> LF
//
// A non-zero exit makes git abort the push (design.md 3.4): it is returned
// when a commit being pushed has not passed its quiz, and also when the
// backend cannot be reached, because the pass state cannot be confirmed.
func runHookPrePush(args []string, stdin io.Reader, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), defaultHookTimeout)
	defer cancel()

	repo, err := gitrepo.Open(ctx, "")
	if err != nil {
		return fail(stderr, err)
	}
	shas, err := pushedCommits(ctx, repo, stdin)
	if err != nil {
		return fail(stderr, err)
	}
	if len(shas) == 0 {
		return exitOK // nothing new to push (deletes only, or everything is upstream)
	}
	if len(shas) > maxPushCheckSHAs {
		fmt.Fprintf(stderr, "nanicommit: %d commits are being pushed; only the newest %d are checked\n", len(shas), maxPushCheckSHAs)
		shas = shas[:maxPushCheckSHAs]
	}

	cfg, err := backend.LoadConfig(ctx, repo)
	if errors.Is(err, backend.ErrNotConfigured) {
		fmt.Fprintf(stderr, "nanicommit: note: the push was allowed without a check, because %v\n", err)
		return exitOK
	}
	if err != nil {
		return fail(stderr, err)
	}
	id, err := repoid.Load(ctx, repo)
	if err != nil {
		return fail(stderr, err)
	}

	check, err := backend.New(cfg).CheckPush(ctx, id, shas)
	if err != nil {
		printBlockBanner(stderr)
		fmt.Fprintf(stderr, "nanicommit: the push was blocked, because the pass state could not be confirmed: %v\n", err)
		return exitError
	}
	if check.Allowed {
		fmt.Fprintf(stderr, "nanicommit: all %d commit(s) have passed their quizzes; pushing\n", len(shas))
		return exitOK
	}

	// Only Ponta's banner: the commits and quiz URLs are not listed here.
	printBlockBanner(stderr)
	return exitError
}

// pushedCommits lists the commits that the refs on stdin would publish,
// newest first, without duplicates. For a ref the remote does not have yet,
// every commit that no remote-tracking ref already has is counted.
func pushedCommits(ctx context.Context, repo *gitrepo.Repo, stdin io.Reader) ([]string, error) {
	var shas []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("unexpected pre-push input line %q", line)
		}
		localOID, remoteOID := fields[1], fields[3]
		if zeroOID(localOID) {
			continue // deleting a remote ref pushes no commits
		}
		var list []string
		var err error
		if zeroOID(remoteOID) {
			list, err = repo.RevList(ctx, localOID, "--not", "--remotes")
		} else {
			list, err = repo.RevList(ctx, remoteOID+".."+localOID)
		}
		if err != nil {
			return nil, fmt.Errorf("listing the commits of %s: %w", fields[0], err)
		}
		for _, oid := range list {
			if !seen[oid] {
				seen[oid] = true
				shas = append(shas, oid)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the pre-push input: %w", err)
	}
	return shas, nil
}
