package cli_test

// When the pre-push hook blocks a push for unpassed quizzes, it shows only
// "絶対に逃さないポン" nine times. These tests
// run a real "git push" to a local bare repository against a fake backend.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestPushBlockBanner(t *testing.T) {
	allowed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/push/check" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			CommitSHAs []string `json:"commit_shas"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		pending := []map[string]any{}
		if !allowed {
			for _, sha := range body.CommitSHAs {
				url := "http://localhost:5173/quizzes/" + testID
				pending = append(pending, map[string]any{"commit_sha": sha, "reason": "not_passed", "quiz_url": url})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"allowed": allowed, "pending_commits": pending})
	}))
	t.Cleanup(srv.Close)

	remote := filepath.Join(t.TempDir(), "remote.git")
	r := testutil.NewRepo(t)
	r.Git("init", "-q", "--bare", remote)
	if res := cc(t, r, "", "init", "--repository-id", testID); res.code != 0 {
		t.Fatalf("init: %+v", res)
	}
	r.Write("a.txt", "x\n")
	r.CommitAll("one")
	// Configure the backend only now, so that the commit above is not sent.
	r.Git("config", "--local", "commitcoach.backendUrl", srv.URL)
	r.Git("config", "--local", "commitcoach.userId", testUser)

	_, stderr, err := r.GitErr("push", remote, "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("push was not blocked:\n%s", stderr)
	}
	// The hook's stderr is a pipe here, so the banner is plain text.
	if !strings.Contains(stderr, strings.Repeat("絶対に逃さないポン\n", 9)) || strings.Count(stderr, "絶対に逃さないポン") != 9 || strings.Contains(stderr, "\x1b[") ||
		strings.Contains(stderr, "nanicommit:") || strings.Contains(stderr, "commitcoach") {
		t.Errorf("blocked push stderr:\n%q", stderr)
	}

	allowed = true
	_, stderr, err = r.GitErr("push", remote, "HEAD:refs/heads/main")
	if err != nil || strings.Contains(stderr, "逃さないポン") || !strings.Contains(stderr, "have passed their quizzes") {
		t.Errorf("allowed push: err=%v\n%s", err, stderr)
	}
}
