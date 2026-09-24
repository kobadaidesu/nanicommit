package hooks_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/hooks"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestMain(m *testing.M) { testutil.Main(m, nil) }

// fakeExe creates an executable that appends its arguments to a log file.
func fakeExe(t *testing.T, dir string) (exe, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell hooks")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(dir, "commitcoach")
	log = filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, log
}

func openRepo(t *testing.T, dir string) *gitrepo.Repo {
	t.Helper()
	repo, err := gitrepo.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestScriptQuotesUnusualPaths(t *testing.T) {
	for _, dir := range []string{
		"plain",
		"with space",
		`single'quote`,
		`double"quote`,
		`dollar $HOME and $(echo x) and ` + "`backtick`",
		`back\slash`,
		"new\nline",
		"日本語 ディレクトリ",
	} {
		exe, log := fakeExe(t, filepath.Join(t.TempDir(), dir))
		script := filepath.Join(t.TempDir(), "post-commit")
		if err := os.WriteFile(script, hooks.Script(exe), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("sh", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%q: hook failed: %v\n%s", dir, err, out)
		}
		if got := readFile(t, log); got != "hook post-commit\n" {
			t.Errorf("%q: binary called with %q (output %s)", dir, got, out)
		}
		// The hook identifies itself and the binary it runs.
		st, err := hooksStateOf(script)
		if err != nil || st.State != hooks.Installed || st.Executable != exe {
			t.Errorf("%q: classified as %+v, %v", dir, st, err)
		}
	}
}

// hooksStateOf classifies a hook file by installing it into a scratch
// repository's hooks directory and inspecting it.
func hooksStateOf(script string) (hooks.HookFile, error) {
	dir, err := os.MkdirTemp("", "hookstate-")
	if err != nil {
		return hooks.HookFile{}, err
	}
	defer os.RemoveAll(dir)
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		return hooks.HookFile{}, errors.New(string(out))
	}
	data, err := os.ReadFile(script)
	if err != nil {
		return hooks.HookFile{}, err
	}
	hookPath := filepath.Join(dir, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hookPath, data, 0o755); err != nil {
		return hooks.HookFile{}, err
	}
	repo, err := gitrepo.Open(context.Background(), dir)
	if err != nil {
		return hooks.HookFile{}, err
	}
	st, err := hooks.Inspect(context.Background(), repo)
	if err != nil {
		return hooks.HookFile{}, err
	}
	return st.Hook, nil
}

func TestScriptExitsZeroWhenBinaryFailsOrIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell hooks")
	}
	dir := t.TempDir()
	failing := filepath.Join(dir, "failing")
	os.WriteFile(failing, []byte("#!/bin/sh\necho boom >&2\nexit 7\n"), 0o755)
	for exe, want := range map[string]string{
		failing:                           "exit status 7",
		filepath.Join(dir, "no-such-bin"): "executable is missing",
	} {
		script := filepath.Join(t.TempDir(), "post-commit")
		os.WriteFile(script, hooks.Script(exe), 0o755)
		out, err := exec.Command("sh", script).CombinedOutput()
		if err != nil {
			t.Errorf("%s: hook exited non-zero: %v", exe, err)
		}
		if !strings.Contains(string(out), want) || !strings.Contains(string(out), "the commit was created") {
			t.Errorf("%s: output %q lacks %q", exe, out, want)
		}
	}
}

func TestInstallIsIdempotentAndUpdates(t *testing.T) {
	r := testutil.NewRepo(t)
	repo := openRepo(t, r.Dir)
	exe, _ := fakeExe(t, filepath.Join(t.TempDir(), "bin one"))
	ctx := context.Background()

	res, err := hooks.Install(ctx, repo, exe)
	if err != nil || res.Action != hooks.Created {
		t.Fatalf("first install: %+v, %v", res, err)
	}
	hookPath := filepath.Join(repo.CommonDir, "hooks", "post-commit")
	first := readFile(t, hookPath)
	if fi, _ := os.Stat(hookPath); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}

	res, err = hooks.Install(ctx, repo, exe)
	if err != nil || res.Action != hooks.Unchanged || readFile(t, hookPath) != first {
		t.Fatalf("second install: %+v, %v", res, err)
	}
	if n := strings.Count(readFile(t, hookPath), "hook post-commit"); n != 1 {
		t.Errorf("the call appears %d times", n)
	}

	exe2, _ := fakeExe(t, filepath.Join(t.TempDir(), "bin two"))
	res, err = hooks.Install(ctx, repo, exe2)
	if err != nil || res.Action != hooks.Updated || res.PreviousExecutable != exe {
		t.Fatalf("update: %+v, %v", res, err)
	}
	if got := readFile(t, hookPath); got != string(hooks.Script(exe2)) {
		t.Errorf("hook after update:\n%s", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(hookPath))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".commitcoach") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestInstallKeepsForeignAndEditedHooks(t *testing.T) {
	r := testutil.NewRepo(t)
	repo := openRepo(t, r.Dir)
	exe, _ := fakeExe(t, t.TempDir())
	ctx := context.Background()
	hookPath := filepath.Join(repo.CommonDir, "hooks", "post-commit")

	foreign := "#!/bin/sh\necho someone else's hook\n"
	os.WriteFile(hookPath, []byte(foreign), 0o700)
	_, err := hooks.Install(ctx, repo, exe)
	var conflict *hooks.ConflictError
	if !errors.As(err, &conflict) || conflict.HookFile != hookPath || !strings.Contains(conflict.Line, "hook post-commit") {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, hookPath) != foreign {
		t.Fatal("foreign hook was changed")
	}
	if fi, _ := os.Stat(hookPath); fi.Mode().Perm() != 0o700 {
		t.Errorf("foreign hook mode changed to %v", fi.Mode().Perm())
	}
	if res, err := hooks.Uninstall(ctx, repo); err != nil || res.Action != hooks.LeftAlone || readFile(t, hookPath) != foreign {
		t.Fatalf("uninstall of foreign hook: %+v, %v", res, err)
	}

	// A commitcoach hook edited by the user is neither replaced nor removed.
	os.Remove(hookPath)
	if _, err := hooks.Install(ctx, repo, exe); err != nil {
		t.Fatal(err)
	}
	edited := readFile(t, hookPath) + "echo my addition\n"
	os.WriteFile(hookPath, []byte(edited), 0o755)
	if _, err := hooks.Install(ctx, repo, exe); !errors.As(err, &conflict) {
		t.Fatalf("install over edited hook: %v", err)
	}
	var modified *hooks.ModifiedHookError
	if _, err := hooks.Uninstall(ctx, repo); !errors.As(err, &modified) {
		t.Fatalf("uninstall of edited hook: %v", err)
	}
	if readFile(t, hookPath) != edited {
		t.Fatal("edited hook was changed")
	}
}

func TestInstallDoesNotFollowSymlink(t *testing.T) {
	r := testutil.NewRepo(t)
	repo := openRepo(t, r.Dir)
	exe, _ := fakeExe(t, t.TempDir())
	target := filepath.Join(t.TempDir(), "shared-hook")
	os.WriteFile(target, hooks.Script(exe), 0o755) // even a commitcoach script behind a link
	hookPath := filepath.Join(repo.CommonDir, "hooks", "post-commit")
	if err := os.Symlink(target, hookPath); err != nil {
		t.Fatal(err)
	}
	var conflict *hooks.ConflictError
	if _, err := hooks.Install(context.Background(), repo, exe); !errors.As(err, &conflict) {
		t.Fatalf("err = %v", err)
	}
	if res, err := hooks.Uninstall(context.Background(), repo); err != nil || res.Action != hooks.LeftAlone {
		t.Fatalf("uninstall: %+v, %v", res, err)
	}
	if fi, err := os.Lstat(hookPath); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced or removed")
	}
}

func TestInstallRespectsHooksPath(t *testing.T) {
	exe, _ := fakeExe(t, t.TempDir())
	ctx := context.Background()

	t.Run("local", func(t *testing.T) {
		r := testutil.NewRepo(t)
		r.Git("config", "core.hooksPath", ".githooks")
		configPath := filepath.Join(r.Dir, ".git", "config")
		before := readFile(t, configPath)
		repo := openRepo(t, r.Dir)
		_, err := hooks.Install(ctx, repo, exe)
		var conflict *hooks.ConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("err = %v", err)
		}
		if conflict.HookFile != filepath.Join(repo.WorkTree, ".githooks", "post-commit") ||
			len(conflict.Details) != 1 || !strings.Contains(conflict.Details[0], "local") {
			t.Errorf("conflict = %+v", conflict)
		}
		if readFile(t, configPath) != before {
			t.Error("git config was changed")
		}
		for _, p := range []string{filepath.Join(r.Dir, ".githooks"), filepath.Join(repo.CommonDir, "hooks", "post-commit")} {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Errorf("%s was created", p)
			}
		}
	})

	t.Run("global", func(t *testing.T) {
		r := testutil.NewRepo(t)
		global := filepath.Join(t.TempDir(), "gitconfig")
		os.WriteFile(global, []byte("[core]\n\thooksPath = ~/global-hooks\n"), 0o600)
		t.Setenv("GIT_CONFIG_GLOBAL", global)
		repo := openRepo(t, r.Dir)
		_, err := hooks.Install(ctx, repo, exe)
		var conflict *hooks.ConflictError
		if !errors.As(err, &conflict) || !strings.Contains(conflict.Details[0], "global") {
			t.Fatalf("err = %v", err)
		}
		if readFile(t, global) != "[core]\n\thooksPath = ~/global-hooks\n" {
			t.Error("global config was changed")
		}
		st, err := hooks.Inspect(ctx, repo)
		if err != nil || st.EffectiveHook == nil || st.Hook.State != hooks.NotInstalled {
			t.Fatalf("Inspect = %+v, %v", st, err)
		}
	})
}

func TestUninstallRemovesOnlyItsOwnHook(t *testing.T) {
	r := testutil.NewRepo(t)
	repo := openRepo(t, r.Dir)
	exe, _ := fakeExe(t, t.TempDir())
	ctx := context.Background()
	hooksDir := filepath.Join(repo.CommonDir, "hooks")
	other := filepath.Join(hooksDir, "pre-commit")
	os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	if res, err := hooks.Uninstall(ctx, repo); err != nil || res.Action != hooks.Absent {
		t.Fatalf("uninstall before install: %+v, %v", res, err)
	}
	if _, err := hooks.Install(ctx, repo, exe); err != nil {
		t.Fatal(err)
	}
	if res, err := hooks.Uninstall(ctx, repo); err != nil || res.Action != hooks.Removed {
		t.Fatalf("uninstall: %+v, %v", res, err)
	}
	if _, err := os.Lstat(filepath.Join(hooksDir, "post-commit")); !os.IsNotExist(err) {
		t.Error("post-commit still exists")
	}
	if readFile(t, other) != "#!/bin/sh\nexit 0\n" {
		t.Error("another hook was changed")
	}
	if res, err := hooks.Uninstall(ctx, repo); err != nil || res.Action != hooks.Absent {
		t.Fatalf("second uninstall: %+v, %v", res, err)
	}
}

func TestInstallRefusesBareRepository(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bare.git")
	(&testutil.Repo{T: t, Dir: t.TempDir()}).Git("init", "-q", "--bare", dir)
	exe, _ := fakeExe(t, t.TempDir())
	_, err := hooks.Install(context.Background(), openRepo(t, dir), exe)
	if err == nil || !strings.Contains(err.Error(), "bare repository") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "hooks", "post-commit")); !os.IsNotExist(err) {
		t.Error("hook was written into a bare repository")
	}
}
