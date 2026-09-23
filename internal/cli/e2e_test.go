package cli_test

// End-to-end tests: they build the commitcoach binary into a directory whose
// name contains spaces and quotes, install it with "init" into temporary
// repositories, and let real "git commit" runs start the hook.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kobadaidesu/hook-test/internal/event"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

var binPath string

func TestMain(m *testing.M) { testutil.Main(m, buildBinary) }

func buildBinary() (func(), error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("these tests use POSIX shell hooks")
	}
	dir, err := os.MkdirTemp("", "commitcoach-bin-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	binDir := filepath.Join(dir, `bin dir with 'single' and "double" quotes`)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return cleanup, err
	}
	binPath = filepath.Join(binDir, "commitcoach")
	goTool, err := exec.LookPath("go")
	if err != nil {
		goTool = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	// -buildvcs=false: the test binary needs no VCS stamp, and stamping fails
	// in checkouts that go cannot map to a repository (e.g. some worktrees).
	out, err := exec.Command(goTool, "build", "-buildvcs=false", "-o", binPath, "github.com/kobadaidesu/hook-test/cmd/commitcoach").CombinedOutput()
	if err != nil {
		return cleanup, fmt.Errorf("go build: %v\n%s", err, out)
	}
	return cleanup, nil
}

type result struct {
	stdout, stderr string
	code           int
}

// cc runs the commitcoach binary in dir with the repository's environment.
func cc(t *testing.T, r *testutil.Repo, dir string, args ...string) result {
	t.Helper()
	return runBin(t, r, dir, binPath, args...)
}

func runBin(t *testing.T, r *testutil.Repo, dir, bin string, args ...string) result {
	t.Helper()
	stdout, stderr, err := r.Cmd(dir, bin, args...)
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", bin, err)
	}
	return result{stdout, stderr, code}
}

// commit runs "git commit" and returns its stderr (where hook output goes).
func commit(t *testing.T, r *testutil.Repo, args ...string) string {
	t.Helper()
	_, stderr, err := r.GitErr(append([]string{"commit", "-q"}, args...)...)
	if err != nil {
		t.Fatalf("git commit: %v\n%s", err, stderr)
	}
	return stderr
}

func eventPath(r *testutil.Repo, oid string) string {
	return filepath.Join(r.Dir, ".git", "commitcoach", "events", oid+".json")
}

// decodeEvent parses exactly one JSON object with no unknown fields.
func decodeEvent(t *testing.T, data []byte) *event.Event {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ev event.Event
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("invalid snapshot JSON: %v\n%s", err, data)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("more than one JSON value in output:\n%s", data)
	}
	return &ev
}

func readEvent(t *testing.T, r *testutil.Repo, oid string) *event.Event {
	t.Helper()
	data, err := os.ReadFile(eventPath(r, oid))
	if err != nil {
		t.Fatalf("snapshot of %s not saved: %v", oid, err)
	}
	return decodeEvent(t, data)
}

func installed(t *testing.T) *testutil.Repo {
	t.Helper()
	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "init"); res.code != 0 {
		t.Fatalf("init failed: %+v", res)
	}
	return r
}

func TestInitAndCommitRecordSnapshots(t *testing.T) {
	r := testutil.NewRepo(t)
	os.MkdirAll(filepath.Join(r.Dir, "sub", "dir"), 0o755)
	res := cc(t, r, filepath.Join(r.Dir, "sub", "dir"), "init") // from a subdirectory
	if res.code != 0 || !strings.Contains(res.stdout, "installed the post-commit hook") {
		t.Fatalf("init: %+v", res)
	}
	hook, _ := os.ReadFile(filepath.Join(r.Dir, ".git", "hooks", "post-commit"))
	if !strings.Contains(string(hook), `'"'"'single'"'"'`) {
		t.Errorf("hook does not quote the binary path:\n%s", hook)
	}

	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a - b\n}\n")
	r.Git("add", "-A")
	stderr := commit(t, r, "-m", "Initial commit")
	first := r.Head()
	if !strings.Contains(stderr, "commitcoach: recorded the snapshot of "+first) {
		t.Errorf("hook message missing: %q", stderr)
	}
	ev := readEvent(t, r, first)
	if ev.Commit.SHA != first || ev.Comparison.Strategy != "empty_tree" || ev.Comparison.BaseSHA != nil {
		t.Errorf("first snapshot: %+v %+v", ev.Commit, ev.Comparison)
	}
	if len(ev.Files) != 1 || *ev.Files[0].NewPath != "src/add.go" || ev.Files[0].Status != "A" {
		t.Errorf("files = %+v", ev.Files)
	}

	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n")
	commit(t, r, "-a", "-m", "Fix addition")
	second := r.Head()
	ev = readEvent(t, r, second)
	f := ev.Files[0]
	if ev.Commit.SHA != second || *ev.Comparison.BaseSHA != first || ev.Commit.Message != "Fix addition\n" {
		t.Errorf("second snapshot: %+v %+v", ev.Commit, ev.Comparison)
	}
	if f.Status != "M" || *f.OldPath != "src/add.go" || *f.NewPath != "src/add.go" || *f.Additions != 1 || *f.Deletions != 1 ||
		!strings.Contains(*f.Patch, "-\treturn a - b\n+\treturn a + b\n") {
		t.Errorf("file = %+v", f)
	}
	if fi, err := os.Stat(eventPath(r, second)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode: %v %v", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(filepath.Dir(eventPath(r, second))); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("events dir mode: %v %v", fi.Mode().Perm(), err)
	}
	// Nothing was written into the working tree.
	if out := r.Git("status", "--porcelain", "--ignored"); out != "" {
		t.Errorf("working tree changed: %q", out)
	}
}

func TestHookIgnoresUncommittedChanges(t *testing.T) {
	r := installed(t)
	r.Write("a.txt", "a v1\n")
	r.Write("b.txt", "b v1\n")
	r.Write("c.txt", "c v1\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "base")

	r.Write("a.txt", "a COMMITTED\n")
	r.Write("b.txt", "b STAGED_NOT_COMMITTED\n")
	r.Git("add", "a.txt", "b.txt")
	r.Write("c.txt", "c UNSTAGED_NOT_COMMITTED\n")
	r.Write("d.txt", "UNTRACKED_FILE\n")
	// A partial commit: only a.txt; b.txt stays staged. git runs the hook
	// with a temporary index in this case.
	commit(t, r, "-m", "only a", "--", "a.txt")

	head := r.Head()
	data, err := os.ReadFile(eventPath(r, head))
	if err != nil {
		t.Fatal(err)
	}
	ev := decodeEvent(t, data)
	if len(ev.Files) != 1 || *ev.Files[0].NewPath != "a.txt" || !strings.Contains(*ev.Files[0].Patch, "+a COMMITTED") {
		t.Errorf("files = %+v", ev.Files)
	}
	for _, leak := range []string{"STAGED_NOT_COMMITTED", "UNSTAGED_NOT_COMMITTED", "UNTRACKED_FILE", "b.txt", "c.txt", "d.txt"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("%s leaked into the snapshot", leak)
		}
	}
	if out := r.Git("diff", "--cached", "--name-only"); out != "b.txt\n" {
		t.Errorf("the staged change was disturbed: %q", out)
	}
}

func TestExport(t *testing.T) {
	r := installed(t)
	r.Write("a.txt", "one\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "one")
	first := r.Head()
	r.Write("a.txt", "two\n")
	commit(t, r, "-a", "-m", "two")
	head := r.Head()

	res := cc(t, r, "", "export")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("export: code %d stderr %q", res.code, res.stderr)
	}
	ev := decodeEvent(t, []byte(res.stdout)) // stdout is exactly one JSON value
	if ev.Commit.SHA != head {
		t.Errorf("sha = %s", ev.Commit.SHA)
	}

	// export and the hook produce the same document (except captured_at).
	hookEv := readEvent(t, r, head)
	hookEv.CapturedAt, ev.CapturedAt = "", ""
	a, _ := json.Marshal(hookEv)
	b, _ := json.Marshal(ev)
	if !bytes.Equal(a, b) {
		t.Errorf("export differs from the hook snapshot:\n%s\n%s", a, b)
	}

	res = cc(t, r, "", "export", "--commit", first, "--output", "-")
	if ev := decodeEvent(t, []byte(res.stdout)); res.code != 0 || ev.Commit.SHA != first || ev.Comparison.Strategy != "empty_tree" {
		t.Errorf("export of the first commit: %+v", res)
	}

	res = cc(t, r, "", "export", "--commit", "HEAD~1", "--output", "./event.json")
	out := filepath.Join(r.Dir, "event.json")
	data, err := os.ReadFile(out)
	if res.code != 0 || res.stdout != "" || err != nil || !strings.Contains(res.stderr, "wrote the snapshot") {
		t.Fatalf("export to file: %+v, %v", res, err)
	}
	if decodeEvent(t, data).Commit.SHA != first {
		t.Error("wrong commit in file")
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestExportErrors(t *testing.T) {
	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "export"); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "does not name a commit") {
		t.Errorf("export in an empty repository: %+v", res)
	}
	r.Write("f.txt", "one\n")
	r.CommitAll("one")
	for _, rev := range []string{"no-such-branch", "HEAD~3", "HEAD^{tree}", "--output"} {
		if res := cc(t, r, "", "export", "--commit="+rev); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "does not name a commit") {
			t.Errorf("export --commit=%s: %+v", rev, res)
		}
	}
	if res := cc(t, r, "", "export", "extra"); res.code != 2 {
		t.Errorf("unexpected argument: %+v", res)
	}
	if res := cc(t, r, t.TempDir(), "export"); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "no usable git repository") {
		t.Errorf("export outside a repository: %+v", res)
	}

	// A git failure (missing object) is reported, not replaced by empty data.
	r.Write("f.txt", "two\n")
	r.CommitAll("two")
	blob := strings.TrimSpace(r.Git("rev-parse", "HEAD:f.txt"))
	os.Remove(filepath.Join(r.Dir, ".git", "objects", blob[:2], blob[2:]))
	if res := cc(t, r, "", "export"); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "git diff-tree exited with status") {
		t.Errorf("export with a missing object: %+v", res)
	}
}

func TestHookFailureKeepsCommit(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	r := installed(t)
	events := filepath.Join(r.Dir, ".git", "commitcoach", "events")
	os.MkdirAll(events, 0o700)
	os.Chmod(events, 0o500) // saving will fail
	defer os.Chmod(events, 0o700)

	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	stderr := commit(t, r, "-m", "commit despite failure") // exits 0
	head := r.Head()
	if msg := strings.TrimSpace(r.Git("log", "-1", "--format=%s")); msg != "commit despite failure" {
		t.Fatalf("commit missing: %q", msg)
	}
	if !strings.Contains(stderr, "the commit was created, but its snapshot could not be recorded") ||
		!strings.Contains(stderr, "saving the snapshot") || !strings.Contains(stderr, "warning") {
		t.Errorf("stderr = %q", stderr)
	}
	if _, err := os.Stat(eventPath(r, head)); !os.IsNotExist(err) {
		t.Errorf("snapshot exists after failure: %v", err)
	}
}

func TestMovedBinary(t *testing.T) {
	r := testutil.NewRepo(t)
	dir := filepath.Join(t.TempDir(), "moved away")
	os.MkdirAll(dir, 0o755)
	copyBin := filepath.Join(dir, "commitcoach")
	data, _ := os.ReadFile(binPath)
	os.WriteFile(copyBin, data, 0o755)
	if res := runBin(t, r, "", copyBin, "init"); res.code != 0 {
		t.Fatalf("init: %+v", res)
	}
	os.Remove(copyBin)

	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	stderr := commit(t, r, "-m", "binary gone")
	if !strings.Contains(stderr, "executable is missing") || r.Head() == "" {
		t.Errorf("stderr = %q", stderr)
	}
	if res := cc(t, r, "", "status"); !strings.Contains(res.stdout, "MISSING") {
		t.Errorf("status does not report the missing binary:\n%s", res.stdout)
	}
	// Recovery: run init with a binary that exists.
	if res := cc(t, r, "", "init"); res.code != 0 || !strings.Contains(res.stdout, "updated the post-commit hook") {
		t.Fatalf("re-init: %+v", res)
	}
	r.Write("a.txt", "y\n")
	commit(t, r, "-a", "-m", "recovered")
	readEvent(t, r, r.Head())
}

func TestTimeout(t *testing.T) {
	r := testutil.NewRepo(t)
	r.CommitAll("one")
	fake := t.TempDir()
	os.WriteFile(filepath.Join(fake, "git"), []byte("#!/bin/sh\nexec sleep 10\n"), 0o755)
	r.Env = append(os.Environ(), "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, args := range [][]string{
		{"hook", "post-commit", "--timeout", "500ms"},
		{"export", "--timeout", "500ms"},
	} {
		start := time.Now()
		res := cc(t, r, "", args...)
		if res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "timed out") {
			t.Errorf("%v: %+v", args, res)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%v took %v", args, d)
		}
	}
}

func TestHookCommandOutput(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a.txt", "x\n")
	head := r.CommitAll("one")
	res := cc(t, r, "", "hook", "post-commit")
	if res.code != 0 || res.stdout != "" || strings.Count(res.stderr, "\n") != 1 || !strings.Contains(res.stderr, head) {
		t.Fatalf("hook: %+v", res)
	}
	readEvent(t, r, head)
	for _, args := range [][]string{{"hook"}, {"hook", "pre-push"}, {}, {"nope"}} {
		if res := cc(t, r, "", args...); res.code != 2 {
			t.Errorf("%v: code %d", args, res.code)
		}
	}
	if res := cc(t, r, "", "help"); res.code != 0 || !strings.Contains(res.stdout, "commitcoach init") {
		t.Errorf("help: %+v", res)
	}
}

func TestInitTwiceStatusAndUninstall(t *testing.T) {
	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "status"); res.code != 0 || !strings.Contains(res.stdout, "not installed") || !strings.Contains(res.stdout, "conflicts:         none") {
		t.Errorf("status before init:\n%+v", res)
	}
	cc(t, r, "", "init")
	hookPath := filepath.Join(r.Dir, ".git", "hooks", "post-commit")
	before, _ := os.ReadFile(hookPath)
	if res := cc(t, r, "", "init"); res.code != 0 || !strings.Contains(res.stdout, "already installed") {
		t.Errorf("second init: %+v", res)
	}
	if after, _ := os.ReadFile(hookPath); !bytes.Equal(before, after) {
		t.Error("second init changed the hook")
	}
	res := cc(t, r, "", "status")
	for _, want := range []string{"installed by commitcoach, unmodified", binPath + " (ok, the binary you are running)", "(Git default)", "not created yet"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, res.stdout)
		}
	}

	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "one")
	if res := cc(t, r, "", "uninstall"); res.code != 0 || !strings.Contains(res.stdout, "removed the post-commit hook") || !strings.Contains(res.stdout, "1 snapshot(s) were kept") {
		t.Errorf("uninstall: %+v", res)
	}
	if _, err := os.Lstat(hookPath); !os.IsNotExist(err) {
		t.Error("hook still exists")
	}
	readEvent(t, r, r.Head()) // snapshots are kept
	if res := cc(t, r, "", "uninstall"); res.code != 0 || !strings.Contains(res.stdout, "nothing to do") {
		t.Errorf("second uninstall: %+v", res)
	}
	r.Write("a.txt", "y\n")
	commit(t, r, "-a", "-m", "after uninstall")
	if _, err := os.Stat(eventPath(r, r.Head())); !os.IsNotExist(err) {
		t.Error("snapshot recorded after uninstall")
	}
}

func TestInitConflicts(t *testing.T) {
	t.Run("foreign hook", func(t *testing.T) {
		r := testutil.NewRepo(t)
		hookPath := filepath.Join(r.Dir, ".git", "hooks", "post-commit")
		foreign := "#!/bin/sh\necho mine\n"
		os.WriteFile(hookPath, []byte(foreign), 0o755)
		res := cc(t, r, "", "init")
		if res.code != 1 || !strings.Contains(res.stderr, "not created by commitcoach") || !strings.Contains(res.stderr, "No file or setting was changed") ||
			!strings.Contains(res.stderr, `'"'"'single'"'"'`) || !strings.Contains(res.stderr, "hook post-commit ||") {
			t.Errorf("init: %+v", res)
		}
		if got, _ := os.ReadFile(hookPath); string(got) != foreign {
			t.Error("foreign hook changed")
		}
		if res := cc(t, r, "", "uninstall"); res.code != 0 || !strings.Contains(res.stdout, "left unchanged") {
			t.Errorf("uninstall: %+v", res)
		}
		if got, _ := os.ReadFile(hookPath); string(got) != foreign {
			t.Error("uninstall changed the foreign hook")
		}
		if res := cc(t, r, "", "status"); !strings.Contains(res.stdout, "someone else's hook") {
			t.Errorf("status:\n%s", res.stdout)
		}
	})

	t.Run("local core.hooksPath", func(t *testing.T) {
		r := testutil.NewRepo(t)
		r.Git("config", "core.hooksPath", ".husky")
		cfg := filepath.Join(r.Dir, ".git", "config")
		before, _ := os.ReadFile(cfg)
		res := cc(t, r, "", "init")
		if res.code != 1 || !strings.Contains(res.stderr, "core.hooksPath = .husky (local scope") ||
			!strings.Contains(res.stderr, filepath.Join(".husky", "post-commit")) {
			t.Errorf("init: %+v", res)
		}
		if after, _ := os.ReadFile(cfg); !bytes.Equal(before, after) {
			t.Error("git config changed")
		}
		if _, err := os.Lstat(filepath.Join(r.Dir, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
			t.Error("hook written despite core.hooksPath")
		}
		if res := cc(t, r, "", "status"); !strings.Contains(res.stdout, "set by core.hooksPath") || !strings.Contains(res.stdout, "core.hooksPath = .husky") {
			t.Errorf("status:\n%s", res.stdout)
		}
	})

	t.Run("global core.hooksPath", func(t *testing.T) {
		r := testutil.NewRepo(t)
		global := filepath.Join(t.TempDir(), "global gitconfig")
		content := "[core]\n\thooksPath = /opt/company-hooks\n"
		os.WriteFile(global, []byte(content), 0o600)
		r.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+global)
		res := cc(t, r, "", "init")
		if res.code != 1 || !strings.Contains(res.stderr, "global scope") || !strings.Contains(res.stderr, "/opt/company-hooks/post-commit") {
			t.Errorf("init: %+v", res)
		}
		if got, _ := os.ReadFile(global); string(got) != content {
			t.Error("global config changed")
		}
	})
}

func TestInitRefusals(t *testing.T) {
	outside := &testutil.Repo{T: t, Dir: t.TempDir()}
	if res := cc(t, outside, "", "init"); res.code != 1 || !strings.Contains(res.stderr, "not a git repository") {
		t.Errorf("init outside a repository: %+v", res)
	}
	if res := cc(t, outside, "", "status"); res.code != 1 || !strings.Contains(res.stderr, "no usable git repository") {
		t.Errorf("status outside a repository: %+v", res)
	}
	bare := &testutil.Repo{T: t, Dir: filepath.Join(t.TempDir(), "bare.git")}
	outside.Git("init", "-q", "--bare", bare.Dir)
	if res := cc(t, bare, "", "init"); res.code != 1 || !strings.Contains(res.stderr, "bare repository") {
		t.Errorf("init in a bare repository: %+v", res)
	}
	if _, err := os.Lstat(filepath.Join(bare.Dir, "hooks", "post-commit")); !os.IsNotExist(err) {
		t.Error("hook written into a bare repository")
	}
}

func TestLinkedWorktree(t *testing.T) {
	r := installed(t)
	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "base")
	wt := &testutil.Repo{T: t, Dir: filepath.Join(t.TempDir(), "wt")}
	r.Git("worktree", "add", "-q", "-b", "feature", wt.Dir)
	wt.Write("b.txt", "from worktree\n")
	wt.Git("add", "-A")
	commit(t, wt, "-m", "in worktree")
	head := strings.TrimSpace(wt.Git("rev-parse", "HEAD"))
	ev := readEvent(t, r, head) // stored in the shared git directory
	if *ev.Repository.CurrentBranch != "feature" || len(ev.Files) != 1 || *ev.Files[0].NewPath != "b.txt" {
		t.Errorf("worktree snapshot: %+v %+v", ev.Repository, ev.Files)
	}
	if res := cc(t, wt, "", "status"); !strings.Contains(res.stdout, "linked worktree") || !strings.Contains(res.stdout, "installed by commitcoach") {
		t.Errorf("status in worktree:\n%s", res.stdout)
	}
}

func TestDetachedAmendAndMerge(t *testing.T) {
	r := installed(t)
	r.Write("a.txt", "one\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "one")
	base := r.Head()

	// Detached HEAD: current_branch is null.
	r.Git("checkout", "-q", "--detach")
	r.Write("a.txt", "detached\n")
	commit(t, r, "-a", "-m", "detached")
	if ev := readEvent(t, r, r.Head()); ev.Repository.CurrentBranch != nil {
		t.Errorf("current_branch = %q", *ev.Repository.CurrentBranch)
	}
	r.Git("checkout", "-q", "main")

	// Amend: the new commit gets its own snapshot; the old one is kept.
	r.Write("a.txt", "draft\n")
	commit(t, r, "-a", "-m", "draft")
	draft := r.Head()
	r.Write("a.txt", "final\n")
	commit(t, r, "-a", "--amend", "-m", "final")
	amended := r.Head()
	ev := readEvent(t, r, amended)
	if ev.Commit.Message != "final\n" || *ev.Comparison.BaseSHA != base || !strings.Contains(*ev.Files[0].Patch, "+final") {
		t.Errorf("amended snapshot: %+v %+v", ev.Commit, ev.Files)
	}
	readEvent(t, r, draft)

	// Merge commit, exported: compared with the first parent.
	r.Git("checkout", "-q", "-b", "topic", base)
	r.Write("topic.txt", "topic\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "topic")
	r.Git("checkout", "-q", "main")
	r.Git("merge", "-q", "--no-ff", "-m", "merge topic", "topic")
	res := cc(t, r, "", "export")
	ev = decodeEvent(t, []byte(res.stdout))
	if len(ev.Commit.Parents) != 2 || *ev.Comparison.BaseSHA != amended || ev.Comparison.Strategy != "first_parent" ||
		len(ev.Files) != 1 || *ev.Files[0].NewPath != "topic.txt" {
		t.Errorf("merge export: %+v %+v %+v", ev.Commit, ev.Comparison, ev.Files)
	}
}

// TestOtherGitDirLayouts covers repositories whose git directory is not
// <worktree>/.git: a submodule and a repository made with --separate-git-dir.
func TestOtherGitDirLayouts(t *testing.T) {
	check := func(t *testing.T, work *testutil.Repo, gitDir string) {
		t.Helper()
		if res := cc(t, work, "", "init"); res.code != 0 {
			t.Fatalf("init: %+v", res)
		}
		if _, err := os.Stat(filepath.Join(gitDir, "hooks", "post-commit")); err != nil {
			t.Fatalf("hook not in %s: %v", gitDir, err)
		}
		work.Write("new.txt", "hello\n")
		work.Git("add", "new.txt")
		commit(t, work, "-m", "in "+filepath.Base(work.Dir))
		head := strings.TrimSpace(work.Git("rev-parse", "HEAD"))
		data, err := os.ReadFile(filepath.Join(gitDir, "commitcoach", "events", head+".json"))
		if err != nil {
			t.Fatalf("snapshot not in %s: %v", gitDir, err)
		}
		if ev := decodeEvent(t, data); ev.Commit.SHA != head || *ev.Files[0].NewPath != "new.txt" {
			t.Errorf("snapshot: %+v", ev.Files)
		}
	}

	t.Run("submodule", func(t *testing.T) {
		lib := testutil.NewRepo(t)
		lib.Write("lib.txt", "lib\n")
		lib.CommitAll("lib")
		super := testutil.NewRepo(t)
		super.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", lib.Dir, "sub")
		super.CommitAll("add submodule")
		sub := &testutil.Repo{T: t, Dir: filepath.Join(super.Dir, "sub")}
		sub.Configure()
		check(t, sub, filepath.Join(super.Dir, ".git", "modules", "sub"))
	})

	t.Run("separate git dir", func(t *testing.T) {
		work := &testutil.Repo{T: t, Dir: t.TempDir()}
		gitDir := filepath.Join(t.TempDir(), "elsewhere.git")
		work.Git("init", "-q", "-b", "main", "--separate-git-dir", gitDir)
		work.Configure()
		check(t, work, gitDir)
		if _, err := os.Stat(filepath.Join(work.Dir, "commitcoach")); !os.IsNotExist(err) {
			t.Error("files were written into the working tree")
		}
	})
}

// TestExportInBareRepository: export only needs objects, so it works in a
// bare repository even though init refuses it.
func TestExportInBareRepository(t *testing.T) {
	src := testutil.NewRepo(t)
	src.Write("a.txt", "one\n")
	src.CommitAll("one")
	src.Write("a.txt", "two\n")
	head := src.CommitAll("two")
	bare := &testutil.Repo{T: t, Dir: filepath.Join(t.TempDir(), "bare.git")}
	src.Git("clone", "-q", "--bare", src.Dir, bare.Dir)
	res := cc(t, bare, "", "export")
	if res.code != 0 {
		t.Fatalf("export: %+v", res)
	}
	ev := decodeEvent(t, []byte(res.stdout))
	if ev.Commit.SHA != head || ev.Repository.Name != "bare" || *ev.Repository.CurrentBranch != "main" ||
		len(ev.Files) != 1 || !strings.Contains(*ev.Files[0].Patch, "+two") {
		t.Errorf("bare export: %+v %+v", ev.Repository, ev.Files)
	}
}
