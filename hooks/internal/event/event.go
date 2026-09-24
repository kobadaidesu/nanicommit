// Package event reads one commit into a detailed, internal snapshot (Event)
// and turns it into the published JSON document (Payload). It is
// independent of how the document is delivered: the CLI writes it to a file
// or stdout today, and an HTTP client could send the same bytes later
// without touching the Git code.
package event

// Values used in the internal snapshot.
const (
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

// Event is the detailed snapshot of one commit that Build reads from git.
// It is not published as is: NewPayload turns it into the six-field JSON.
type Event struct {
	Repository Repository
	Commit     Commit
	Comparison Comparison
	Files      []File
	Summary    Summary
	// Limits are the limits the snapshot was read with.
	Limits Limits
	// Warnings are notes for the user, such as the comparison used for a
	// merge commit or text that was not valid UTF-8.
	Warnings []string
}

// Repository describes the checkout the snapshot was taken in.
type Repository struct {
	// CurrentBranch is the branch checked out when the snapshot was taken
	// (not necessarily a branch containing the commit), or nil when HEAD
	// was detached.
	CurrentBranch *string
}

// Commit is the commit's own metadata.
type Commit struct {
	SHA     string   // full object name
	Parents []string // first parent first; empty for a root commit
	Message string   // full message
}

// Comparison says what the files were compared against.
type Comparison struct {
	Strategy string
	BaseSHA  *string // nil for StrategyEmptyTree
}

// File is one changed file.
type File struct {
	Status  string  // git's letter: A, M, D, R, T, ...
	OldPath *string // nil when added
	NewPath *string // nil when deleted
	// Additions and Deletions are nil when git cannot count lines (binary).
	Additions      *int
	Deletions      *int
	Binary         bool
	Patch          *string // nil exactly when OmittedReason is set
	PatchTruncated bool
	OmittedReason  *string
}

// Path is the path a file is known by in the commit: the new path, or the
// old one for a deleted file.
func (f *File) Path() string {
	if f.NewPath != nil {
		return *f.NewPath
	}
	return *f.OldPath
}

// Summary gives the totals.
type Summary struct {
	ChangedFiles  int  // files in the comparison
	IncludedFiles int  // len(Files)
	OmittedFiles  int  // changed files left out of Files by the file limit
	Truncated     bool // a limit cut the file list or any patch
}
