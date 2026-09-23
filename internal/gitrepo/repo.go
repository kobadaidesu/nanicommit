package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrNotRepository is returned by Open when git cannot find a usable
// repository from the given directory.
var ErrNotRepository = errors.New("no usable git repository found")

// ErrUnknownCommit is returned when a revision does not resolve to a commit.
var ErrUnknownCommit = errors.New("does not name a commit in this repository")

// Git 2.31 added "rev-parse --path-format=absolute", which Open relies on to
// get absolute paths no matter where it is run from.
const minGitMajor, minGitMinor = 2, 31

// Repo is an opened repository. All paths are absolute.
type Repo struct {
	run *Runner

	// GitVersion is the output of "git version".
	GitVersion string
	// WorkTree is the root of the working tree; "" for a bare repository or
	// when running inside the git directory.
	WorkTree string
	// GitDir is $GIT_DIR. In a linked worktree it is the per-worktree
	// directory (.git/worktrees/<name>).
	GitDir string
	// CommonDir is the directory shared by all worktrees. It holds objects,
	// refs and, by default, hooks.
	CommonDir string
	Bare      bool
}

// Open finds the repository that contains dir ("" means the current
// directory), the same way git itself does. GIT_DIR and related environment
// variables, which git sets when running hooks, are honored.
func Open(ctx context.Context, dir string) (*Repo, error) {
	run := &Runner{Dir: dir}
	version, err := checkGitVersion(ctx, run)
	if err != nil {
		return nil, err
	}
	out, err := run.Output(ctx, "rev-parse", "--is-bare-repository", "--is-inside-work-tree")
	if err != nil {
		var gerr *Error
		if errors.As(err, &gerr) && gerr.ExitCode == 128 {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, strings.TrimSpace(gerr.Stderr))
		}
		return nil, err
	}
	flags := strings.Fields(string(out)) // two "true"/"false" words, no paths
	if len(flags) != 2 {
		return nil, fmt.Errorf("unexpected output from git rev-parse: %q", out)
	}
	r := &Repo{run: run, GitVersion: version, Bare: flags[0] == "true"}
	if r.GitDir, err = r.absPath(ctx, "--git-dir"); err != nil {
		return nil, err
	}
	if r.CommonDir, err = r.absPath(ctx, "--git-common-dir"); err != nil {
		return nil, err
	}
	if flags[1] == "true" {
		if r.WorkTree, err = r.absPath(ctx, "--show-toplevel"); err != nil {
			return nil, err
		}
	}
	return r, nil
}

var versionRE = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

func checkGitVersion(ctx context.Context, run *Runner) (string, error) {
	out, err := run.Output(ctx, "version")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if m := versionRE.FindStringSubmatch(v); m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		if major < minGitMajor || major == minGitMajor && minor < minGitMinor {
			return v, fmt.Errorf("%s is too old: commitcoach needs Git %d.%d or later", v, minGitMajor, minGitMinor)
		}
	}
	return v, nil
}

// absPath asks rev-parse for one path. Each path is requested separately so
// that a path containing a newline cannot be confused with a separator: the
// output is exactly the path plus one terminating newline.
func (r *Repo) absPath(ctx context.Context, option string) (string, error) {
	out, err := r.run.Output(ctx, "rev-parse", "--path-format=absolute", option)
	if err != nil {
		return "", err
	}
	p := strings.TrimSuffix(string(out), "\n")
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("git rev-parse %s returned %q, not an absolute path", option, p)
	}
	return p, nil
}

// DisplayName returns a human-readable repository name (the directory name).
// It is not a unique identifier.
func (r *Repo) DisplayName() string {
	if filepath.Base(r.CommonDir) == ".git" {
		return filepath.Base(filepath.Dir(r.CommonDir))
	}
	if r.WorkTree != "" {
		return filepath.Base(r.WorkTree)
	}
	return strings.TrimSuffix(filepath.Base(r.CommonDir), ".git")
}

// IsObjectID reports whether s is a full object name as printed by git:
// lowercase hexadecimal. The length is deliberately not fixed because both
// SHA-1 and SHA-256 repositories exist.
func IsObjectID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// ResolveCommit resolves rev (a branch, tag, HEAD, abbreviated ID, ...) to
// the full object name of a commit. Anything that is not a commit, such as a
// tree, a blob or an unknown name, is an error wrapping ErrUnknownCommit.
func (r *Repo) ResolveCommit(ctx context.Context, rev string) (string, error) {
	if rev == "" {
		return "", errors.New("empty commit name")
	}
	// --end-of-options keeps a rev such as "--help" from being read as an option.
	out, err := r.run.Output(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		if hasExitCode(err, 1) {
			return "", fmt.Errorf("%q %w (unknown, ambiguous, or not a commit)", rev, ErrUnknownCommit)
		}
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if !IsObjectID(oid) {
		return "", fmt.Errorf("git rev-parse returned %q for %q, not an object name", oid, rev)
	}
	return oid, nil
}

// CurrentBranch returns the branch checked out in this working tree at the
// time of the call, or ok=false when HEAD is detached.
func (r *Repo) CurrentBranch(ctx context.Context) (name string, ok bool, err error) {
	out, err := r.run.Output(ctx, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if hasExitCode(err, 1) { // HEAD is not a symbolic ref: detached
			return "", false, nil
		}
		return "", false, err
	}
	ref := strings.TrimSuffix(string(out), "\n")
	name, ok = strings.CutPrefix(ref, "refs/heads/")
	return name, ok, nil
}

// HooksDir returns the directory git runs hooks from, honoring
// core.hooksPath.
func (r *Repo) HooksDir(ctx context.Context) (string, error) {
	out, err := r.run.Output(ctx, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	p := strings.TrimSuffix(string(out), "\n")
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("git rev-parse --git-path hooks returned %q, not an absolute path", p)
	}
	return p, nil
}

// ConfigValue is one setting of a configuration key.
type ConfigValue struct {
	Scope  string // system, global, local, worktree, command
	Origin string // e.g. "file:/home/me/.gitconfig"
	Value  string
}

// ConfigValues returns every setting of key visible from this repository,
// from all scopes, in the order git reads them (the last one wins).
func (r *Repo) ConfigValues(ctx context.Context, key string) ([]ConfigValue, error) {
	out, err := r.run.Output(ctx, "config", "--show-scope", "--show-origin", "-z", "--get-all", key)
	if err != nil {
		if hasExitCode(err, 1) { // the key is not set
			return nil, nil
		}
		return nil, err
	}
	// Each setting is "scope NUL origin NUL value NUL".
	fields := strings.Split(string(out), "\x00")
	if len(fields)%3 != 1 || fields[len(fields)-1] != "" {
		return nil, fmt.Errorf("unexpected output from git config: %q", out)
	}
	var values []ConfigValue
	for i := 0; i+2 < len(fields); i += 3 {
		values = append(values, ConfigValue{Scope: fields[i], Origin: fields[i+1], Value: fields[i+2]})
	}
	return values, nil
}

// IsShallow reports whether the repository is a shallow clone.
func (r *Repo) IsShallow(ctx context.Context) (bool, error) {
	out, err := r.run.Output(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}
