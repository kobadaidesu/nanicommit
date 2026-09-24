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
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kobadaidesu/hook-test/internal/event"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

var binPath string

// Made-up repository IDs for the tests.
const (
	testID  = "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	otherID = "9b8a7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

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

var payloadKeys = []string{"branch", "commit_sha", "diff", "files", "message", "repository_id"}

// decodePayload parses exactly one JSON object that has exactly the six
// payload keys.
func decodePayload(t *testing.T, data []byte) *event.Payload {
	t.Helper()
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, data)
	}
	var keys []string
	for k := range generic {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, payloadKeys) {
		t.Fatalf("top-level keys %v, want exactly %v", keys, payloadKeys)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p event.Payload
	if err := dec.Decode(&p); err != nil {
		t.Fatalf("invalid payload: %v\n%s", err, data)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("more than one JSON value in output:\n%s", data)
	}
	if p.Files == nil {
		t.Fatalf("files is null:\n%s", data)
	}
	return &p
}

func readPayload(t *testing.T, r *testutil.Repo, oid string) *event.Payload {
	t.Helper()
	data, err := os.ReadFile(eventPath(r, oid))
	if err != nil {
		t.Fatalf("JSON of %s not saved: %v", oid, err)
	}
	return decodePayload(t, data)
}

func installed(t *testing.T) *testutil.Repo {
	t.Helper()
	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "init", "--repository-id", testID); res.code != 0 {
		t.Fatalf("init failed: %+v", res)
	}
	return r
}

func TestInitAndCommitRecordPayloads(t *testing.T) {
	r := testutil.NewRepo(t)
	os.MkdirAll(filepath.Join(r.Dir, "sub", "dir"), 0o755)
	res := cc(t, r, filepath.Join(r.Dir, "sub", "dir"), "init", "--repository-id", testID) // from a subdirectory
	if res.code != 0 || !strings.Contains(res.stdout, "installed the post-commit hook") || !strings.Contains(res.stdout, "repository id: "+testID) {
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
	p := readPayload(t, r, first)
	if p.RepositoryID != testID || p.CommitSHA != first || p.Branch == nil || *p.Branch != "main" || p.Message != "Initial commit\n" ||
		strings.Join(p.Files, ",") != "src/add.go" || !strings.HasPrefix(p.Diff, "diff --git a/src/add.go b/src/add.go\nnew file mode 100644\n") {
		t.Errorf("first payload: %+v", p)
	}

	// A commit with several files.
	r.Write("src/add.go", "package calc\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n")
	r.Write("src/sub.go", "package calc\n\nfunc sub(a, b int) int {\n\treturn a - b\n}\n")
	r.Write("docs/メモ 1.md", "# メモ\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "足し算を修正\n\n- sub を追加\n- メモを追加")
	second := r.Head()
	p = readPayload(t, r, second)
	if p.CommitSHA != second || p.Message != "足し算を修正\n\n- sub を追加\n- メモを追加\n" {
		t.Errorf("second payload: %+v", p)
	}
	if strings.Join(p.Files, "|") != "docs/メモ 1.md|src/add.go|src/sub.go" {
		t.Errorf("files = %q", p.Files)
	}
	for _, want := range []string{"diff --git a/docs/メモ 1.md b/docs/メモ 1.md\n", "+# メモ\n", "-\treturn a - b\n+\treturn a + b\n", "diff --git a/src/sub.go b/src/sub.go\nnew file mode"} {
		if !strings.Contains(p.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, p.Diff)
		}
	}
	if fi, err := os.Stat(eventPath(r, second)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("JSON file mode: %v %v", fi.Mode().Perm(), err)
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
	p := decodePayload(t, data)
	if strings.Join(p.Files, ",") != "a.txt" || !strings.Contains(p.Diff, "+a COMMITTED") {
		t.Errorf("payload = %+v", p)
	}
	for _, leak := range []string{"STAGED_NOT_COMMITTED", "UNSTAGED_NOT_COMMITTED", "UNTRACKED_FILE", "b.txt", "c.txt", "d.txt"} {
		if bytes.Contains(data, []byte(leak)) {
			t.Errorf("%s leaked into the JSON", leak)
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
	r.Write("a.txt", "UNCOMMITTED\n")

	res := cc(t, r, "", "export")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("export: code %d stderr %q", res.code, res.stderr)
	}
	p := decodePayload(t, []byte(res.stdout)) // stdout is exactly one JSON value
	if p.CommitSHA != head || p.RepositoryID != testID || strings.Contains(res.stdout, "UNCOMMITTED") {
		t.Errorf("payload = %+v", p)
	}

	// export and the hook produce the same bytes for the same commit.
	hookJSON, _ := os.ReadFile(eventPath(r, head))
	if string(hookJSON) != res.stdout {
		t.Errorf("export differs from the hook output:\n%s\n%s", hookJSON, res.stdout)
	}

	res = cc(t, r, "", "export", "--commit", first[:12], "--output", "-")
	if p := decodePayload(t, []byte(res.stdout)); res.code != 0 || p.CommitSHA != first || !strings.Contains(p.Diff, "new file mode") {
		t.Errorf("export of the first commit: %+v", res)
	}

	res = cc(t, r, "", "export", "--commit", "HEAD~1", "--output", "./payload.json")
	out := filepath.Join(r.Dir, "payload.json")
	data, err := os.ReadFile(out)
	if res.code != 0 || res.stdout != "" || err != nil || !strings.Contains(res.stderr, "wrote the snapshot") {
		t.Fatalf("export to file: %+v, %v", res, err)
	}
	if decodePayload(t, data).CommitSHA != first {
		t.Error("wrong commit in file")
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestRepositoryID(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Write("a.txt", "x\n")
	r.CommitAll("one")
	cfg := filepath.Join(r.Dir, ".git", "config")
	hookPath := filepath.Join(r.Dir, ".git", "hooks", "post-commit")

	// Neither a flag nor a saved ID: an error that says how to set one.
	for _, args := range [][]string{{"export"}, {"init"}} {
		res := cc(t, r, "", args...)
		if res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "repository_id is not set") || !strings.Contains(res.stderr, "--repository-id") {
			t.Errorf("%v without an ID: %+v", args, res)
		}
	}
	if _, err := os.Lstat(hookPath); !os.IsNotExist(err) {
		t.Error("init installed the hook without an ID")
	}
	// Invalid IDs are refused.
	for _, bad := range []string{"", "abc", "00000000-0000-0000-0000-000000000000", testID + "0"} {
		res := cc(t, r, "", "export", "--repository-id="+bad)
		if res.code != 1 || res.stdout != "" {
			t.Errorf("export --repository-id=%q: %+v", bad, res)
		}
	}
	if res := cc(t, r, "", "init", "--repository-id", "not-a-uuid"); res.code != 1 || !strings.Contains(res.stderr, "not a UUID") {
		t.Errorf("init with an invalid ID: %+v", res)
	}
	if _, err := os.Lstat(hookPath); !os.IsNotExist(err) {
		t.Error("init installed the hook with an invalid ID")
	}

	// A flag works without saving anything.
	before, _ := os.ReadFile(cfg)
	res := cc(t, r, "", "export", "--repository-id", strings.ToUpper(testID))
	if p := decodePayload(t, []byte(res.stdout)); res.code != 0 || p.RepositoryID != testID {
		t.Errorf("export with a flag: %+v", res)
	}
	if after, _ := os.ReadFile(cfg); !bytes.Equal(before, after) {
		t.Error("export --repository-id changed the saved config")
	}

	// init saves the ID in the local config only.
	global := testutil.GlobalConfig()
	globalBefore, _ := os.ReadFile(global)
	if res := cc(t, r, "", "init", "--repository-id", testID); res.code != 0 || !strings.Contains(res.stdout, "saved as commitcoach.repositoryId") {
		t.Fatalf("init: %+v", res)
	}
	if got := strings.TrimSpace(r.Git("config", "--local", "--get", "commitcoach.repositoryId")); got != testID {
		t.Errorf("local config = %q", got)
	}
	if after, _ := os.ReadFile(global); !bytes.Equal(globalBefore, after) {
		t.Error("the global config was changed")
	}
	if res := cc(t, r, "", "status"); !strings.Contains(res.stdout, "repository id:     "+testID) {
		t.Errorf("status:\n%s", res.stdout)
	}

	// The saved ID is used; a flag overrides it for one run without saving.
	if p := decodePayload(t, []byte(cc(t, r, "", "export").stdout)); p.RepositoryID != testID {
		t.Errorf("saved ID not used: %s", p.RepositoryID)
	}
	before, _ = os.ReadFile(cfg)
	if p := decodePayload(t, []byte(cc(t, r, "", "export", "--repository-id", otherID).stdout)); p.RepositoryID != otherID {
		t.Errorf("flag did not override: %s", p.RepositoryID)
	}
	if after, _ := os.ReadFile(cfg); !bytes.Equal(before, after) {
		t.Error("export --repository-id changed the saved config")
	}

	// init again: without a flag it keeps the ID, with one it replaces it.
	if res := cc(t, r, "", "init"); res.code != 0 || !strings.Contains(res.stdout, "repository id: "+testID) {
		t.Errorf("init without a flag: %+v", res)
	}
	if res := cc(t, r, "", "init", "--repository-id", otherID); res.code != 0 {
		t.Errorf("init with a new ID: %+v", res)
	}
	if got := r.Git("config", "--local", "--get-all", "commitcoach.repositoryId"); got != otherID+"\n" {
		t.Errorf("local config after the change = %q", got)
	}
	r.Write("a.txt", "y\n")
	commit(t, r, "-a", "-m", "two")
	if p := readPayload(t, r, r.Head()); p.RepositoryID != otherID {
		t.Errorf("hook used %s", p.RepositoryID)
	}
}

func TestHookWithoutRepositoryIDKeepsCommit(t *testing.T) {
	r := installed(t)
	r.Git("config", "--local", "--unset", "commitcoach.repositoryId")
	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	stderr := commit(t, r, "-m", "no id")
	if !strings.Contains(stderr, "the commit was created, but its snapshot could not be recorded: repository_id is not set") || !strings.Contains(stderr, "warning") {
		t.Errorf("stderr = %q", stderr)
	}
	if strings.TrimSpace(r.Git("log", "-1", "--format=%s")) != "no id" {
		t.Error("commit missing")
	}
	if _, err := os.Stat(eventPath(r, r.Head())); !os.IsNotExist(err) {
		t.Error("JSON saved without an ID")
	}
}

func TestSensitiveAndBinaryFilesAreLeftOut(t *testing.T) {
	r := installed(t)
	r.Write(".env", "API_KEY=SECRET_ENV_VALUE\n")
	r.Write("keys/deploy.pem", "SECRET_PEM_VALUE\n")
	r.Write("logo.png", "\x89PNG\x00\x00binary")
	r.Write("app.py", "print('hello')\n")
	r.Git("add", "-A")
	stderr := commit(t, r, "-m", "secrets")
	head := r.Head()
	data, _ := os.ReadFile(eventPath(r, head))
	p := decodePayload(t, data)
	if strings.Join(p.Files, ",") != "app.py" || strings.Contains(p.Diff, ".env") || strings.Contains(p.Diff, "logo.png") {
		t.Errorf("payload = %+v", p)
	}
	for _, secret := range []string{"SECRET_ENV_VALUE", "SECRET_PEM_VALUE"} {
		if bytes.Contains(data, []byte(secret)) || strings.Contains(stderr, secret) {
			t.Errorf("%s leaked", secret)
		}
	}
	for _, want := range []string{"note: left out .env: the path looks like it holds secrets", "note: left out keys/deploy.pem:", "note: left out logo.png: binary file"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("hook stderr lacks %q:\n%s", want, stderr)
		}
	}
	res := cc(t, r, "", "export")
	if res.code != 0 || res.stdout != string(data) || !strings.Contains(res.stderr, "left out .env") {
		t.Errorf("export: %+v", res)
	}
}

func TestLimitsProduceNoJSON(t *testing.T) {
	r := installed(t)
	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "base")
	good := filepath.Join(r.Dir, "payload.json")
	if res := cc(t, r, "", "export", "--output", good); res.code != 0 {
		t.Fatal(res)
	}
	goodJSON, _ := os.ReadFile(good)

	for name, prepare := range map[string]func(){
		"101 files": func() {
			for i := 0; i < 101; i++ {
				r.Write(fmt.Sprintf("many/%03d.txt", i), "x\n")
			}
		},
		"large file": func() {
			r.Write("big.txt", strings.Repeat(strings.Repeat("0123456789", 7)+"\n", 1000)) // ~71 KiB
		},
	} {
		t.Run(name, func(t *testing.T) {
			prepare()
			r.Git("add", "-A")
			stderr := commit(t, r, "-m", name) // the hook fails, the commit stays
			head := r.Head()
			if !strings.Contains(stderr, "limit exceeded") || !strings.Contains(stderr, "no JSON was written") || !strings.Contains(stderr, "warning") {
				t.Errorf("hook stderr = %q", stderr)
			}
			if _, err := os.Stat(eventPath(r, head)); !os.IsNotExist(err) {
				t.Error("the hook saved JSON over the limit")
			}
			res := cc(t, r, "", "export")
			if res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "limit exceeded") {
				t.Errorf("export: %+v", res)
			}
			// A failed export leaves an existing output file as it was.
			res = cc(t, r, "", "export", "--output", good)
			if after, _ := os.ReadFile(good); res.code != 1 || !bytes.Equal(after, goodJSON) {
				t.Errorf("export --output over the limit: %+v", res)
			}
			entries, _ := os.ReadDir(r.Dir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".payload.json.tmp") {
					t.Errorf("temporary file left behind: %s", e.Name())
				}
			}
		})
	}
}

func TestExportErrors(t *testing.T) {
	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "export", "--repository-id", testID); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "does not name a commit") {
		t.Errorf("export in an empty repository: %+v", res)
	}
	r.Write("f.txt", "one\n")
	r.CommitAll("one")
	for _, rev := range []string{"no-such-branch", "HEAD~3", "HEAD^{tree}", "--output"} {
		if res := cc(t, r, "", "export", "--repository-id", testID, "--commit="+rev); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "does not name a commit") {
			t.Errorf("export --commit=%s: %+v", rev, res)
		}
	}
	if res := cc(t, r, "", "export", "extra"); res.code != 2 {
		t.Errorf("unexpected argument: %+v", res)
	}
	if res := cc(t, r, t.TempDir(), "export", "--repository-id", testID); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "no usable git repository") {
		t.Errorf("export outside a repository: %+v", res)
	}

	// A git failure (missing object) is reported, not replaced by empty data.
	r.Write("f.txt", "two\n")
	r.CommitAll("two")
	blob := strings.TrimSpace(r.Git("rev-parse", "HEAD:f.txt"))
	os.Remove(filepath.Join(r.Dir, ".git", "objects", blob[:2], blob[2:]))
	if res := cc(t, r, "", "export", "--repository-id", testID); res.code != 1 || res.stdout != "" || !strings.Contains(res.stderr, "git diff-tree exited with status") {
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
		t.Errorf("JSON exists after failure: %v", err)
	}
}

func TestMovedBinary(t *testing.T) {
	r := testutil.NewRepo(t)
	dir := filepath.Join(t.TempDir(), "moved away")
	os.MkdirAll(dir, 0o755)
	copyBin := filepath.Join(dir, "commitcoach")
	data, _ := os.ReadFile(binPath)
	os.WriteFile(copyBin, data, 0o755)
	if res := runBin(t, r, "", copyBin, "init", "--repository-id", testID); res.code != 0 {
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
	// Recovery: run init with a binary that exists (the saved ID is reused).
	if res := cc(t, r, "", "init"); res.code != 0 || !strings.Contains(res.stdout, "updated the post-commit hook") {
		t.Fatalf("re-init: %+v", res)
	}
	r.Write("a.txt", "y\n")
	commit(t, r, "-a", "-m", "recovered")
	readPayload(t, r, r.Head())
}

func TestTimeout(t *testing.T) {
	r := testutil.NewRepo(t)
	r.CommitAll("one")
	fake := t.TempDir()
	os.WriteFile(filepath.Join(fake, "git"), []byte("#!/bin/sh\nexec sleep 10\n"), 0o755)
	r.Env = append(os.Environ(), "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, args := range [][]string{
		{"hook", "post-commit", "--timeout", "500ms"},
		{"export", "--repository-id", testID, "--timeout", "500ms"},
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
	r.Git("config", "--local", "commitcoach.repositoryId", testID)
	r.Write("a.txt", "x\n")
	head := r.CommitAll("one")
	res := cc(t, r, "", "hook", "post-commit")
	if res.code != 0 || res.stdout != "" || strings.Count(res.stderr, "\n") != 1 || !strings.Contains(res.stderr, head) {
		t.Fatalf("hook: %+v", res)
	}
	readPayload(t, r, head)
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
	res := cc(t, r, "", "status")
	if res.code != 0 || !strings.Contains(res.stdout, "not installed") || !strings.Contains(res.stdout, "conflicts:         none") ||
		!strings.Contains(res.stdout, "repository id:     not set") {
		t.Errorf("status before init:\n%+v", res)
	}
	cc(t, r, "", "init", "--repository-id", testID)
	hookPath := filepath.Join(r.Dir, ".git", "hooks", "post-commit")
	before, _ := os.ReadFile(hookPath)
	if res := cc(t, r, "", "init"); res.code != 0 || !strings.Contains(res.stdout, "already installed") {
		t.Errorf("second init: %+v", res)
	}
	if after, _ := os.ReadFile(hookPath); !bytes.Equal(before, after) {
		t.Error("second init changed the hook")
	}
	res = cc(t, r, "", "status")
	for _, want := range []string{"installed by commitcoach, unmodified", binPath + " (ok, the binary you are running)", "(Git default)", "not created yet", testID} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, res.stdout)
		}
	}

	r.Write("a.txt", "x\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "one")
	res = cc(t, r, "", "uninstall")
	if res.code != 0 || !strings.Contains(res.stdout, "removed the post-commit hook") || !strings.Contains(res.stdout, "1 snapshot(s) were kept") ||
		!strings.Contains(res.stdout, "git config --local --unset commitcoach.repositoryId") {
		t.Errorf("uninstall: %+v", res)
	}
	if _, err := os.Lstat(hookPath); !os.IsNotExist(err) {
		t.Error("hook still exists")
	}
	readPayload(t, r, r.Head()) // saved JSON is kept
	if res := cc(t, r, "", "uninstall"); res.code != 0 || !strings.Contains(res.stdout, "nothing to do") {
		t.Errorf("second uninstall: %+v", res)
	}
	r.Write("a.txt", "y\n")
	commit(t, r, "-a", "-m", "after uninstall")
	if _, err := os.Stat(eventPath(r, r.Head())); !os.IsNotExist(err) {
		t.Error("JSON recorded after uninstall")
	}
}

func TestInitConflicts(t *testing.T) {
	t.Run("foreign hook", func(t *testing.T) {
		r := testutil.NewRepo(t)
		hookPath := filepath.Join(r.Dir, ".git", "hooks", "post-commit")
		foreign := "#!/bin/sh\necho mine\n"
		os.WriteFile(hookPath, []byte(foreign), 0o755)
		cfg := filepath.Join(r.Dir, ".git", "config")
		cfgBefore, _ := os.ReadFile(cfg)
		res := cc(t, r, "", "init", "--repository-id", testID)
		if res.code != 1 || !strings.Contains(res.stderr, "not created by commitcoach") || !strings.Contains(res.stderr, "No file or setting was changed") ||
			!strings.Contains(res.stderr, `'"'"'single'"'"'`) || !strings.Contains(res.stderr, "hook post-commit ||") ||
			!strings.Contains(res.stderr, "git config --local commitcoach.repositoryId "+testID) {
			t.Errorf("init: %+v", res)
		}
		if got, _ := os.ReadFile(hookPath); string(got) != foreign {
			t.Error("foreign hook changed")
		}
		if after, _ := os.ReadFile(cfg); !bytes.Equal(cfgBefore, after) {
			t.Error("git config changed")
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
		res := cc(t, r, "", "init", "--repository-id", testID)
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
		res := cc(t, r, "", "init", "--repository-id", testID)
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
	if res := cc(t, outside, "", "init", "--repository-id", testID); res.code != 1 || !strings.Contains(res.stderr, "not a git repository") {
		t.Errorf("init outside a repository: %+v", res)
	}
	if res := cc(t, outside, "", "status"); res.code != 1 || !strings.Contains(res.stderr, "no usable git repository") {
		t.Errorf("status outside a repository: %+v", res)
	}
	bare := &testutil.Repo{T: t, Dir: filepath.Join(t.TempDir(), "bare.git")}
	outside.Git("init", "-q", "--bare", bare.Dir)
	if res := cc(t, bare, "", "init", "--repository-id", testID); res.code != 1 || !strings.Contains(res.stderr, "bare repository") {
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
	p := readPayload(t, r, head) // stored in the shared git directory; the ID is shared too
	if *p.Branch != "feature" || strings.Join(p.Files, ",") != "b.txt" || p.RepositoryID != testID {
		t.Errorf("worktree payload: %+v", p)
	}
	if res := cc(t, wt, "", "status"); !strings.Contains(res.stdout, "linked worktree") || !strings.Contains(res.stdout, "installed by commitcoach") {
		t.Errorf("status in worktree:\n%s", res.stdout)
	}
}

func TestDetachedEmptyAmendAndMerge(t *testing.T) {
	r := installed(t)
	r.Write("a.txt", "one\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "one")
	base := r.Head()

	// Detached HEAD: branch is null.
	r.Git("checkout", "-q", "--detach")
	r.Write("a.txt", "detached\n")
	commit(t, r, "-a", "-m", "detached")
	data, _ := os.ReadFile(eventPath(r, r.Head()))
	if p := decodePayload(t, data); p.Branch != nil || !bytes.Contains(data, []byte(`"branch": null`)) {
		t.Errorf("branch = %q", *p.Branch)
	}
	r.Git("checkout", "-q", "main")

	// Empty commit: files [] and an empty diff.
	commit(t, r, "--allow-empty", "-m", "empty")
	data, _ = os.ReadFile(eventPath(r, r.Head()))
	if p := decodePayload(t, data); len(p.Files) != 0 || p.Diff != "" || !bytes.Contains(data, []byte(`"files": []`)) {
		t.Errorf("empty commit:\n%s", data)
	}

	// Amend: the new commit gets its own JSON; the old one is kept.
	r.Write("a.txt", "draft\n")
	commit(t, r, "-a", "-m", "draft")
	draft := r.Head()
	r.Write("a.txt", "final\n")
	commit(t, r, "-a", "--amend", "-m", "final")
	amended := r.Head()
	if p := readPayload(t, r, amended); p.Message != "final\n" || !strings.Contains(p.Diff, "-one\n+final\n") {
		t.Errorf("amended payload: %+v", p)
	}
	readPayload(t, r, draft)

	// Merge commit, exported: compared with the first parent.
	r.Git("checkout", "-q", "-b", "topic", base)
	r.Write("topic.txt", "topic\n")
	r.Git("add", "-A")
	commit(t, r, "-m", "topic")
	r.Git("checkout", "-q", "main")
	r.Git("merge", "-q", "--no-ff", "-m", "merge topic", "topic")
	res := cc(t, r, "", "export")
	p := decodePayload(t, []byte(res.stdout))
	if strings.Join(p.Files, ",") != "topic.txt" || strings.Contains(p.Diff, "a.txt") || !strings.Contains(res.stderr, "relative to the first parent only") {
		t.Errorf("merge export: %+v\n%s", p, res.stderr)
	}
}

// TestOtherGitDirLayouts covers repositories whose git directory is not
// <worktree>/.git: a submodule and a repository made with --separate-git-dir.
func TestOtherGitDirLayouts(t *testing.T) {
	check := func(t *testing.T, work *testutil.Repo, gitDir string) {
		t.Helper()
		if res := cc(t, work, "", "init", "--repository-id", testID); res.code != 0 {
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
			t.Fatalf("JSON not in %s: %v", gitDir, err)
		}
		if p := decodePayload(t, data); p.CommitSHA != head || strings.Join(p.Files, ",") != "new.txt" {
			t.Errorf("payload: %+v", p)
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
	res := cc(t, bare, "", "export", "--repository-id", testID)
	if res.code != 0 {
		t.Fatalf("export: %+v", res)
	}
	if p := decodePayload(t, []byte(res.stdout)); p.CommitSHA != head || *p.Branch != "main" || !strings.Contains(p.Diff, "+two") {
		t.Errorf("bare export: %+v", p)
	}
}
