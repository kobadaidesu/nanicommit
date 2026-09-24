package event_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/event"
	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestMain(m *testing.M) { testutil.Main(m, nil) }

// testID is a made-up repository ID used by the tests.
const testID = "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"

// build returns the internal snapshot of rev and checks its invariants.
func build(t *testing.T, r *testutil.Repo, rev string, limits event.Limits) *event.Event {
	t.Helper()
	ev, err := buildErr(r, rev, limits)
	if err != nil {
		t.Fatal(err)
	}
	checkEvent(t, rev, ev)
	return ev
}

func buildErr(r *testutil.Repo, rev string, limits event.Limits) (*event.Event, error) {
	ctx := context.Background()
	repo, err := gitrepo.Open(ctx, r.Dir)
	if err != nil {
		return nil, err
	}
	oid, err := repo.ResolveCommit(ctx, rev)
	if err != nil {
		return nil, err
	}
	return event.Build(ctx, repo, oid, limits)
}

// publish returns the payload of rev with the default limits, its JSON text
// (checked against the schema rules) and the notes.
func publish(t *testing.T, r *testutil.Repo, rev string) (*event.Payload, string, []string) {
	t.Helper()
	p, notes, err := event.NewPayload(testID, build(t, r, rev, event.DefaultLimits))
	if err != nil {
		t.Fatal(err)
	}
	data, err := event.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	checkPayload(t, rev, data)
	return p, string(data), notes
}

func file(t *testing.T, ev *event.Event, path string) event.File {
	t.Helper()
	for _, f := range ev.Files {
		if f.Path() == path {
			return f
		}
	}
	t.Fatalf("no file %q in the snapshot", path)
	return event.File{}
}

func TestRootCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("src/add.go", "package calc\n")
	r.Write("README.md", "# demo\n")
	head := r.CommitAll("Initial commit")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	if ev.Comparison.Strategy != "empty_tree" || ev.Comparison.BaseSHA != nil || ev.Commit.Parents == nil || len(ev.Commit.Parents) != 0 {
		t.Errorf("comparison = %+v, parents = %#v", ev.Comparison, ev.Commit.Parents)
	}
	p, text, notes := publish(t, r, "HEAD")
	if p.RepositoryID != testID || p.CommitSHA != head || p.Message != "Initial commit\n" || p.Branch == nil || *p.Branch != "main" {
		t.Errorf("payload = %+v", p)
	}
	if strings.Join(p.Files, ",") != "README.md,src/add.go" {
		t.Errorf("files = %v", p.Files)
	}
	for _, want := range []string{"diff --git a/README.md b/README.md\nnew file mode 100644\n", "+# demo\n", "diff --git a/src/add.go b/src/add.go\n", "+package calc\n"} {
		if !strings.Contains(p.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, text)
		}
	}
	if len(notes) != 0 {
		t.Errorf("notes = %v", notes)
	}
}

func TestNormalCommitComparesWithFirstParent(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a - b\n}\n")
	r.Write("src/old.go", "package calc\n")
	parent := r.CommitAll("first")
	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n")
	r.Write("src/sub.go", "package calc\n\nfunc sub(a, b int) int { return a - b }\n")
	r.Git("rm", "-q", "src/old.go")
	head := r.CommitAll("Fix addition\n\nand add sub")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	if ev.Comparison.Strategy != "first_parent" || *ev.Comparison.BaseSHA != parent {
		t.Errorf("comparison = %+v", ev.Comparison)
	}
	p, _, _ := publish(t, r, "HEAD")
	if p.CommitSHA != head || p.Message != "Fix addition\n\nand add sub\n" {
		t.Errorf("payload = %+v", p)
	}
	// Changed, deleted (old path) and added files, in git's path order.
	if strings.Join(p.Files, ",") != "src/add.go,src/old.go,src/sub.go" {
		t.Errorf("files = %v", p.Files)
	}
	for _, want := range []string{"-\treturn a - b\n+\treturn a + b\n", "deleted file mode 100644\n", "-package calc\n", "+func sub(a, b int) int { return a - b }\n"} {
		if !strings.Contains(p.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, p.Diff)
		}
	}
	if countSections(p.Diff) != 3 {
		t.Errorf("diff has %d sections, want 3", countSections(p.Diff))
	}
}

func TestPastCommitAndUncommittedChanges(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a.txt", "committed v1\n")
	first := r.CommitAll("v1")
	r.Write("a.txt", "committed v2\n")
	r.CommitAll("v2")

	// Unstaged, staged and untracked changes after the commits.
	r.Write("a.txt", "UNSTAGED CHANGE\n")
	r.Write("staged.txt", "STAGED CHANGE\n")
	r.Git("add", "staged.txt")
	r.Write("untracked.txt", "UNTRACKED\n")

	for rev, want := range map[string]string{"HEAD": "+committed v2", first: "+committed v1"} {
		p, text, _ := publish(t, r, rev)
		if len(p.Files) != 1 || p.Files[0] != "a.txt" || !strings.Contains(p.Diff, want) {
			t.Errorf("%s: files = %v, diff lacks %q", rev, p.Files, want)
		}
		for _, leak := range []string{"UNSTAGED", "STAGED", "UNTRACKED", "staged.txt", "untracked.txt"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s: uncommitted %q leaked into the payload", rev, leak)
			}
		}
	}
	if p, _, _ := publish(t, r, first); p.CommitSHA != first {
		t.Errorf("commit_sha = %s, want %s", p.CommitSHA, first)
	}
}

func TestDetachedHEADAndBranchAtCaptureTime(t *testing.T) {
	r := testutil.NewRepo(t)
	first := r.CommitAll("one")
	r.CommitAll("two")
	r.Git("checkout", "-q", "--detach", first)
	if p, text, _ := publish(t, r, "HEAD"); p.Branch != nil || !strings.Contains(text, `"branch": null`) {
		t.Errorf("branch = %v, want null when detached:\n%s", p.Branch, text)
	}
	r.Git("checkout", "-q", "-b", "feature/cache")
	// The branch is the one checked out now, even for an older commit.
	if p, _, _ := publish(t, r, "main"); p.Branch == nil || *p.Branch != "feature/cache" {
		t.Errorf("branch = %v, want feature/cache", p.Branch)
	}
}

func TestMergeCommitUsesFirstParent(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("base.txt", "base\n")
	r.CommitAll("base")
	r.Git("checkout", "-q", "-b", "feature")
	r.Write("feature.txt", "from feature\n")
	featureTip := r.CommitAll("feature work")
	r.Git("checkout", "-q", "main")
	r.Write("main.txt", "from main\n")
	mainTip := r.CommitAll("main work")
	r.Git("merge", "-q", "--no-ff", "-m", "Merge feature", "feature")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	if len(ev.Commit.Parents) != 2 || ev.Commit.Parents[0] != mainTip || ev.Commit.Parents[1] != featureTip {
		t.Fatalf("parents = %v", ev.Commit.Parents)
	}
	if ev.Comparison.Strategy != "first_parent" || *ev.Comparison.BaseSHA != mainTip {
		t.Errorf("comparison = %+v", ev.Comparison)
	}
	// Relative to the first parent only feature.txt changed.
	p, _, notes := publish(t, r, "HEAD")
	if strings.Join(p.Files, ",") != "feature.txt" || strings.Contains(p.Diff, "main.txt") || p.Message != "Merge feature\n" {
		t.Errorf("payload = %+v", p)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "first parent") {
		t.Errorf("notes = %v", notes)
	}
}

func TestAmendedCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a.txt", "one\n")
	r.CommitAll("first")
	r.Write("a.txt", "draft\n")
	draft := r.CommitAll("draft")
	r.Write("a.txt", "final\n")
	r.Git("commit", "-q", "-a", "--amend", "-m", "final")
	amended := r.Head()
	if amended == draft {
		t.Fatal("amend did not create a new commit")
	}
	p, text, _ := publish(t, r, "HEAD")
	if p.CommitSHA != amended || p.Message != "final\n" || !strings.Contains(p.Diff, "+final") || strings.Contains(text, "draft") {
		t.Errorf("payload of the amended commit:\n%s", text)
	}
}

func TestEmptyCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a.txt", "x\n")
	r.CommitAll("first")
	r.CommitAll("empty")
	p, text, _ := publish(t, r, "HEAD")
	if p.Files == nil || len(p.Files) != 0 || p.Diff != "" || !strings.Contains(text, `"files": []`) || !strings.Contains(text, `"diff": ""`) {
		t.Errorf("empty commit:\n%s", text)
	}
}

func TestUnusualNamesAndTypeChange(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("typ", "regular\n")
	r.Write("old name.txt", "1\n2\n3\n4\n5\n")
	r.CommitAll("base")
	os.Remove(filepath.Join(r.Dir, "typ"))
	if err := os.Symlink("target", filepath.Join(r.Dir, "typ")); err != nil {
		t.Fatal(err)
	}
	r.Git("mv", "old name.txt", "新しい 名前.txt")
	names := []string{"日本語 と 空白.txt", "-dash.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, "tab\tname.txt", "new\nline.txt")
	}
	for _, n := range names {
		r.Write(n, "content of "+n+"\n")
	}
	r.CommitAll("names")

	p, _, _ := publish(t, r, "HEAD")
	want := append([]string{"typ", "新しい 名前.txt"}, names...)
	got := map[string]bool{}
	for _, f := range p.Files {
		got[f] = true
	}
	if len(p.Files) != len(want) {
		t.Errorf("files = %q, want %q", p.Files, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("files lack %q: %q", w, p.Files)
		}
	}
	// One section per file, plus one more for the type change (git prints
	// it as a deletion and a creation). The rename appears once.
	if n := countSections(p.Diff); n != len(want)+1 {
		t.Errorf("diff has %d sections, want %d:\n%s", n, len(want)+1, p.Diff)
	}
	if strings.Count(p.Diff, "rename from old name.txt\n") != 1 {
		t.Errorf("rename appears %d times", strings.Count(p.Diff, "rename from old name.txt\n"))
	}
}

func TestBinaryAndSensitiveFiles(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("config.txt", "SECRET_IN_RENAMED_SOURCE=1\nline2\nline3\n")
	r.Write("settings.ini", "SECRET_IN_RENAMED_TARGET=1\nline2\nline3\n")
	r.CommitAll("base")
	r.Write(".env", "API_KEY=SECRET_ENV_VALUE\n")
	r.Write("deploy/.env.production", "TOKEN=SECRET_PROD\n")
	r.Write("keys/server.PEM", "-----BEGIN PRIVATE KEY-----\nSECRET_PEM\n")
	r.Write("id_ed25519", "SECRET_SSH\n")
	r.Write("id_ed25519.pub", "ssh-ed25519 PUBLIC\n")
	r.Write("logo.png", "\x89PNG\x00\x00binary")
	r.Write("app.go", "package app\n")
	// Renames are checked on both sides.
	r.Git("mv", "settings.ini", ".env.local")
	r.Git("mv", "config.txt", "..tmp") // a rename that is not sensitive
	r.CommitAll("secrets")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	sensitive := []string{".env", "deploy/.env.production", "keys/server.PEM", "id_ed25519", ".env.local"}
	for _, path := range sensitive {
		if f := file(t, ev, path); f.Patch != nil || *f.OmittedReason != "sensitive_path" {
			t.Errorf("%s: %+v", path, f)
		}
	}
	if png := file(t, ev, "logo.png"); !png.Binary || png.Patch != nil || *png.OmittedReason != "binary" {
		t.Errorf("logo.png: %+v", png)
	}

	p, text, notes := publish(t, r, "HEAD")
	for _, secret := range []string{"SECRET_ENV_VALUE", "SECRET_PROD", "SECRET_PEM", "SECRET_SSH", "SECRET_IN_RENAMED_TARGET"} {
		if strings.Contains(text, secret) || strings.Contains(strings.Join(notes, "\n"), secret) {
			t.Errorf("%s leaked", secret)
		}
	}
	// Left-out files are missing from files and diff alike.
	if strings.Join(p.Files, ",") != "..tmp,app.go,id_ed25519.pub" {
		t.Errorf("files = %v", p.Files)
	}
	for _, path := range append(sensitive, "logo.png", "settings.ini") {
		if strings.Contains(p.Diff, path+"\n") || strings.Contains(p.Diff, "b/"+path+" ") || strings.Contains(p.Diff, "a/"+path+" ") {
			t.Errorf("diff mentions %s", path)
		}
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"left out .env:", "left out settings.ini -> .env.local:", "left out logo.png: binary file"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes lack %q:\n%s", want, joined)
		}
	}
}

func TestRenameFromSensitivePath(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write(".env", "PASSWORD=SECRET_OLD_SIDE\nA=1\nB=2\n")
	r.CommitAll("base")
	r.Git("mv", ".env", "defaults.txt")
	r.Write("defaults.txt", "PASSWORD=SECRET_OLD_SIDE\nA=1\nB=3\n")
	r.CommitAll("rename")
	if f := file(t, build(t, r, "HEAD", event.DefaultLimits), "defaults.txt"); f.Status != "R" || *f.OmittedReason != "sensitive_path" {
		t.Errorf("rename from .env: %+v", f)
	}
	p, text, notes := publish(t, r, "HEAD")
	if len(p.Files) != 0 || p.Diff != "" || strings.Contains(text, "SECRET_OLD_SIDE") {
		t.Errorf("payload:\n%s", text)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "left out .env -> defaults.txt") {
		t.Errorf("notes = %v", notes)
	}
}

func TestLimitsStopThePayload(t *testing.T) {
	r := testutil.NewRepo(t)
	for i := 0; i < 100; i++ {
		r.Write(fmt.Sprintf("many/f%03d.txt", i), "x\n")
	}
	r.CommitAll("100 files")
	if p, _, _ := publish(t, r, "HEAD"); len(p.Files) != 100 {
		t.Fatalf("100 files: got %d", len(p.Files))
	}

	for i := 0; i < 101; i++ {
		r.Write(fmt.Sprintf("more/f%03d.txt", i), "x\n")
	}
	r.CommitAll("101 files")
	checkLimitError(t, r, "the commit changes 101 files, more than the limit of 100")

	// One file above 64 KiB.
	line := strings.Repeat("0123456789", 7) + "\n"
	r.Write("big/one.txt", strings.Repeat(line, 1000)) // ~71 KiB of patch
	r.CommitAll("big file")
	checkLimitError(t, r, "the diff of big/one.txt is larger than 65536 bytes")

	// Nine files below 64 KiB each, above 512 KiB together.
	for i := 0; i < 9; i++ {
		r.Write(fmt.Sprintf("big/part%d.txt", i), strings.Repeat(line, 850)) // ~60 KiB each
	}
	r.CommitAll("big total")
	checkLimitError(t, r, "larger than 524288 bytes in total")
}

func checkLimitError(t *testing.T, r *testutil.Repo, want string) {
	t.Helper()
	ev := build(t, r, "HEAD", event.DefaultLimits)
	if !ev.Summary.Truncated {
		t.Errorf("snapshot not marked truncated: %+v", ev.Summary)
	}
	p, notes, err := event.NewPayload(testID, ev)
	if !errors.Is(err, event.ErrLimitExceeded) || !strings.Contains(err.Error(), want) || p != nil || notes != nil {
		t.Errorf("NewPayload = %v, %v, %v; want an error containing %q", p, notes, err, want)
	}
}

func TestNewPayloadDeduplicatesFiles(t *testing.T) {
	a, b := "a.txt", "b.txt"
	pa, pb := "diff --git a/a.txt b/a.txt\n+1\n", "diff --git a/b.txt b/b.txt\n+2\n"
	ev := &event.Event{
		Commit: event.Commit{SHA: "abc", Parents: []string{}, Message: "m\n"},
		Files: []event.File{
			{Status: "M", OldPath: &a, NewPath: &a, Patch: &pa},
			{Status: "M", OldPath: &b, NewPath: &b, Patch: &pb},
			{Status: "T", OldPath: &a, NewPath: &a, Patch: &pa},
		},
		Summary: event.Summary{ChangedFiles: 3, IncludedFiles: 3},
		Limits:  event.DefaultLimits,
	}
	p, _, err := event.NewPayload(testID, ev)
	if err != nil || strings.Join(p.Files, ",") != "a.txt,b.txt" || p.Diff != pa+pb+pa {
		t.Fatalf("payload = %+v, %v", p, err)
	}
}

func TestInvalidUTF8PathIsReported(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS file systems reject file names that are not valid UTF-8")
	}
	r := testutil.NewRepo(t)
	r.Write("bad-\xff.txt", "x\n")
	r.CommitAll("bad name")
	p, _, notes := publish(t, r, "HEAD")
	if p.Files[0] != "bad-\uFFFD.txt" {
		t.Errorf("files = %q", p.Files)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, `the path "bad-\xff.txt" is not valid UTF-8`) || !strings.Contains(joined, `the diff of "bad-\xff.txt"`) {
		t.Errorf("notes = %v", notes)
	}
}

func TestShallowBoundaryIsNotTreatedAsRoot(t *testing.T) {
	src := testutil.NewRepo(t)
	src.Write("a.txt", "1\n")
	src.CommitAll("one")
	src.Write("a.txt", "2\n")
	src.CommitAll("two")
	dst := &testutil.Repo{T: t, Dir: filepath.Join(t.TempDir(), "shallow")}
	src.Git("clone", "-q", "--depth", "1", "file://"+src.Dir, dst.Dir)

	_, err := buildErr(dst, "HEAD", event.DefaultLimits)
	if err == nil || !strings.Contains(err.Error(), "not treated as a root commit") || !strings.Contains(err.Error(), "shallow") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoAbsolutePathsOrEmails(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Git("remote", "add", "origin", "https://user:token@example.com/repo.git")
	r.Write("a.txt", "x\n")
	r.CommitAll("first")
	_, text, _ := publish(t, r, "HEAD")
	for _, s := range []string{r.Dir, "test@example.com", "example.com", "token", "Test User"} {
		if strings.Contains(text, s) {
			t.Errorf("payload contains %q:\n%s", s, text)
		}
	}
}

func TestIsSensitivePath(t *testing.T) {
	for p, want := range map[string]bool{
		".env": true, "app/.env": true, ".env.local": true, ".ENV.production": true, ".env.example": true,
		"id_rsa": true, "home/.ssh/id_ed25519": true, "id_rsa.pub": false,
		"server.key": true, "cert.pem": true, "store.p12": true, "keystore.jks": true,
		".npmrc": true, ".netrc": true, ".aws/credentials": true, "credentials.json": true,
		"main.go": false, "environment.go": false, "src/env.ts": false, "keys.go": false, "": false,
	} {
		if got := event.IsSensitivePath(p); got != want {
			t.Errorf("IsSensitivePath(%q) = %v, want %v", p, got, want)
		}
	}
}
