package gitrepo_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

var bigLimits = gitrepo.DiffOptions{MaxFiles: 1000, MaxFilePatchBytes: 1 << 20, MaxTotalPatchBytes: 8 << 20}

func diffHead(t *testing.T, r *testutil.Repo, opt gitrepo.DiffOptions) *gitrepo.Diff {
	t.Helper()
	repo := open(t, r.Dir)
	ctx := context.Background()
	head := r.Head()
	c, err := repo.ReadCommit(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	base := ""
	if len(c.Parents) == 0 {
		if base, err = repo.EmptyTree(ctx); err != nil {
			t.Fatal(err)
		}
	} else {
		base = c.Parents[0]
	}
	d, err := repo.Diff(ctx, base, head, opt)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// byPath indexes changes by "old -> new".
func byPath(d *gitrepo.Diff) map[string]gitrepo.FileChange {
	m := map[string]gitrepo.FileChange{}
	for _, f := range d.Files {
		m[f.OldPath+" -> "+f.NewPath] = f
	}
	return m
}

func TestDiffStatusesAndPaths(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("keep.txt", "a\nb\nc\n")
	r.Write("gone.txt", "bye\n")
	r.Write("old name.txt", "line1\nline2\nline3\nline4\nline5\nline6\n")
	r.Write("typ", "regular file\n")
	r.CommitAll("init")

	r.Write("keep.txt", "a\nB\nc\n")
	r.Git("rm", "-q", "gone.txt")
	r.Git("mv", "old name.txt", "新しい 名前.txt")
	r.Write("新しい 名前.txt", "line1\nline2\nline3\nline4\nline5\nline6 changed\n")
	os.Remove(filepath.Join(r.Dir, "typ"))
	if err := os.Symlink("target", filepath.Join(r.Dir, "typ")); err != nil {
		t.Fatal(err)
	}
	r.Write("dir/added.go", "package dir\n")
	r.CommitAll("changes")

	d := diffHead(t, r, bigLimits)
	if d.TotalFiles != 5 || len(d.Files) != 5 {
		t.Fatalf("TotalFiles = %d, Files = %d, want 5", d.TotalFiles, len(d.Files))
	}
	m := byPath(d)
	check := func(key, status string, add, del int, patchHas ...string) {
		t.Helper()
		f, ok := m[key]
		if !ok {
			t.Fatalf("no change %q; have %v", key, keys(m))
		}
		if f.Status != status || f.Additions != add || f.Deletions != del || f.Binary {
			t.Errorf("%s: status %s +%d -%d binary=%v; want %s +%d -%d", key, f.Status, f.Additions, f.Deletions, f.Binary, status, add, del)
		}
		if f.PatchState != gitrepo.PatchKept {
			t.Errorf("%s: PatchState = %v", key, f.PatchState)
		}
		for _, s := range patchHas {
			if !strings.Contains(f.Patch, s) {
				t.Errorf("%s: patch lacks %q:\n%s", key, s, f.Patch)
			}
		}
	}
	check("keep.txt -> keep.txt", "M", 1, 1, "diff --git a/keep.txt b/keep.txt\n", "-b\n+B\n")
	check("gone.txt -> ", "D", 0, 1, "deleted file mode", "-bye\n")
	check("old name.txt -> 新しい 名前.txt", "R", 1, 1, "rename from old name.txt\nrename to 新しい 名前.txt\n", "+line6 changed\n")
	check(" -> dir/added.go", "A", 1, 0, "new file mode", "+package dir\n")
	// A type change is printed by git as a deletion plus a creation.
	check("typ -> typ", "T", 1, 1, "deleted file mode 100644", "-regular file\n", "new file mode 120000", "+target")
	if n := countSections(m["typ -> typ"].Patch); n != 2 {
		t.Errorf("type change patch has %d sections, want 2", n)
	}
}

func keys(m map[string]gitrepo.FileChange) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	return k
}

func TestDiffUnusualFileNames(t *testing.T) {
	names := []string{
		"日本語 と 空白.txt",
		"-starts-with-dash.txt",
		":(glob)*.txt",
		`quote " and backslash \.txt`,
	}
	if runtime.GOOS != "windows" {
		names = append(names,
			"tab\there.txt",
			"new\nline.txt",
			// A name that contains a patch header must not split the patch.
			"x\ndiff --git a",
			"trailing space ",
		)
	}
	r := testutil.NewRepo(t)
	r.Write("base.txt", "base\n")
	r.CommitAll("base")
	for i, n := range names {
		r.Write(n, strings.Repeat("content line\n", i+1))
	}
	r.CommitAll("weird names")

	d := diffHead(t, r, bigLimits)
	if len(d.Files) != len(names) {
		t.Fatalf("got %d files, want %d", len(d.Files), len(names))
	}
	got := map[string]gitrepo.FileChange{}
	for _, f := range d.Files {
		got[f.NewPath] = f
	}
	for i, n := range names {
		f, ok := got[n]
		if !ok {
			t.Errorf("missing %q", n)
			continue
		}
		if f.Status != "A" || f.OldPath != "" || f.Additions != i+1 {
			t.Errorf("%q: status %s old %q +%d", n, f.Status, f.OldPath, f.Additions)
		}
		if strings.Count(f.Patch, "\n+content line") != i+1 || countSections(f.Patch) != 1 {
			t.Errorf("%q: patch does not belong to this file:\n%s", n, f.Patch)
		}
	}
}

// countSections counts lines that start a patch section. (A quoted path in
// a header may itself contain the text "diff --git ".)
func countSections(patch string) int {
	n := 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			n++
		}
	}
	return n
}

func TestDiffBinaryFile(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("img.bin", "\x00\x01\x02binary")
	r.CommitAll("add binary")
	r.Write("img.bin", "\x00\x01\x02binary changed")
	r.Write("text.txt", "text\n")
	r.CommitAll("change binary")
	m := byPath(diffHead(t, r, bigLimits))
	f := m["img.bin -> img.bin"]
	if !f.Binary || f.PatchState != gitrepo.PatchOmittedBinary || f.Patch != "" {
		t.Fatalf("binary change = %+v", f)
	}
	if t2 := m[" -> text.txt"]; t2.PatchState != gitrepo.PatchKept || !strings.Contains(t2.Patch, "+text") {
		t.Fatalf("text file after binary = %+v", t2)
	}
}

func TestDiffEmptyCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("f", "x\n")
	r.CommitAll("first")
	r.CommitAll("empty")
	d := diffHead(t, r, bigLimits)
	if d.TotalFiles != 0 || d.Files == nil || len(d.Files) != 0 {
		t.Fatalf("empty commit diff = %+v", d)
	}
}

func TestDiffFileLimitStopsEarly(t *testing.T) {
	r := testutil.NewRepo(t)
	for i := 0; i < 30; i++ {
		r.Write(filepath.Join("files", string(rune('a'+i%26))+strings.Repeat("x", i)), "line\n")
	}
	r.CommitAll("many")
	opt := bigLimits
	opt.MaxFiles = 7
	d := diffHead(t, r, opt)
	if d.TotalFiles != 30 || len(d.Files) != 7 {
		t.Fatalf("TotalFiles = %d, len(Files) = %d", d.TotalFiles, len(d.Files))
	}
	for _, f := range d.Files {
		if f.PatchState != gitrepo.PatchKept || !strings.Contains(f.Patch, "+line") {
			t.Errorf("%s: %+v", f.NewPath, f)
		}
	}
}

func TestDiffPatchLimits(t *testing.T) {
	r := testutil.NewRepo(t)
	// "あ" is 3 bytes, so a byte limit usually falls inside a character.
	big := strings.Repeat("あいうえお かきくけこ\n", 400) // ~12 KiB
	r.Write("a_big.txt", big)
	r.Write("b_small.txt", "small\n")
	r.Write("c_big.txt", big)
	r.Write("d_after_limit.txt", "late\n")
	r.Write("e.bin", "\x00bin")
	r.CommitAll("patches")

	opt := gitrepo.DiffOptions{MaxFiles: 100, MaxFilePatchBytes: 5000, MaxTotalPatchBytes: 6000}
	m := byPath(diffHead(t, r, opt))
	a, b, c, dd, e := m[" -> a_big.txt"], m[" -> b_small.txt"], m[" -> c_big.txt"], m[" -> d_after_limit.txt"], m[" -> e.bin"]

	if !a.PatchTruncated || len(a.Patch) > 5000 || len(a.Patch) < 4990 || !utf8.ValidString(a.Patch) || a.PatchInvalidUTF8 {
		t.Errorf("a: truncated=%v len=%d valid=%v invalid=%v", a.PatchTruncated, len(a.Patch), utf8.ValidString(a.Patch), a.PatchInvalidUTF8)
	}
	if a.Additions != 400 { // counts are git's, not those of the truncated text
		t.Errorf("a: additions = %d", a.Additions)
	}
	if b.PatchTruncated || !strings.Contains(b.Patch, "+small\n") {
		t.Errorf("b: %+v", b)
	}
	// c gets what is left of the total budget.
	if !c.PatchTruncated || c.PatchState != gitrepo.PatchKept || len(a.Patch)+len(b.Patch)+len(c.Patch) > 6000 || !utf8.ValidString(c.Patch) {
		t.Errorf("c: state=%v truncated=%v len=%d", c.PatchState, c.PatchTruncated, len(c.Patch))
	}
	if dd.PatchState != gitrepo.PatchOmittedTotalLimit || dd.Patch != "" || dd.Additions != 1 {
		t.Errorf("d: %+v", dd)
	}
	if e.PatchState != gitrepo.PatchOmittedBinary {
		t.Errorf("e: state = %v, want binary even after the budget is used up", e.PatchState)
	}
}

func TestDiffHugeSingleLine(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("min.js", strings.Repeat("x", 3<<20)+"\n") // one 3 MiB line
	r.Write("z.txt", "after\n")
	r.CommitAll("minified")
	opt := gitrepo.DiffOptions{MaxFiles: 100, MaxFilePatchBytes: 64 << 10, MaxTotalPatchBytes: 512 << 10}
	m := byPath(diffHead(t, r, opt))
	f := m[" -> min.js"]
	if !f.PatchTruncated || len(f.Patch) != 64<<10 {
		t.Errorf("min.js: truncated=%v len=%d", f.PatchTruncated, len(f.Patch))
	}
	if z := m[" -> z.txt"]; z.PatchTruncated || !strings.Contains(z.Patch, "+after\n") {
		t.Errorf("z.txt: %+v", z)
	}
}

func TestDiffOmitPatchFilter(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write(".env", "API_KEY=super-secret-value\n")
	r.Write("app.go", "package app\n")
	r.CommitAll("with secret")
	opt := bigLimits
	opt.OmitPatch = func(o, n string) bool { return o == ".env" || n == ".env" }
	m := byPath(diffHead(t, r, opt))
	env := m[" -> .env"]
	if env.PatchState != gitrepo.PatchOmittedByFilter || env.Patch != "" || env.Additions != 1 {
		t.Fatalf(".env: %+v", env)
	}
	if app := m[" -> app.go"]; app.PatchState != gitrepo.PatchKept || strings.Contains(app.Patch, "secret") {
		t.Fatalf("app.go: %+v", app)
	}
}

func TestDiffInvalidUTF8(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS file systems reject file names that are not valid UTF-8")
	}
	r := testutil.NewRepo(t)
	r.Write("latin1.txt", "caf\xe9\n") // Latin-1 content
	r.Write("name-\xff.txt", "x\n")
	r.CommitAll("invalid utf8")
	m := byPath(diffHead(t, r, bigLimits))
	f := m[" -> latin1.txt"]
	if !f.PatchInvalidUTF8 || !strings.Contains(f.Patch, "+caf\uFFFD\n") {
		t.Errorf("latin1.txt: invalid=%v patch=%q", f.PatchInvalidUTF8, f.Patch)
	}
	g, ok := m[" -> name-\xff.txt"]
	if !ok || !g.PatchInvalidUTF8 {
		t.Errorf("raw path not preserved or not flagged: %v %+v", keys(m), g)
	}
}

// TestDiffIgnoresWorkingTreeAttributes checks that an uncommitted
// .gitattributes does not change how a commit is rendered (Git 2.40+).
func TestDiffIgnoresWorkingTreeAttributes(t *testing.T) {
	r := testutil.NewRepo(t)
	repo := open(t, r.Dir)
	var major, minor int
	if _, err := fmt.Sscanf(repo.GitVersion, "git version %d.%d", &major, &minor); err != nil || major == 2 && minor < 40 {
		t.Skipf("%s does not support GIT_ATTR_SOURCE", repo.GitVersion)
	}
	r.Write("notes.txt", "hello\n")
	r.CommitAll("text")
	r.Write(".gitattributes", "*.txt binary\n") // not committed
	f := byPath(diffHead(t, r, bigLimits))[" -> notes.txt"]
	if f.Binary || !strings.Contains(f.Patch, "+hello\n") {
		t.Fatalf("working tree .gitattributes leaked into the diff: %+v", f)
	}
}

// TestDiffGitFailure removes a blob that the commit needs. git must fail
// (the working tree still has the same content, but it must not be used).
func TestDiffGitFailure(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("f.txt", "one\n")
	r.CommitAll("one")
	r.Write("f.txt", "two\n")
	head := r.CommitAll("two")
	// Delete the new blob so that git diff-tree fails while producing the patch.
	blob := strings.TrimSpace(r.Git("rev-parse", "HEAD:f.txt"))
	if err := os.Remove(filepath.Join(r.Dir, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	repo := open(t, r.Dir)
	ctx := context.Background()
	c, err := repo.ReadCommit(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Diff(ctx, c.Parents[0], head, bigLimits)
	gerr, ok := err.(*gitrepo.Error)
	if !ok || gerr.ExitCode <= 0 || !strings.Contains(gerr.Stderr, blob[:7]) {
		t.Fatalf("err = %#v, want a *gitrepo.Error naming the missing object", err)
	}
}
