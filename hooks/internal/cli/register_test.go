package cli_test

// Tests of "init" registering the repository with the backend (R0,
// design.md 4.1) against a fake backend.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kobadaidesu/hook-test/internal/testutil"
)

const (
	testUser  = "a672330f-476e-4bc2-9798-1bda8beaef86"
	otherUser = "56b08cc1-b925-415a-b13e-25772529aceb"
)

type registerRequest struct {
	User string
	Body map[string]any
}

// fakeBackend answers POST /api/v1/repositories like the FastAPI router:
// a new registration gets newID (201); a given repository_id is confirmed
// (200) when it is owned by the user; unknown users get 401.
type fakeBackend struct {
	*httptest.Server
	mu       sync.Mutex
	requests []registerRequest
	owners   map[string]string // repository ID -> user ID
	newID    string
}

func newFakeBackend(t *testing.T, newID string) *fakeBackend {
	t.Helper()
	b := &fakeBackend{owners: map[string]string{}, newID: newID}
	b.Server = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.Close)
	return b
}

func (b *fakeBackend) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repositories" {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	user := r.Header.Get("X-User-Id")
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests = append(b.requests, registerRequest{user, body})
	w.Header().Set("Content-Type", "application/json")
	if user != testUser && user != otherUser {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"Authentication required"}`))
		return
	}
	if id, ok := body["repository_id"].(string); ok {
		if b.owners[id] != user {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"detail":"Repository not found"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"repository_id": id})
		return
	}
	b.owners[b.newID] = user
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"repository_id": b.newID})
}

// take returns the requests received since the last call.
func (b *fakeBackend) take() []registerRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	got := b.requests
	b.requests = nil
	return got
}

func TestInitRegistersRepository(t *testing.T) {
	b := newFakeBackend(t, testID)
	r := testutil.NewRepo(t)
	r.Write("a.txt", "x\n")
	head := r.CommitAll("one")

	// No saved ID: init registers the repository with HEAD as the start.
	res := cc(t, r, "", "init", "--backend-url", b.URL+"/", "--user-id", strings.ToUpper(testUser))
	if res.code != 0 || !strings.Contains(res.stdout, "repository id: "+testID+" (registered in the backend as") {
		t.Fatalf("init: %+v", res)
	}
	reqs := b.take()
	if len(reqs) != 1 || reqs[0].User != testUser || reqs[0].Body["learning_base_sha"] != head ||
		reqs[0].Body["name"] != filepath.Base(r.Dir) || reqs[0].Body["repository_id"] != nil {
		t.Errorf("requests: %+v", reqs)
	}
	if got := strings.TrimSpace(r.Git("config", "--local", "--get", "nanicommit.repositoryId")); got != testID {
		t.Errorf("saved ID = %q", got)
	}

	// init again with the saved settings: nothing is sent, the ID is kept.
	if res := cc(t, r, "", "init"); res.code != 0 || !strings.Contains(res.stdout, "repository id: "+testID+"\n") {
		t.Errorf("second init: %+v", res)
	}
	if reqs := b.take(); len(reqs) != 0 {
		t.Errorf("second init sent %+v", reqs)
	}

	// With the backend flags again, the saved ID is only confirmed.
	r.Write("a.txt", "y\n")
	r.CommitAll("two")
	if res := cc(t, r, "", "init", "--backend-url", b.URL, "--user-id", testUser); res.code != 0 {
		t.Errorf("init with flags again: %+v", res)
	}
	if reqs := b.take(); len(reqs) != 1 || reqs[0].Body["repository_id"] != testID {
		t.Errorf("confirmation requests: %+v", reqs)
	}

	// Another user does not own the saved ID: refused, settings unchanged.
	cfg := filepath.Join(r.Dir, ".git", "config")
	before, _ := os.ReadFile(cfg)
	res = cc(t, r, "", "init", "--backend-url", b.URL, "--user-id", otherUser)
	if res.code != 1 || !strings.Contains(res.stderr, "not registered to this user") ||
		!strings.Contains(res.stderr, "git config --local --unset nanicommit.repositoryId") {
		t.Errorf("init as another user: %+v", res)
	}
	if after, _ := os.ReadFile(cfg); !bytes.Equal(before, after) {
		t.Error("git config changed")
	}
}

func TestInitRegistersEmptyRepository(t *testing.T) {
	b := newFakeBackend(t, testID)
	r := testutil.NewRepo(t)
	if res := cc(t, r, "", "init", "--backend-url", b.URL, "--user-id", testUser); res.code != 0 {
		t.Fatalf("init: %+v", res)
	}
	reqs := b.take()
	if len(reqs) != 1 {
		t.Fatalf("requests: %+v", reqs)
	}
	if v, ok := reqs[0].Body["learning_base_sha"]; !ok || v != nil {
		t.Errorf("learning_base_sha = %v (present %v), want null", v, ok)
	}
}

func TestInitRecoversRegistration(t *testing.T) {
	b := newFakeBackend(t, otherID)
	b.owners[testID] = testUser
	r := testutil.NewRepo(t)

	// A given ID is confirmed, not registered again.
	res := cc(t, r, "", "init", "--backend-url", b.URL, "--user-id", testUser, "--repository-id", testID)
	if res.code != 0 || strings.Contains(res.stdout, "registered in the backend") {
		t.Errorf("init: %+v", res)
	}
	if reqs := b.take(); len(reqs) != 1 || reqs[0].Body["repository_id"] != testID {
		t.Errorf("requests: %+v", reqs)
	}
	if got := strings.TrimSpace(r.Git("config", "--local", "--get", "nanicommit.repositoryId")); got != testID {
		t.Errorf("saved ID = %q", got)
	}
}

func TestInitRegistrationFailuresChangeNothing(t *testing.T) {
	b := newFakeBackend(t, testID)
	unknownUser := "00000000-0000-4000-8000-000000000001"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"unknown user":    {[]string{"--backend-url", b.URL, "--user-id", unknownUser}, "does not know this user ID"},
		"foreign ID":      {[]string{"--backend-url", b.URL, "--user-id", testUser, "--repository-id", otherID}, "not registered to this user"},
		"unreachable URL": {[]string{"--backend-url", "http://127.0.0.1:1", "--user-id", testUser}, "cannot reach the backend"},
	} {
		t.Run(name, func(t *testing.T) {
			r := testutil.NewRepo(t)
			cfg := filepath.Join(r.Dir, ".git", "config")
			before, _ := os.ReadFile(cfg)
			res := cc(t, r, "", append([]string{"init"}, tc.args...)...)
			if res.code != 1 || !strings.Contains(res.stderr, tc.want) {
				t.Errorf("init: %+v", res)
			}
			if after, _ := os.ReadFile(cfg); !bytes.Equal(before, after) {
				t.Error("git config changed")
			}
			if _, err := os.Lstat(filepath.Join(r.Dir, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
				t.Error("hook installed")
			}
		})
	}
}

func TestInitRegisteredButHookConflicts(t *testing.T) {
	b := newFakeBackend(t, testID)
	r := testutil.NewRepo(t)
	os.WriteFile(filepath.Join(r.Dir, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\necho mine\n"), 0o755)
	res := cc(t, r, "", "init", "--backend-url", b.URL, "--user-id", testUser)
	// The repository is registered before the hooks are installed, so the
	// message must hand over the new ID instead of claiming nothing changed.
	if res.code != 1 || !strings.Contains(res.stderr, "registered in the backend") ||
		!strings.Contains(res.stderr, "git config --local nanicommit.repositoryId "+testID) {
		t.Errorf("init: %+v", res)
	}
	if len(b.take()) != 1 {
		t.Error("the repository was not registered")
	}
}
