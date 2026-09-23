package event

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
)

// maxGitStderrWarnings bounds how many lines of git's stderr become warnings.
const maxGitStderrWarnings = 10

// replacementChar replaces bytes that are not valid UTF-8 (U+FFFD).
const replacementChar = string(utf8.RuneError)

// Build makes the snapshot of the commit named by the full object name oid.
// It reads only objects of that commit and its first parent, never the
// working tree or the index, so uncommitted changes cannot leak in.
func Build(ctx context.Context, repo *gitrepo.Repo, oid string, limits Limits) (*Event, error) {
	c, err := repo.ReadCommit(ctx, oid)
	if err != nil {
		return nil, fmt.Errorf("reading commit %s: %w", oid, err)
	}
	w := &warnings{list: []string{}}

	ev := &Event{
		Commit: Commit{
			SHA:     c.OID,
			Parents: c.Parents,
			Message: w.text("the commit message", c.Message),
		},
		Limits: limits,
	}

	branch, ok, err := repo.CurrentBranch(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the current branch: %w", err)
	}
	if ok {
		b := w.text("the branch name", branch)
		ev.Repository.CurrentBranch = &b
	}

	var base string
	if len(c.Parents) == 0 {
		ev.Comparison.Strategy = StrategyEmptyTree
		if base, err = repo.EmptyTree(ctx); err != nil {
			return nil, fmt.Errorf("computing the empty tree: %w", err)
		}
	} else {
		base = c.Parents[0]
		if err := requireObject(ctx, repo, base); err != nil {
			return nil, fmt.Errorf("commit %s: %w", oid, err)
		}
		ev.Comparison.Strategy = StrategyFirstParent
		ev.Comparison.BaseSHA = &base
		if len(c.Parents) > 1 {
			w.add(fmt.Sprintf("merge commit with %d parents: files and diff show the changes relative to the first parent only", len(c.Parents)))
		}
	}

	d, err := repo.Diff(ctx, base, oid, gitrepo.DiffOptions{
		MaxFiles:           limits.MaxFiles,
		MaxFilePatchBytes:  limits.MaxFilePatchBytes,
		MaxTotalPatchBytes: limits.MaxTotalPatchBytes,
		OmitPatch: func(oldPath, newPath string) bool {
			return IsSensitivePath(oldPath) || IsSensitivePath(newPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("computing the diff of %s: %w", oid, err)
	}

	ev.Files = make([]File, 0, len(d.Files))
	truncated := false
	for _, fc := range d.Files {
		f := convertFile(fc, w)
		truncated = truncated || f.PatchTruncated || fc.PatchState == gitrepo.PatchOmittedTotalLimit
		ev.Files = append(ev.Files, f)
	}
	ev.Summary = Summary{
		ChangedFiles:  d.TotalFiles,
		IncludedFiles: len(ev.Files),
		OmittedFiles:  d.TotalFiles - len(ev.Files),
	}
	ev.Summary.Truncated = ev.Summary.OmittedFiles > 0 || truncated

	gitLines := 0
	for _, line := range strings.Split(d.GitStderr, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if gitLines == maxGitStderrWarnings {
			w.add("git: (more messages omitted)")
			break
		}
		gitLines++
		w.add("git: " + strings.ToValidUTF8(line, replacementChar))
	}
	ev.Warnings = w.list
	return ev, nil
}

// requireObject fails if the base of the comparison is missing, which
// happens at the boundary of a shallow clone. Such a commit is not a root
// commit, so it must not be compared with the empty tree.
func requireObject(ctx context.Context, repo *gitrepo.Repo, oid string) error {
	ok, err := repo.HasObject(ctx, oid)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	hint := ""
	if shallow, err := repo.IsShallow(ctx); err == nil && shallow {
		hint = "; this is a shallow clone, run 'git fetch --unshallow' (or fetch more history) to get it"
	}
	return fmt.Errorf("its first parent %s is not in the local repository, so the diff cannot be computed "+
		"(the commit is not treated as a root commit)%s", oid, hint)
}

func convertFile(fc gitrepo.FileChange, w *warnings) File {
	f := File{Status: fc.Status, Binary: fc.Binary}
	// %q shows the exact bytes of a path that is not valid UTF-8.
	if fc.OldPath != "" {
		p := w.text(fmt.Sprintf("the path %q", fc.OldPath), fc.OldPath)
		f.OldPath = &p
	}
	if fc.NewPath != "" {
		p := w.text(fmt.Sprintf("the path %q", fc.NewPath), fc.NewPath)
		f.NewPath = &p
	}
	if !fc.Binary {
		a, d := fc.Additions, fc.Deletions
		f.Additions, f.Deletions = &a, &d
	}
	var reason string
	switch fc.PatchState {
	case gitrepo.PatchKept:
		p := fc.Patch
		f.Patch = &p
		f.PatchTruncated = fc.PatchTruncated
		if fc.PatchInvalidUTF8 {
			path := fc.NewPath
			if path == "" {
				path = fc.OldPath
			}
			w.add(fmt.Sprintf("the diff of %q contained bytes that are not valid UTF-8; they were replaced with U+FFFD", path))
		}
	case gitrepo.PatchOmittedBinary:
		reason = OmittedBinary
	case gitrepo.PatchOmittedByFilter:
		reason = OmittedSensitivePath
	case gitrepo.PatchOmittedTotalLimit:
		reason = OmittedTotalPatchLimit
	}
	if reason != "" {
		f.OmittedReason = &reason
	}
	return f
}

type warnings struct{ list []string }

func (w *warnings) add(s string) { w.list = append(w.list, s) }

// text returns s as valid UTF-8. JSON strings cannot carry arbitrary bytes,
// so invalid bytes are replaced with U+FFFD, and a warning says so: the
// value then no longer matches the repository exactly.
func (w *warnings) text(what, s string) string {
	if utf8.ValidString(s) {
		return s
	}
	w.add(fmt.Sprintf("%s is not valid UTF-8; invalid bytes were replaced with U+FFFD, so it differs from the value in the repository", what))
	return strings.ToValidUTF8(s, replacementChar)
}
