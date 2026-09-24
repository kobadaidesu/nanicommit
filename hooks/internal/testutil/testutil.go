// Package testutil creates throwaway Git repositories for tests.
//
// Main isolates every git process started by a test binary from the user's
// global and system configuration (hooks paths, signing, attributes, ...):
// HOME and XDG_CONFIG_HOME point to an empty temporary directory,
// GIT_CONFIG_GLOBAL to an empty file, GIT_CONFIG_NOSYSTEM is set and all
// other GIT_* variables are removed. Repositories get a local user.name and
// user.email.
package testutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Main is the body of TestMain for packages that run git. setup, if not nil,
// runs before the environment is isolated (e.g. to build a binary with the
// user's Go cache); the function it returns runs after the tests.
func Main(m *testing.M, setup func() (cleanup func(), err error)) {
	os.Exit(run(m, setup))
}

func run(m *testing.M, setup func() (func(), error)) int {
	if setup != nil {
		cleanup, err := setup()
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "test setup:", err)
			return 1
		}
	}
	home, err := os.MkdirTemp("", "commitcoach-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(home)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	os.Setenv("GIT_CONFIG_GLOBAL", global)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return m.Run()
}

// GlobalConfig returns the path of the isolated global git config file.
func GlobalConfig() string { return os.Getenv("GIT_CONFIG_GLOBAL") }

// Repo is a temporary repository.
type Repo struct {
	T   testing.TB
	Dir string
	// Env is the environment of commands run through the Repo; nil means
	// the (isolated) process environment.
	Env []string
}

// NewRepo creates a repository in a new temporary directory. Extra
// arguments are passed to git init (e.g. "--object-format=sha256").
func NewRepo(t testing.TB, initArgs ...string) *Repo {
	t.Helper()
	r := &Repo{T: t, Dir: t.TempDir()}
	r.Git(append([]string{"init", "-q", "-b", "main"}, initArgs...)...)
	r.Configure()
	return r
}

// Configure sets the local configuration every test repository needs.
func (r *Repo) Configure() {
	r.T.Helper()
	r.Git("config", "user.name", "Test User")
	r.Git("config", "user.email", "test@example.com")
	r.Git("config", "commit.gpgsign", "false")
	r.Git("config", "tag.gpgsign", "false")
}

// Cmd runs name with args in dir ("" means r.Dir) and returns its output.
func (r *Repo) Cmd(dir, name string, args ...string) (stdout, stderr string, err error) {
	if dir == "" {
		dir = r.Dir
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = r.Env
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

// GitErr runs git in the repository and returns its output and error.
func (r *Repo) GitErr(args ...string) (stdout, stderr string, err error) {
	return r.Cmd("", "git", args...)
}

// Git runs git in the repository and fails the test if it fails.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	out, errOut, err := r.GitErr(args...)
	if err != nil {
		r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// Write writes a file relative to the repository root, creating directories.
func (r *Repo) Write(rel, content string) {
	r.T.Helper()
	p := filepath.Join(r.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.T.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.T.Fatal(err)
	}
}

// CommitAll stages everything and commits it; it returns the new HEAD.
func (r *Repo) CommitAll(msg string) string {
	r.T.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
	return r.Head()
}

// Head returns the full object name of HEAD.
func (r *Repo) Head() string {
	r.T.Helper()
	return strings.TrimSpace(r.Git("rev-parse", "HEAD"))
}

// RealPath resolves symlinks (e.g. /var -> /private/var on macOS) so paths
// can be compared with the ones git prints.
func RealPath(t testing.TB, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
