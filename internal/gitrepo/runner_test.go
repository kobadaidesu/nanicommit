package gitrepo_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
)

// fakeGit puts a "git" on PATH that ignores its arguments and runs script.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as fake git")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRunnerTimeout(t *testing.T) {
	// The fake git leaves a child running that holds stdout and stderr open,
	// like a hung helper process would.
	fakeGit(t, "sleep 5\necho late")
	run := &gitrepo.Runner{Dir: t.TempDir()}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := run.Output(ctx, "version")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Output err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("Output returned after %v", d)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	start = time.Now()
	s, err := run.Start(ctx2, nil, "diff-tree")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(s); err == nil {
		t.Log("read ended without error")
	}
	if err := s.Finish(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Finish err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("streaming read returned after %v", d)
	}
}

func TestOpenTimeout(t *testing.T) {
	fakeGit(t, "exec sleep 5")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := gitrepo.Open(ctx, t.TempDir()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestGitNotRunnable(t *testing.T) {
	fakeGit(t, "echo 'fatal: simulated failure' >&2\nexit 3")
	_, err := gitrepo.Open(context.Background(), t.TempDir())
	var gerr *gitrepo.Error
	if !errors.As(err, &gerr) || gerr.ExitCode != 3 || gerr.Stderr == "" {
		t.Fatalf("err = %#v", err)
	}
}
