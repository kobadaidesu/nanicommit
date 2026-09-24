package gitrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestMain(m *testing.M) { testutil.Main(m, nil) }

func open(t *testing.T, dir string) *gitrepo.Repo {
	t.Helper()
	repo, err := gitrepo.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestOpenFromSubdirectory(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a/b/file.txt", "x\n")
	repo := open(t, filepath.Join(r.Dir, "a", "b"))
	dir := testutil.RealPath(t, r.Dir)
	if got := testutil.RealPath(t, repo.WorkTree); got != dir {
		t.Errorf("WorkTree = %q, want %q", got, dir)
	}
	if want := filepath.Join(dir, ".git"); testutil.RealPath(t, repo.GitDir) != want || testutil.RealPath(t, repo.CommonDir) != want {
		t.Errorf("GitDir = %q, CommonDir = %q, want %q", repo.GitDir, repo.CommonDir, want)
	}
	if repo.Bare {
		t.Error("Bare = true")
	}
	if repo.DisplayName() != filepath.Base(r.Dir) {
		t.Errorf("DisplayName = %q", repo.DisplayName())
	}
}

func TestOpenOutsideRepository(t *testing.T) {
	_, err := gitrepo.Open(context.Background(), t.TempDir())
	if !errors.Is(err, gitrepo.ErrNotRepository) {
		t.Fatalf("err = %v, want ErrNotRepository", err)
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error does not include git's explanation: %v", err)
	}
}

func TestOpenBareRepository(t *testing.T) {
	dir := t.TempDir()
	r := &testutil.Repo{T: t, Dir: dir}
	r.Git("init", "-q", "--bare", "-b", "main", "demo.git")
	repo := open(t, filepath.Join(dir, "demo.git"))
	if !repo.Bare || repo.WorkTree != "" {
		t.Fatalf("Bare = %v, WorkTree = %q", repo.Bare, repo.WorkTree)
	}
	if repo.DisplayName() != "demo" {
		t.Errorf("DisplayName = %q, want demo", repo.DisplayName())
	}
}

func TestOpenLinkedWorktree(t *testing.T) {
	r := testutil.NewRepo(t)
	r.CommitAll("init")
	wt := filepath.Join(t.TempDir(), "wt")
	r.Git("worktree", "add", "-q", "-b", "feature", wt)
	repo := open(t, wt)
	common := filepath.Join(testutil.RealPath(t, r.Dir), ".git")
	if testutil.RealPath(t, repo.CommonDir) != common {
		t.Errorf("CommonDir = %q, want %q", repo.CommonDir, common)
	}
	if repo.GitDir == repo.CommonDir {
		t.Error("GitDir of a linked worktree should differ from CommonDir")
	}
	if testutil.RealPath(t, repo.WorkTree) != testutil.RealPath(t, wt) {
		t.Errorf("WorkTree = %q", repo.WorkTree)
	}
	branch, ok, err := repo.CurrentBranch(context.Background())
	if err != nil || !ok || branch != "feature" {
		t.Errorf("CurrentBranch = %q, %v, %v", branch, ok, err)
	}
	if repo.DisplayName() != filepath.Base(r.Dir) {
		t.Errorf("DisplayName = %q, want the main repository's name", repo.DisplayName())
	}
}

func TestResolveCommit(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("f.txt", "1\n")
	first := r.CommitAll("first")
	r.Write("f.txt", "2\n")
	second := r.CommitAll("second")
	r.Git("tag", "-a", "-m", "annotated", "v1", first)
	repo := open(t, r.Dir)
	ctx := context.Background()

	for rev, want := range map[string]string{
		"HEAD": second, "HEAD~1": first, "main": second, second[:10]: second, first: first,
		"v1": first, // an annotated tag is peeled to its commit
	} {
		got, err := repo.ResolveCommit(ctx, rev)
		if err != nil || got != want {
			t.Errorf("ResolveCommit(%q) = %q, %v; want %q", rev, got, err, want)
		}
	}
	for _, rev := range []string{"nope", "HEAD~5", "HEAD^{tree}", "HEAD:f.txt", "--help", "-n1", "0000000000000000000000000000000000000000"} {
		if got, err := repo.ResolveCommit(ctx, rev); !errors.Is(err, gitrepo.ErrUnknownCommit) {
			t.Errorf("ResolveCommit(%q) = %q, %v; want ErrUnknownCommit", rev, got, err)
		}
	}
}

func TestResolveCommitInEmptyRepository(t *testing.T) {
	r := testutil.NewRepo(t)
	if _, err := open(t, r.Dir).ResolveCommit(context.Background(), "HEAD"); !errors.Is(err, gitrepo.ErrUnknownCommit) {
		t.Fatalf("err = %v, want ErrUnknownCommit", err)
	}
}

func TestReadCommitKeepsMessageExactly(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Git("config", "user.name", `山田 "Taro" O'Neil`)
	r.Write("f.txt", "x\n")
	r.Git("add", "f.txt")
	msg := "修正: \"quote\" と 'single'\n\n本文の2行目\n\tタブ付き行\n末尾の空行\n\n"
	r.Write(".git/msg", msg)
	r.Git("commit", "-q", "--cleanup=verbatim", "-F", ".git/msg")
	head := r.Head()

	c, err := open(t, r.Dir).ReadCommit(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	if c.Message != msg {
		t.Errorf("Message = %q, want %q", c.Message, msg)
	}
	if c.AuthorName != `山田 "Taro" O'Neil` {
		t.Errorf("AuthorName = %q", c.AuthorName)
	}
	if len(c.Parents) != 0 {
		t.Errorf("Parents = %v, want none", c.Parents)
	}
	if c.AuthorDate.IsZero() || c.CommitterDate.IsZero() {
		t.Error("dates not parsed")
	}
}

func TestReadCommitDates(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-09-24T09:59:00+09:00", "GIT_COMMITTER_DATE=2026-09-24T10:00:30Z")
	head := r.CommitAll("dated")
	c, err := open(t, r.Dir).ReadCommit(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.AuthorDate.Format(time.RFC3339); got != "2026-09-24T09:59:00+09:00" {
		t.Errorf("AuthorDate = %s", got)
	}
	if got := c.CommitterDate.Format(time.RFC3339); got != "2026-09-24T10:00:30Z" {
		t.Errorf("CommitterDate = %s", got)
	}
}

func TestShallowCloneKeepsRealParents(t *testing.T) {
	src := testutil.NewRepo(t)
	src.Write("f.txt", "1\n")
	parent := src.CommitAll("first")
	src.Write("f.txt", "2\n")
	src.CommitAll("second")

	dst := filepath.Join(t.TempDir(), "shallow")
	src.Git("clone", "-q", "--depth", "1", "file://"+src.Dir, dst)
	repo := open(t, dst)
	ctx := context.Background()
	head, err := repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.ReadCommit(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Parents) != 1 || c.Parents[0] != parent {
		t.Fatalf("Parents = %v, want [%s] (git log would report none here)", c.Parents, parent)
	}
	if ok, err := repo.HasObject(ctx, parent); err != nil || ok {
		t.Errorf("HasObject(parent) = %v, %v; want false in a shallow clone", ok, err)
	}
	if shallow, err := repo.IsShallow(ctx); err != nil || !shallow {
		t.Errorf("IsShallow = %v, %v", shallow, err)
	}
}

func TestSHA256Repository(t *testing.T) {
	r := testutil.NewRepo(t, "--object-format=sha256")
	r.Write("a.txt", "hello\n")
	root := r.CommitAll("root")
	r.Write("a.txt", "hello world\n")
	head := r.CommitAll("change")
	if len(head) != 64 {
		t.Fatalf("expected a 64-character object name, got %q", head)
	}
	repo := open(t, r.Dir)
	ctx := context.Background()
	if got, err := repo.ResolveCommit(ctx, "HEAD"); err != nil || got != head {
		t.Fatalf("ResolveCommit = %q, %v", got, err)
	}
	c, err := repo.ReadCommit(ctx, head)
	if err != nil || len(c.Parents) != 1 || c.Parents[0] != root {
		t.Fatalf("ReadCommit = %+v, %v", c, err)
	}
	empty, err := repo.EmptyTree(ctx)
	if err != nil || len(empty) != 64 {
		t.Fatalf("EmptyTree = %q, %v", empty, err)
	}
	d, err := repo.Diff(ctx, empty, root, bigLimits)
	if err != nil || len(d.Files) != 1 || d.Files[0].Status != "A" {
		t.Fatalf("Diff(empty tree, root) = %+v, %v", d, err)
	}
}

func TestConfigValues(t *testing.T) {
	r := testutil.NewRepo(t)
	repo := open(t, r.Dir)
	ctx := context.Background()
	if v, err := repo.ConfigValues(ctx, "core.hooksPath"); err != nil || v != nil {
		t.Fatalf("unset key: %v, %v", v, err)
	}
	r.Git("config", "core.hooksPath", "my hooks")
	v, err := repo.ConfigValues(ctx, "core.hooksPath")
	if err != nil || len(v) != 1 || v[0].Value != "my hooks" || v[0].Scope != "local" {
		t.Fatalf("ConfigValues = %+v, %v", v, err)
	}
	hooksDir, err := repo.HooksDir(ctx)
	if err != nil || hooksDir != filepath.Join(repo.WorkTree, "my hooks") {
		t.Fatalf("HooksDir = %q, %v", hooksDir, err)
	}
}
