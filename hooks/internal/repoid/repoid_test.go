package repoid_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/repoid"
	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestMain(m *testing.M) { testutil.Main(m, nil) }

const (
	idA = "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	idB = "9b8a7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

func TestNormalize(t *testing.T) {
	if got, err := repoid.Normalize("3F1C2D4E-5A6B-4C7D-8E9F-0A1B2C3D4E5F"); err != nil || got != idA {
		t.Errorf("uppercase: %q, %v", got, err)
	}
	for _, bad := range []string{
		"", "abc", "3f1c2d4e5a6b4c7d8e9f0a1b2c3d4e5f", "{" + idA + "}", idA + " ", " " + idA,
		"3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5g", "3f1c2d4e-5a6b-4c7d-8e9f-0a1b2c3d4e5", "00000000-0000-0000-0000-000000000000",
	} {
		if got, err := repoid.Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", bad, got)
		}
	}
}

func TestLoadSaveResolve(t *testing.T) {
	r := testutil.NewRepo(t)
	ctx := context.Background()
	repo, err := gitrepo.Open(ctx, r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repoid.Load(ctx, repo); !errors.Is(err, repoid.ErrNotSet) {
		t.Fatalf("Load before saving: %v", err)
	}
	if _, err := repoid.Resolve(ctx, repo, ""); !errors.Is(err, repoid.ErrNotSet) || !strings.Contains(err.Error(), "init --repository-id") {
		t.Fatalf("Resolve without anything: %v", err)
	}
	if _, err := repoid.Resolve(ctx, repo, "not-a-uuid"); err == nil {
		t.Fatal("Resolve accepted an invalid flag")
	}

	global := testutil.GlobalConfig()
	globalBefore, _ := os.ReadFile(global)
	if err := repoid.Save(ctx, repo, strings.ToUpper(idA)); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(r.Git("config", "--local", "--get", repoid.ConfigKey)); got != idA {
		t.Errorf("local config = %q", got)
	}
	if after, _ := os.ReadFile(global); string(after) != string(globalBefore) {
		t.Error("the global config was changed")
	}
	if got, err := repoid.Load(ctx, repo); err != nil || got != idA {
		t.Errorf("Load = %q, %v", got, err)
	}

	// A flag wins for that call only; the saved value stays.
	cfg := filepath.Join(r.Dir, ".git", "config")
	before, _ := os.ReadFile(cfg)
	if got, err := repoid.Resolve(ctx, repo, idB); err != nil || got != idB {
		t.Errorf("Resolve with a flag = %q, %v", got, err)
	}
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Error("Resolve changed the saved config")
	}
	if got, _ := repoid.Resolve(ctx, repo, ""); got != idA {
		t.Errorf("Resolve without a flag = %q", got)
	}

	// Saving again replaces the value instead of adding a second one.
	if err := repoid.Save(ctx, repo, idB); err != nil {
		t.Fatal(err)
	}
	if got := r.Git("config", "--local", "--get-all", repoid.ConfigKey); got != idB+"\n" {
		t.Errorf("values after a second save: %q", got)
	}
}

func TestLoadIgnoresOtherScopesAndRejectsBadValues(t *testing.T) {
	r := testutil.NewRepo(t)
	ctx := context.Background()
	global := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(global, []byte("[commitcoach]\n\trepositoryId = "+idA+"\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	repo, err := gitrepo.Open(ctx, r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repoid.Load(ctx, repo); !errors.Is(err, repoid.ErrNotSet) {
		t.Errorf("a global setting was used: %v", err)
	}
	r.Git("config", "--local", repoid.ConfigKey, "placeholder")
	if _, err := repoid.Load(ctx, repo); err == nil || errors.Is(err, repoid.ErrNotSet) || !strings.Contains(err.Error(), "not a UUID") {
		t.Errorf("invalid saved value: %v", err)
	}
}
