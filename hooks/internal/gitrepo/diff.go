package gitrepo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DiffOptions bounds how much of a diff Diff keeps. The caller supplies the
// numbers (see event.DefaultLimits); Diff enforces them while it streams
// git's output, so a huge diff is never held in memory.
type DiffOptions struct {
	// MaxFiles is the number of changed files whose details are returned.
	// Files beyond it are only counted.
	MaxFiles int
	// MaxFilePatchBytes caps the patch text kept for one file.
	MaxFilePatchBytes int
	// MaxTotalPatchBytes caps the patch text kept for all files together.
	MaxTotalPatchBytes int
	// OmitPatch reports whether a file's patch text must not be kept (for
	// example because the path looks like it holds secrets). oldPath or
	// newPath is "" when that side does not exist. The text still flows
	// through git's output pipe but is discarded without being stored.
	OmitPatch func(oldPath, newPath string) bool
}

// PatchState says whether FileChange.Patch holds text and, if not, why.
type PatchState int

const (
	// PatchKept means Patch holds the file's patch (possibly truncated).
	PatchKept PatchState = iota
	// PatchOmittedBinary means git treats the file as binary.
	PatchOmittedBinary
	// PatchOmittedByFilter means DiffOptions.OmitPatch rejected the file.
	PatchOmittedByFilter
	// PatchOmittedTotalLimit means earlier files used up MaxTotalPatchBytes.
	PatchOmittedTotalLimit
)

// FileChange is one changed file as reported by git diff-tree.
type FileChange struct {
	// Status is git's change letter: A, M, D, R, T (type change), ...
	Status string
	// OldPath is "" for an added file and NewPath is "" for a deleted one.
	// Both are repository-relative and exactly as git printed them.
	OldPath, NewPath string
	OldMode, NewMode uint32
	// Binary is true when git reports no line counts for the file.
	Binary bool
	// Additions and Deletions are line counts; meaningless when Binary.
	Additions, Deletions int

	PatchState PatchState
	// Patch is unified diff text, valid UTF-8, when PatchState is PatchKept.
	Patch string
	// PatchTruncated reports that Patch was cut at a size limit.
	PatchTruncated bool
	// PatchInvalidUTF8 reports that bytes of the patch that were not valid
	// UTF-8 were replaced with U+FFFD.
	PatchInvalidUTF8 bool

	sections int // "diff --git" sections git prints for this change
}

// Diff is the difference between two trees.
type Diff struct {
	// Files holds the first DiffOptions.MaxFiles changes in git's order
	// (sorted by path).
	Files []FileChange
	// TotalFiles is the number of changed files git reported.
	TotalFiles int
	// GitStderr is what git printed on standard error, for example that
	// rename detection was skipped because there are too many files.
	GitStderr string
}

// Diff compares the trees of base and target (object names of commits or
// trees) with rename detection.
//
// A single git diff-tree process prints, in this order and all from the same
// internal list of file pairs: one NUL-terminated --raw record per file, one
// --numstat record per file, a NUL, and then the patch. Records are matched
// by position, and every patch section header is checked against the header
// git must print for the expected file, so a patch is never attributed to
// the wrong file even when paths contain spaces, tabs, newlines or text that
// looks like a diff header (git C-quotes such paths in patch headers).
func (r *Repo) Diff(ctx context.Context, base, target string, opt DiffOptions) (*Diff, error) {
	if !IsObjectID(base) || !IsObjectID(target) {
		return nil, fmt.Errorf("diff needs full object names, got %q and %q", base, target)
	}
	if opt.MaxFiles < 0 || opt.MaxFilePatchBytes < 0 || opt.MaxTotalPatchBytes < 0 {
		return nil, errors.New("diff limits must not be negative")
	}
	// diff-tree loads the index and, for a file whose index entry matches
	// the blob, may read the content from the working tree instead of the
	// object database. Pointing it at an index file that does not exist (an
	// empty index) rules that out, so only committed objects are read.
	// GIT_ATTR_SOURCE (Git 2.40+; ignored by older versions) makes it read
	// .gitattributes from the commit instead of the working tree as well.
	noIndex, err := os.MkdirTemp("", "commitcoach-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(noIndex)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(noIndex, "index"), "GIT_ATTR_SOURCE=" + target}

	s, err := r.run.Start(ctx, env, "diff-tree", "-r", "-z", "--raw", "--numstat", "--patch",
		"--find-renames", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/",
		// No external diff programs or textconv filters: nothing but git runs.
		// Without --binary, binary files get a one-line note instead of data.
		"--no-color", "--no-ext-diff", "--no-textconv",
		"--end-of-options", base, target)
	if err != nil {
		return nil, err
	}
	p := &diffParser{br: bufio.NewReaderSize(s, 64<<10), opt: opt}
	d, complete, perr := p.parse()
	if perr == nil && !complete {
		// Everything that will be returned has been read. The rest of the
		// output belongs to files beyond the limits, so stop git.
		s.Abort()
		d.GitStderr = s.Stderr()
		return d, nil
	}
	werr := s.Finish()
	if werr != nil && (perr == nil || ctx.Err() != nil || exitedWithFailure(werr)) {
		// git failed or timed out; a parse error is only a symptom of that.
		return nil, werr
	}
	if perr != nil {
		return nil, fmt.Errorf("reading output of git diff-tree: %w", perr)
	}
	d.GitStderr = s.Stderr()
	return d, nil
}

// maxRecordBytes bounds one NUL-terminated record or header line (paths).
const maxRecordBytes = 1 << 20

var diffHeaderPrefix = []byte("diff --git ")

type diffParser struct {
	br  *bufio.Reader
	opt DiffOptions
}

// parse reads git's output. complete is false when it stopped before the
// end because nothing after that point is needed.
func (p *diffParser) parse() (d *Diff, complete bool, err error) {
	d = &Diff{Files: []FileChange{}}

	// --raw records start with ':'; the first --numstat record does not.
	for {
		b, err := p.br.Peek(1)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		if b[0] != ':' {
			break
		}
		fc, err := p.readRaw()
		if err != nil {
			return nil, false, err
		}
		d.TotalFiles++
		if len(d.Files) < p.opt.MaxFiles {
			d.Files = append(d.Files, fc)
		}
	}

	// One --numstat record per --raw record, in the same order.
	for i := 0; i < d.TotalFiles; i++ {
		ns, err := p.readNumstat()
		if err != nil {
			return nil, false, err
		}
		if i < len(d.Files) {
			if err := d.Files[i].applyNumstat(ns); err != nil {
				return nil, false, err
			}
		}
	}

	if d.TotalFiles == 0 { // an empty diff produces no output at all
		if _, err := p.br.Peek(1); !errors.Is(err, io.EOF) {
			return nil, false, errors.New("unexpected output after an empty file list")
		}
		return d, true, nil
	}

	// The patch is separated from the records by the -z line terminator.
	if sep, err := p.br.ReadByte(); err != nil || sep != 0 {
		return nil, false, errors.New("missing separator before the patch")
	}
	complete, err = p.readPatches(d.Files, d.TotalFiles > len(d.Files))
	if err != nil {
		return nil, false, err
	}
	return d, complete, nil
}

// readField reads one NUL-terminated field.
func (p *diffParser) readField() (string, error) {
	var acc []byte
	for {
		frag, err := p.br.ReadSlice(0)
		if len(acc)+len(frag) > maxRecordBytes {
			return "", errors.New("record too long")
		}
		acc = append(acc, frag...)
		switch {
		case err == nil:
			return string(acc[:len(acc)-1]), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return "", io.ErrUnexpectedEOF
		default:
			return "", err
		}
	}
}

// readRaw reads ":<old mode> <new mode> <old oid> <new oid> <status>" NUL
// <path> NUL, with a second path for renames and copies.
func (p *diffParser) readRaw() (FileChange, error) {
	var fc FileChange
	head, err := p.readField()
	if err != nil {
		return fc, err
	}
	f := strings.Fields(head) // this field contains no paths
	if len(f) != 5 || len(f[0]) < 2 || f[0][0] != ':' || f[4] == "" {
		return fc, fmt.Errorf("malformed raw diff record %q", head)
	}
	oldMode, err1 := strconv.ParseUint(f[0][1:], 8, 32)
	newMode, err2 := strconv.ParseUint(f[1], 8, 32)
	if err1 != nil || err2 != nil {
		return fc, fmt.Errorf("malformed file modes in %q", head)
	}
	fc.OldMode, fc.NewMode = uint32(oldMode), uint32(newMode)
	fc.Status = f[4][:1] // "R086" -> "R": the similarity score is dropped

	path, err := p.readField()
	if err != nil {
		return fc, err
	}
	switch fc.Status {
	case "R", "C":
		newPath, err := p.readField()
		if err != nil {
			return fc, err
		}
		fc.OldPath, fc.NewPath = path, newPath
	case "A":
		fc.NewPath = path
	case "D":
		fc.OldPath = path
	default:
		fc.OldPath, fc.NewPath = path, path
	}

	// git prints a change between file types (regular file, symlink,
	// submodule) as a deletion followed by a creation.
	const typeMask = 0o170000
	fc.sections = 1
	if fc.OldMode != 0 && fc.NewMode != 0 && fc.OldMode&typeMask != fc.NewMode&typeMask {
		fc.sections = 2
	}
	return fc, nil
}

type numstat struct {
	binary   bool
	add, del int
	path     string // set for records with one path
	oldPath  string // set for renames and copies
	newPath  string
	twoPaths bool
}

// readNumstat reads "<added> TAB <deleted> TAB <path>" NUL, or for renames
// and copies "<added> TAB <deleted> TAB" NUL <old> NUL <new> NUL. Binary
// files have "-" for both counts.
func (p *diffParser) readNumstat() (numstat, error) {
	var ns numstat
	rec, err := p.readField()
	if err != nil {
		return ns, err
	}
	parts := strings.SplitN(rec, "\t", 3) // the path itself may contain tabs
	if len(parts) != 3 {
		return ns, fmt.Errorf("malformed numstat record %q", rec)
	}
	if parts[0] == "-" && parts[1] == "-" {
		ns.binary = true
	} else {
		var err1, err2 error
		ns.add, err1 = strconv.Atoi(parts[0])
		ns.del, err2 = strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return ns, fmt.Errorf("malformed numstat counts %q", rec)
		}
	}
	if parts[2] != "" {
		ns.path = parts[2]
		return ns, nil
	}
	ns.twoPaths = true
	if ns.oldPath, err = p.readField(); err != nil {
		return ns, err
	}
	if ns.newPath, err = p.readField(); err != nil {
		return ns, err
	}
	return ns, nil
}

func (fc *FileChange) applyNumstat(ns numstat) error {
	switch {
	case fc.Status == "R" || fc.Status == "C":
		if !ns.twoPaths || ns.oldPath != fc.OldPath || ns.newPath != fc.NewPath {
			return errors.New("raw and numstat records of git diff-tree disagree")
		}
	case ns.twoPaths || ns.path != fc.path():
		return errors.New("raw and numstat records of git diff-tree disagree")
	}
	fc.Binary = ns.binary
	fc.Additions, fc.Deletions = ns.add, ns.del
	return nil
}

// path returns the path of a change that has only one.
func (fc *FileChange) path() string {
	if fc.NewPath != "" {
		return fc.NewPath
	}
	return fc.OldPath
}

// expectedHeader is the first line git prints for each patch section of fc.
func (fc *FileChange) expectedHeader() string {
	oldName, newName := fc.OldPath, fc.NewPath
	if oldName == "" {
		oldName = newName
	}
	if newName == "" {
		newName = oldName
	}
	return "diff --git " + cQuote("a/"+oldName) + " " + cQuote("b/"+newName) + "\n"
}

// cQuote quotes a name the way git does in patch headers when
// core.quotePath is false: if it contains a control character, DEL, a double
// quote or a backslash, the whole name is enclosed in double quotes and those
// bytes are escaped. Other bytes, including non-ASCII ones, are kept as is.
func cQuote(s string) string {
	need := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c == '"' || c == '\\' {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// readPatches assigns the patch sections to files in order. Inside a
// section every line starts with a fixed prefix (" ", "+", "-", "\", "@@",
// or a keyword such as "index"), and header paths never contain a raw
// newline, so a line starting with "diff --git " always starts a section.
// Each such line is additionally compared with the header expected for the
// file it is assigned to. moreFiles says whether git reported files beyond
// those in files.
func (p *diffParser) readPatches(files []FileChange, moreFiles bool) (complete bool, err error) {
	remaining := p.opt.MaxTotalPatchBytes
	next := 0 // index of the file whose patch starts next
	cur := -1 // index of the file whose patch is being read
	sectionsLeft := 0
	var col *collector // nil: discard the current file's text
	atLineStart := true

	finishCurrent := func() {
		if cur >= 0 && col != nil {
			f := &files[cur]
			f.Patch, f.PatchTruncated, f.PatchInvalidUTF8 = col.finish()
			remaining -= len(f.Patch)
		}
		col = nil
	}

	for {
		frag, err := p.br.ReadSlice('\n')
		if atLineStart && bytes.HasPrefix(frag, diffHeaderPrefix) {
			line := frag
			if errors.Is(err, bufio.ErrBufferFull) { // very long header: collect it whole
				buf := append([]byte(nil), frag...)
				for errors.Is(err, bufio.ErrBufferFull) {
					frag, err = p.br.ReadSlice('\n')
					if len(buf)+len(frag) > maxRecordBytes {
						return false, errors.New("patch header too long")
					}
					buf = append(buf, frag...)
				}
				line = buf
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return false, err
			}
			if sectionsLeft == 0 {
				finishCurrent()
				if next == len(files) {
					if !moreFiles {
						return false, errors.New("git printed more patch sections than files")
					}
					// The remaining sections belong to files beyond MaxFiles.
					return false, nil
				}
				if remaining <= 0 {
					// No further patch text can be kept; nothing more to read.
					for i := next; i < len(files); i++ {
						p.plan(&files[i], 0)
					}
					return false, nil
				}
				cur, next = next, next+1
				sectionsLeft = files[cur].sections
				if limit := p.plan(&files[cur], remaining); files[cur].PatchState == PatchKept {
					col = newCollector(limit)
				}
			}
			if want := files[cur].expectedHeader(); string(line) != want {
				return false, fmt.Errorf("patch section header %q does not match the expected %q", line, want)
			}
			sectionsLeft--
			if col != nil {
				col.write(line)
			}
		} else if len(frag) > 0 {
			if cur < 0 {
				return false, errors.New("patch text before the first section header")
			}
			if col != nil {
				col.write(frag)
			}
		}

		atLineStart = err == nil
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			finishCurrent()
			if next != len(files) || sectionsLeft != 0 {
				return false, fmt.Errorf("git printed patches for %d of %d files", next, len(files))
			}
			return true, nil
		default:
			return false, err
		}
	}
}

// plan decides what happens to f's patch text, before it is read, and
// returns how many bytes may be kept.
func (p *diffParser) plan(f *FileChange, remaining int) int {
	switch {
	case f.Binary:
		f.PatchState = PatchOmittedBinary
	case p.opt.OmitPatch != nil && p.opt.OmitPatch(f.OldPath, f.NewPath):
		f.PatchState = PatchOmittedByFilter
	case remaining <= 0:
		f.PatchState = PatchOmittedTotalLimit
	default:
		f.PatchState = PatchKept
		return min(p.opt.MaxFilePatchBytes, remaining)
	}
	return 0
}

// collector keeps up to limit bytes of one file's patch. It stores a few
// bytes more than the limit so that finish can cut at a character boundary
// without mistaking a character split by the cap for invalid UTF-8.
type collector struct {
	limit    int
	buf      []byte
	overflow bool // bytes were dropped
}

func newCollector(limit int) *collector {
	return &collector{limit: limit, buf: make([]byte, 0, min(limit+utf8.UTFMax, 4096))}
}

func (c *collector) write(p []byte) {
	room := c.limit + utf8.UTFMax - len(c.buf)
	if len(p) > room {
		c.overflow = true
		if room <= 0 {
			return
		}
		p = p[:room]
	}
	c.buf = append(c.buf, p...)
}

func (c *collector) finish() (text string, truncated, invalidUTF8 bool) {
	b := c.buf
	if c.overflow {
		b = dropIncompleteRune(b)
	}
	if !utf8.Valid(b) {
		invalidUTF8 = true
		b = []byte(strings.ToValidUTF8(string(b), "�"))
	}
	truncated = c.overflow
	if len(b) > c.limit {
		n := c.limit
		for n > 0 && !utf8.RuneStart(b[n]) {
			n--
		}
		b = b[:n]
		truncated = true
	}
	return string(b), truncated, invalidUTF8
}

// dropIncompleteRune removes a multi-byte character that was cut off at the
// end of b.
func dropIncompleteRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}
