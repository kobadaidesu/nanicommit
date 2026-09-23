package event

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
)

// maxGitStderrWarnings bounds how many lines of git's stderr become warnings.
const maxGitStderrWarnings = 10

// Build makes the snapshot of the commit named by the full object name oid.
// It reads only objects of that commit and its first parent, never the
// working tree or the index, so uncommitted changes cannot leak in.
func Build(ctx context.Context, repo *gitrepo.Repo, oid string, limits Limits, capturedAt time.Time) (*Event, error) {
	c, err := repo.ReadCommit(ctx, oid)
	if err != nil {
		return nil, fmt.Errorf("reading commit %s: %w", oid, err)
	}
	w := &warnings{list: []string{}}

	ev := &Event{
		SchemaVersion: SchemaVersion,
		EventType:     EventType,
		CapturedAt:    capturedAt.UTC().Format(time.RFC3339),
		Repository:    Repository{Name: w.text("repository.name", repo.DisplayName())},
		Commit: Commit{
			SHA:         c.OID,
			Parents:     c.Parents,
			Message:     w.text("commit.message", c.Message),
			AuthorName:  w.text("commit.author_name", c.AuthorName),
			AuthoredAt:  c.AuthorDate.Format(time.RFC3339),
			CommittedAt: c.CommitterDate.Format(time.RFC3339),
		},
	}

	branch, ok, err := repo.CurrentBranch(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the current branch: %w", err)
	}
	if ok {
		b := w.text("repository.current_branch", branch)
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
			w.add(fmt.Sprintf("merge commit with %d parents: files and patches show the changes relative to the first parent only", len(c.Parents)))
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
	truncatedPatches, totalLimitHit := 0, false
	for i, fc := range d.Files {
		f := convertFile(i, fc, w)
		if f.PatchTruncated {
			truncatedPatches++
		}
		if fc.PatchState == gitrepo.PatchOmittedTotalLimit {
			totalLimitHit = true
		}
		ev.Files = append(ev.Files, f)
	}
	ev.Summary = Summary{
		ChangedFiles:  d.TotalFiles,
		IncludedFiles: len(ev.Files),
		OmittedFiles:  d.TotalFiles - len(ev.Files),
	}
	ev.Summary.Truncated = ev.Summary.OmittedFiles > 0 || truncatedPatches > 0 || totalLimitHit

	if ev.Summary.OmittedFiles > 0 {
		w.add(fmt.Sprintf("files lists the first %d of %d changed files (limit: %d files per event)",
			len(ev.Files), d.TotalFiles, limits.MaxFiles))
	}
	if truncatedPatches > 0 {
		w.add(fmt.Sprintf("%d patch(es) were cut at a size limit (%d bytes per file, %d bytes per event); see patch_truncated",
			truncatedPatches, limits.MaxFilePatchBytes, limits.MaxTotalPatchBytes))
	}
	if totalLimitHit {
		w.add(fmt.Sprintf("patch text reached the limit of %d bytes per event; later files have no patch (omitted_reason %q)",
			limits.MaxTotalPatchBytes, OmittedTotalPatchLimit))
	}
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
		w.add("git: " + strings.ToValidUTF8(line, "�"))
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

func convertFile(i int, fc gitrepo.FileChange, w *warnings) File {
	f := File{Status: fc.Status, Binary: fc.Binary}
	if fc.OldPath != "" {
		p := w.text(fmt.Sprintf("files[%d].old_path", i), fc.OldPath)
		f.OldPath = &p
	}
	if fc.NewPath != "" {
		p := w.text(fmt.Sprintf("files[%d].new_path", i), fc.NewPath)
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
			w.add(fmt.Sprintf("files[%d].patch contained bytes that are not valid UTF-8; they were replaced with U+FFFD", i))
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
func (w *warnings) text(field, s string) string {
	if utf8.ValidString(s) {
		return s
	}
	w.add(fmt.Sprintf("%s is not valid UTF-8; invalid bytes were replaced with U+FFFD, so it differs from the value in the repository", field))
	return strings.ToValidUTF8(s, "�")
}
