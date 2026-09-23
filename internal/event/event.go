// Package event defines the commit.snapshot JSON document and builds it from
// a repository. It is independent of how the document is delivered: the CLI
// writes it to a file or stdout today, and an HTTP client could send the same
// bytes later without touching the Git code.
package event

import (
	"bytes"
	"encoding/json"
)

// Constant values used in the document. See docs/commit-snapshot.schema.json.
const (
	SchemaVersion = "1.0"
	// EventType is the same for hook captures and manual exports: the
	// document describes a commit, not the act of creating it.
	EventType = "commit.snapshot"

	// StrategyFirstParent compares the commit with its first parent. It is
	// used for ordinary and merge commits alike.
	StrategyFirstParent = "first_parent"
	// StrategyEmptyTree compares a root commit with the empty tree.
	StrategyEmptyTree = "empty_tree"

	// Values of File.OmittedReason.
	OmittedBinary          = "binary"
	OmittedSensitivePath   = "sensitive_path"
	OmittedTotalPatchLimit = "total_patch_limit"
)

// Event is one commit.snapshot document.
type Event struct {
	SchemaVersion string     `json:"schema_version"`
	EventType     string     `json:"event_type"`
	CapturedAt    string     `json:"captured_at"` // when this document was made (RFC 3339, UTC)
	Repository    Repository `json:"repository"`
	Commit        Commit     `json:"commit"`
	Comparison    Comparison `json:"comparison"`
	Files         []File     `json:"files"`
	Summary       Summary    `json:"summary"`
	Warnings      []string   `json:"warnings"`
}

// Repository identifies the repository for display only.
type Repository struct {
	// Name is the repository directory name. It is not unique.
	Name string `json:"name"`
	// CurrentBranch is the branch checked out when the document was made
	// (not necessarily a branch containing the commit), or null when HEAD
	// was detached.
	CurrentBranch *string `json:"current_branch"`
}

// Commit is the commit's own metadata.
type Commit struct {
	SHA         string   `json:"sha"`
	Parents     []string `json:"parents"`
	Message     string   `json:"message"`
	AuthorName  string   `json:"author_name"`
	AuthoredAt  string   `json:"authored_at"`  // RFC 3339 with the author's UTC offset
	CommittedAt string   `json:"committed_at"` // RFC 3339 with the committer's UTC offset
}

// Comparison says what the files were compared against.
type Comparison struct {
	Strategy string  `json:"strategy"`
	BaseSHA  *string `json:"base_sha"` // null for StrategyEmptyTree
}

// File is one changed file.
type File struct {
	Status  string  `json:"status"`   // git's letter: A, M, D, R, T, ...
	OldPath *string `json:"old_path"` // null when added
	NewPath *string `json:"new_path"` // null when deleted
	// Additions and Deletions are null when git cannot count lines (binary).
	Additions      *int    `json:"additions"`
	Deletions      *int    `json:"deletions"`
	Binary         bool    `json:"binary"`
	Patch          *string `json:"patch"` // null exactly when OmittedReason is set
	PatchTruncated bool    `json:"patch_truncated"`
	OmittedReason  *string `json:"omitted_reason"`
}

// Summary gives the totals.
type Summary struct {
	ChangedFiles  int  `json:"changed_files"`  // files in the comparison
	IncludedFiles int  `json:"included_files"` // len(files)
	OmittedFiles  int  `json:"omitted_files"`  // changed files left out of files by the file limit
	Truncated     bool `json:"truncated"`      // a limit cut the file list or any patch
}

// Marshal encodes ev as indented JSON followed by a newline. HTML characters
// are not escaped, so code in patches stays readable.
func Marshal(ev *Event) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ev); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
