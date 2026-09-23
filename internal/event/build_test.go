package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kobadaidesu/hook-test/internal/event"
	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestMain(m *testing.M) { testutil.Main(m, nil) }

var capturedAt = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

func build(t *testing.T, r *testutil.Repo, rev string, limits event.Limits) *event.Event {
	t.Helper()
	ev, err := buildErr(r, rev, limits)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := event.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, rev, ev, raw) // every event built by the tests obeys the schema's rules
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
	return event.Build(ctx, repo, oid, limits, capturedAt)
}

// roundTrip encodes ev and returns both the JSON text and a generic view.
func roundTrip(t *testing.T, ev *event.Event) (string, map[string]any) {
	t.Helper()
	data, err := event.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return string(data), m
}

func file(t *testing.T, ev *event.Event, path string) event.File {
	t.Helper()
	for _, f := range ev.Files {
		if f.NewPath != nil && *f.NewPath == path || f.NewPath == nil && f.OldPath != nil && *f.OldPath == path {
			return f
		}
	}
	t.Fatalf("no file %q in event", path)
	return event.File{}
}

func TestRootCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("src/add.go", "package calc\n")
	r.Write("README.md", "# demo\n")
	head := r.CommitAll("Initial commit")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	text, m := roundTrip(t, ev)
	if ev.SchemaVersion != "1.0" || ev.EventType != "commit.snapshot" || ev.CapturedAt != "2026-09-24T10:00:00Z" {
		t.Errorf("header = %s %s %s", ev.SchemaVersion, ev.EventType, ev.CapturedAt)
	}
	if ev.Commit.SHA != head || ev.Commit.Message != "Initial commit\n" || ev.Commit.AuthorName != "Test User" {
		t.Errorf("commit = %+v", ev.Commit)
	}
	if ev.Comparison.Strategy != "empty_tree" || ev.Comparison.BaseSHA != nil {
		t.Errorf("comparison = %+v", ev.Comparison)
	}
	// Empty arrays are [] and missing values are null, never omitted.
	if !strings.Contains(text, `"parents": []`) || !strings.Contains(text, `"warnings": []`) || !strings.Contains(text, `"base_sha": null`) {
		t.Errorf("JSON:\n%s", text)
	}
	if m["repository"].(map[string]any)["current_branch"] != "main" {
		t.Errorf("current_branch = %v", m["repository"])
	}
	f := file(t, ev, "src/add.go")
	if f.Status != "A" || f.OldPath != nil || *f.Additions != 1 || *f.Deletions != 0 || !strings.Contains(*f.Patch, "+package calc\n") {
		t.Errorf("file = %+v", f)
	}
	if ev.Summary != (event.Summary{ChangedFiles: 2, IncludedFiles: 2}) {
		t.Errorf("summary = %+v", ev.Summary)
	}
}

func TestNormalCommitComparesWithFirstParent(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a - b\n}\n")
	parent := r.CommitAll("first")
	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n")
	head := r.CommitAll("Fix addition")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	if ev.Commit.SHA != head || len(ev.Commit.Parents) != 1 || ev.Commit.Parents[0] != parent {
		t.Errorf("commit = %+v", ev.Commit)
	}
	if ev.Comparison.Strategy != "first_parent" || *ev.Comparison.BaseSHA != parent {
		t.Errorf("comparison = %+v", ev.Comparison)
	}
	f := file(t, ev, "src/add.go")
	if f.Status != "M" || *f.OldPath != "src/add.go" || !strings.Contains(*f.Patch, "-\treturn a - b\n+\treturn a + b\n") {
		t.Errorf("file = %+v", f)
	}
	if f.PatchTruncated || f.OmittedReason != nil || f.Binary {
		t.Errorf("file flags = %+v", f)
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
		ev := build(t, r, rev, event.DefaultLimits)
		text, _ := roundTrip(t, ev)
		if len(ev.Files) != 1 || !strings.Contains(text, want) {
			t.Errorf("%s: files = %d, JSON lacks %q", rev, len(ev.Files), want)
		}
		for _, leak := range []string{"UNSTAGED", "STAGED", "UNTRACKED", "staged.txt", "untracked.txt"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s: uncommitted %q leaked into the event", rev, leak)
			}
		}
	}
}

func TestDetachedHEADAndBranchAtCaptureTime(t *testing.T) {
	r := testutil.NewRepo(t)
	first := r.CommitAll("one")
	r.CommitAll("two")
	r.Git("checkout", "-q", "--detach", first)
	if ev := build(t, r, "HEAD", event.DefaultLimits); ev.Repository.CurrentBranch != nil {
		t.Errorf("current_branch = %q, want null when detached", *ev.Repository.CurrentBranch)
	}
	r.Git("checkout", "-q", "-b", "other")
	// The branch is the one checked out now, even for an older commit.
	if ev := build(t, r, "main", event.DefaultLimits); ev.Repository.CurrentBranch == nil || *ev.Repository.CurrentBranch != "other" {
		t.Errorf("current_branch = %v, want other", ev.Repository.CurrentBranch)
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
	if len(ev.Files) != 1 || *ev.Files[0].NewPath != "feature.txt" {
		t.Errorf("files = %+v", ev.Files)
	}
	if len(ev.Warnings) != 1 || !strings.Contains(ev.Warnings[0], "first parent") {
		t.Errorf("warnings = %v", ev.Warnings)
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
	ev := build(t, r, "HEAD", event.DefaultLimits)
	text, _ := roundTrip(t, ev)
	if ev.Commit.SHA != amended || ev.Commit.Message != "final\n" || !strings.Contains(text, "+final") || strings.Contains(text, "draft") {
		t.Errorf("event of amended commit:\n%s", text)
	}
}

func TestEmptyCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a.txt", "x\n")
	r.CommitAll("first")
	r.CommitAll("empty")
	ev := build(t, r, "HEAD", event.DefaultLimits)
	text, _ := roundTrip(t, ev)
	if !strings.Contains(text, `"files": []`) || ev.Summary != (event.Summary{}) {
		t.Errorf("empty commit:\n%s", text)
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
	// Renames are checked on both sides.
	r.Git("mv", "settings.ini", ".env.local")
	r.Git("mv", "config.txt", "harmless.txt")
	r.Git("mv", "harmless.txt", "..tmp") // keep a non-sensitive rename too
	r.CommitAll("secrets")

	ev := build(t, r, "HEAD", event.DefaultLimits)
	text, _ := roundTrip(t, ev)
	for _, secret := range []string{"SECRET_ENV_VALUE", "SECRET_PROD", "SECRET_PEM", "SECRET_SSH", "SECRET_IN_RENAMED_TARGET"} {
		if strings.Contains(text, secret) {
			t.Errorf("%s leaked into the event", secret)
		}
	}
	for _, p := range []string{".env", "deploy/.env.production", "keys/server.PEM", "id_ed25519", ".env.local"} {
		f := file(t, ev, p)
		if f.Patch != nil || f.OmittedReason == nil || *f.OmittedReason != "sensitive_path" || f.Additions == nil {
			t.Errorf("%s: %+v", p, f)
		}
	}
	if f := file(t, ev, "id_ed25519.pub"); f.Patch == nil {
		t.Errorf("public key should keep its patch")
	}
	png := file(t, ev, "logo.png")
	if !png.Binary || png.Patch != nil || png.Additions != nil || png.Deletions != nil || *png.OmittedReason != "binary" {
		t.Errorf("logo.png: %+v", png)
	}
	if ev.Summary.Truncated {
		t.Error("binary and sensitive omissions are not truncation")
	}
	renamed := file(t, ev, ".env.local")
	if renamed.Status != "R" || *renamed.OldPath != "settings.ini" {
		t.Errorf("rename into .env.local: %+v", renamed)
	}
}

func TestRenameFromSensitivePath(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write(".env", "PASSWORD=SECRET_OLD_SIDE\nA=1\nB=2\n")
	r.CommitAll("base")
	r.Git("mv", ".env", "defaults.txt")
	r.CommitAll("rename")
	ev := build(t, r, "HEAD", event.DefaultLimits)
	text, _ := roundTrip(t, ev)
	f := file(t, ev, "defaults.txt")
	if f.Status != "R" || f.Patch != nil || *f.OmittedReason != "sensitive_path" || strings.Contains(text, "SECRET_OLD_SIDE") {
		t.Errorf("rename from .env: %+v", f)
	}
}

func TestDefaultLimits(t *testing.T) {
	r := testutil.NewRepo(t)
	for i := 0; i < 101; i++ {
		r.Write(fmt.Sprintf("many/f%03d.txt", i), "x\n")
	}
	r.CommitAll("101 files")
	ev := build(t, r, "HEAD", event.DefaultLimits)
	if ev.Summary != (event.Summary{ChangedFiles: 101, IncludedFiles: 100, OmittedFiles: 1, Truncated: true}) || len(ev.Files) != 100 {
		t.Errorf("summary = %+v, files = %d", ev.Summary, len(ev.Files))
	}
	if *ev.Files[99].NewPath != "many/f099.txt" {
		t.Errorf("files are not in path order: last = %s", *ev.Files[99].NewPath)
	}

	// Nine files of ~70 KiB: each is cut at 64 KiB, and the 512 KiB total
	// runs out at the ninth.
	line := strings.Repeat("0123456789", 7) + "\n"
	for i := 0; i < 9; i++ {
		r.Write(fmt.Sprintf("big/%d.txt", i), strings.Repeat(line, 1000))
	}
	r.CommitAll("big files")
	ev = build(t, r, "HEAD", event.DefaultLimits)
	total := 0
	for i, f := range ev.Files {
		switch {
		case i < 8:
			if !f.PatchTruncated || len(*f.Patch) != 64<<10 {
				t.Errorf("file %d: truncated=%v len=%d", i, f.PatchTruncated, len(*f.Patch))
			}
			total += len(*f.Patch)
		default:
			if f.Patch != nil || *f.OmittedReason != "total_patch_limit" || *f.Additions != 1000 {
				t.Errorf("file %d: %+v", i, f)
			}
		}
	}
	if total != 512<<10 || !ev.Summary.Truncated || ev.Summary.OmittedFiles != 0 {
		t.Errorf("total = %d, summary = %+v", total, ev.Summary)
	}
	if len(ev.Warnings) != 2 {
		t.Errorf("warnings = %v", ev.Warnings)
	}
}

func TestInvalidUTF8PathIsReported(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS file systems reject file names that are not valid UTF-8")
	}
	r := testutil.NewRepo(t)
	r.Write("bad-\xff.txt", "x\n")
	r.CommitAll("bad name")
	ev := build(t, r, "HEAD", event.DefaultLimits)
	if *ev.Files[0].NewPath != "bad-\uFFFD.txt" {
		t.Errorf("new_path = %q", *ev.Files[0].NewPath)
	}
	joined := strings.Join(ev.Warnings, "\n")
	if !strings.Contains(joined, "files[0].new_path is not valid UTF-8") || !strings.Contains(joined, "files[0].patch") {
		t.Errorf("warnings = %v", ev.Warnings)
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

func TestDatesAreRFC3339(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-09-24T18:59:00+09:00", "GIT_COMMITTER_DATE=2026-09-24T10:00:00Z")
	r.CommitAll("dated")
	ev := build(t, r, "HEAD", event.DefaultLimits)
	for _, s := range []string{ev.Commit.AuthoredAt, ev.Commit.CommittedAt, ev.CapturedAt} {
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			t.Errorf("%q is not RFC 3339", s)
		}
	}
	if ev.Commit.AuthoredAt != "2026-09-24T18:59:00+09:00" || ev.Commit.CommittedAt != "2026-09-24T10:00:00Z" {
		t.Errorf("dates = %s, %s", ev.Commit.AuthoredAt, ev.Commit.CommittedAt)
	}
}

func TestNoAbsolutePathsOrEmails(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Git("remote", "add", "origin", "https://user:token@example.com/repo.git")
	r.Write("a.txt", "x\n")
	r.CommitAll("first")
	text, _ := roundTrip(t, build(t, r, "HEAD", event.DefaultLimits))
	for _, s := range []string{r.Dir, "test@example.com", "example.com", "token"} {
		if strings.Contains(text, s) {
			t.Errorf("event contains %q:\n%s", s, text)
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
