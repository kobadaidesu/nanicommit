package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

// Commit holds the metadata of one commit.
type Commit struct {
	OID string
	// Parents come from the raw commit object. They are read with
	// "git cat-file" because "git log" hides the parents of the boundary
	// commits of a shallow clone, which would make them look like root
	// commits.
	Parents       []string
	Message       string // full message, byte for byte (re-encoded to UTF-8 if the commit declares another encoding)
	AuthorName    string
	AuthorDate    time.Time
	CommitterDate time.Time
}

// ReadCommit reads the commit named by the full object name oid.
func (r *Repo) ReadCommit(ctx context.Context, oid string) (*Commit, error) {
	if !IsObjectID(oid) {
		return nil, fmt.Errorf("%q is not a full object name", oid)
	}
	raw, err := r.run.Output(ctx, "cat-file", "commit", oid)
	if err != nil {
		return nil, err
	}
	parents, err := parseParents(raw)
	if err != nil {
		return nil, fmt.Errorf("commit %s: %w", oid, err)
	}

	// %B is the raw message. With "format:" (not "tformat:") git appends no
	// terminator, and the NUL separators cannot occur in a commit message.
	// The flags keep user configuration from changing the output or running
	// gpg (log.showSignature), and keep the recorded author name as is.
	out, err := r.run.Output(ctx, "log", "--max-count=1", "--no-show-signature", "--no-use-mailmap",
		"--encoding=UTF-8", "--format=format:%H%x00%an%x00%aI%x00%cI%x00%B", "--end-of-options", oid, "--")
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(out), "\x00", 5)
	if len(parts) != 5 || parts[0] != oid {
		return nil, fmt.Errorf("unexpected output from git log for %s", oid)
	}
	authorDate, err := time.Parse(time.RFC3339, parts[2])
	if err != nil {
		return nil, fmt.Errorf("commit %s: author date: %w", oid, err)
	}
	committerDate, err := time.Parse(time.RFC3339, parts[3])
	if err != nil {
		return nil, fmt.Errorf("commit %s: committer date: %w", oid, err)
	}
	return &Commit{
		OID:           oid,
		Parents:       parents,
		Message:       parts[4],
		AuthorName:    parts[1],
		AuthorDate:    authorDate,
		CommitterDate: committerDate,
	}, nil
}

// parseParents returns the "parent" headers of a raw commit object. Header
// lines hold no paths; continuation lines (e.g. of a signature) start with a
// space and never match.
func parseParents(raw []byte) ([]string, error) {
	header, _, _ := bytes.Cut(raw, []byte("\n\n"))
	parents := []string{}
	for _, line := range bytes.Split(header, []byte("\n")) {
		if p, ok := bytes.CutPrefix(line, []byte("parent ")); ok {
			if !IsObjectID(string(p)) {
				return nil, fmt.Errorf("malformed parent header %q", line)
			}
			parents = append(parents, string(p))
		}
	}
	return parents, nil
}

// HasObject reports whether the object named oid exists locally. It does not
// fetch anything.
func (r *Repo) HasObject(ctx context.Context, oid string) (bool, error) {
	if !IsObjectID(oid) {
		return false, fmt.Errorf("%q is not a full object name", oid)
	}
	_, err := r.run.Output(ctx, "cat-file", "-e", oid)
	if err == nil {
		return true, nil
	}
	if hasExitCode(err, 1) { // missing: cat-file -e exits 1 without a message
		return false, nil
	}
	return false, err
}

// EmptyTree returns the name of the empty tree in this repository's hash
// algorithm. git computes it; nothing is written to the object database.
func (r *Repo) EmptyTree(ctx context.Context) (string, error) {
	// Standard input is empty (os/exec connects it to the null device).
	out, err := r.run.Output(ctx, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if !IsObjectID(oid) {
		return "", fmt.Errorf("git hash-object returned %q, not an object name", oid)
	}
	return oid, nil
}
