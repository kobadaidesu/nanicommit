package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Payload is the JSON document commitcoach publishes for one commit. See
// docs/commit-payload.schema.json.
type Payload struct {
	// RepositoryID is the UUID under which the backend knows the repository.
	RepositoryID string `json:"repository_id"`
	// CommitSHA is the full object name of the commit.
	CommitSHA string `json:"commit_sha"`
	// Branch is the branch checked out when the payload was made, or null
	// when HEAD was detached. It is not necessarily a branch containing the
	// commit.
	Branch *string `json:"branch"`
	// Message is the full commit message.
	Message string `json:"message"`
	// Files lists the repository-relative paths that Diff covers, in the
	// same order: the new path, or the old one for a deleted file.
	Files []string `json:"files"`
	// Diff is the unified diff of those files against the first parent (or
	// the empty tree for a root commit).
	Diff string `json:"diff"`
}

// ErrLimitExceeded is wrapped by NewPayload errors caused by the size or
// file-count limits.
var ErrLimitExceeded = errors.New("limit exceeded")

// NewPayload makes the published document from a snapshot.
//
// Files whose text was deliberately left out of the snapshot (paths that
// look sensitive, binary files) are left out of Files and Diff alike, so the
// two always describe the same files; notes says which files were left out,
// by path only. The snapshot's warnings are added to notes.
//
// A snapshot cut short by a limit is an error wrapping ErrLimitExceeded:
// the payload has no field to mark it incomplete, so part of a commit is
// never presented as the whole.
func NewPayload(repositoryID string, ev *Event) (p *Payload, notes []string, err error) {
	lim := ev.Limits
	if ev.Summary.OmittedFiles > 0 {
		return nil, nil, fmt.Errorf("%w: the commit changes %d files, more than the limit of %d", ErrLimitExceeded, ev.Summary.ChangedFiles, lim.MaxFiles)
	}
	p = &Payload{
		RepositoryID: repositoryID,
		CommitSHA:    ev.Commit.SHA,
		Branch:       ev.Repository.CurrentBranch,
		Message:      ev.Commit.Message,
		Files:        []string{},
	}
	var diff strings.Builder
	seen := map[string]bool{}
	for i := range ev.Files {
		f := &ev.Files[i]
		if f.OmittedReason != nil {
			switch *f.OmittedReason {
			case OmittedSensitivePath:
				notes = append(notes, fmt.Sprintf("left out %s: the path looks like it holds secrets, so its content is not recorded", describe(f)))
				continue
			case OmittedBinary:
				notes = append(notes, fmt.Sprintf("left out %s: binary file", describe(f)))
				continue
			default:
				return nil, nil, fmt.Errorf("%w: the diff is larger than %d bytes in total (at %s)", ErrLimitExceeded, lim.MaxTotalPatchBytes, describe(f))
			}
		}
		if f.PatchTruncated {
			// The reader keeps min(per-file limit, what is left of the total).
			if diff.Len()+lim.MaxFilePatchBytes <= lim.MaxTotalPatchBytes {
				return nil, nil, fmt.Errorf("%w: the diff of %s is larger than %d bytes", ErrLimitExceeded, describe(f), lim.MaxFilePatchBytes)
			}
			return nil, nil, fmt.Errorf("%w: the diff is larger than %d bytes in total (at %s)", ErrLimitExceeded, lim.MaxTotalPatchBytes, describe(f))
		}
		if path := f.Path(); !seen[path] {
			seen[path] = true
			p.Files = append(p.Files, path)
		}
		diff.WriteString(*f.Patch)
	}
	p.Diff = diff.String()
	notes = append(notes, ev.Warnings...)
	return p, notes, nil
}

// describe names a file for notes: "a.txt", or "old.txt -> new.txt" for a
// rename.
func describe(f *File) string {
	if f.OldPath != nil && f.NewPath != nil && *f.OldPath != *f.NewPath {
		return *f.OldPath + " -> " + *f.NewPath
	}
	return f.Path()
}

// Marshal encodes p as indented JSON followed by a newline. HTML characters
// are not escaped, so code in the diff stays readable.
func Marshal(p *Payload) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
