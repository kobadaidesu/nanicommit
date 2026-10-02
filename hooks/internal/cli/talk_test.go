package cli_test

// After a commit is sent, the post-commit hook opens its /talk page. Tests
// run with NANICOMMIT_NO_BROWSER set (see testutil.Main), so the hook only
// prints the URL instead of starting a browser.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/testutil"
)

func TestPostCommitShowsTalkURL(t *testing.T) {
	const talkURL = "http://localhost:5173/talk/" + testID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/commits" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"quiz_id":        testID,
			"quiz_url":       "http://localhost:5173/quizzes/" + testID,
			"talk_url":       talkURL,
			"status":         "ready",
			"question_count": 3,
		})
	}))
	t.Cleanup(srv.Close)

	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "init", "--repository-id", testID); res.code != 0 {
		t.Fatalf("init: %+v", res)
	}
	r.Git("config", "--local", "nanicommit.backendUrl", srv.URL)
	r.Git("config", "--local", "nanicommit.userId", testUser)

	r.Write("a.txt", "x\n")
	r.Git("add", "a.txt")
	stderr := commit(t, r, "-m", "one")
	if !strings.Contains(stderr, "nanicommit: look back on it with ぽんた: "+talkURL+"\n") ||
		strings.Contains(stderr, "nanicommit: opening") {
		t.Errorf("post-commit stderr:\n%s", stderr)
	}
}
