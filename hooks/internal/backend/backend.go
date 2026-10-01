// Package backend talks to the learning backend (FastAPI) over HTTP.
// It registers repositories (R0), sends commit payloads (D1) and asks
// whether a push may proceed (D5).
//
// The backend location and the user are read from Git configuration:
//
//	commitcoach.backendUrl  e.g. http://localhost:8100
//	commitcoach.userId      UUID shown by the web login
//
// Both can live in any scope; the effective (last) value wins, like Git
// itself. "commitcoach init --backend-url ... --user-id ..." saves them in
// the local scope.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
	"github.com/kobadaidesu/hook-test/internal/repoid"
)

const (
	// ConfigKeyURL is the Git configuration key of the backend base URL.
	ConfigKeyURL = "commitcoach.backendUrl"
	// ConfigKeyUser is the Git configuration key of the user ID.
	ConfigKeyUser = "commitcoach.userId"

	userIDHeader = "X-User-Id"
	// maxBodyBytes bounds how much of a response is read; real responses
	// are tiny, so anything bigger is broken.
	maxBodyBytes = 1 << 20
)

// ErrNotConfigured means the backend URL or the user ID is missing.
var ErrNotConfigured = errors.New("the backend is not configured: run " +
	`"commitcoach init --backend-url <URL> --user-id <UUID>" (or set ` +
	ConfigKeyURL + " and " + ConfigKeyUser + " with git config)")

// Config is where and as whom to talk to the backend.
type Config struct {
	BaseURL string
	UserID  string
}

// LoadConfig reads the backend settings from Git configuration.
// It returns ErrNotConfigured when either value is missing.
func LoadConfig(ctx context.Context, repo *gitrepo.Repo) (Config, error) {
	url, err := effectiveValue(ctx, repo, ConfigKeyURL)
	if err != nil {
		return Config{}, err
	}
	user, err := effectiveValue(ctx, repo, ConfigKeyUser)
	if err != nil {
		return Config{}, err
	}
	if url == "" || user == "" {
		return Config{}, ErrNotConfigured
	}
	if user, err = repoid.Normalize(user); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigKeyUser, err)
	}
	return Config{BaseURL: strings.TrimRight(url, "/"), UserID: user}, nil
}

func effectiveValue(ctx context.Context, repo *gitrepo.Repo, key string) (string, error) {
	values, err := repo.ConfigValues(ctx, key)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", key, err)
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[len(values)-1].Value, nil // last value wins, like git config --get
}

// Client sends requests. The zero value is not usable; use New.
type Client struct {
	cfg  Config
	http *http.Client
}

// New returns a client for cfg. Timeouts come from the request context.
func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{}}
}

// CommitResult is the backend's answer to a sent commit.
type CommitResult struct {
	QuizID        string `json:"quiz_id"`
	QuizURL       string `json:"quiz_url"`
	Status        string `json:"status"` // "ready" or "passed"
	QuestionCount int    `json:"question_count"`
	// AlreadyRegistered means the commit was sent before and the existing
	// quiz was returned (HTTP 200 instead of 201).
	AlreadyRegistered bool `json:"-"`
}

// SendCommit posts one commit payload (the six-field JSON produced by the
// event package) and returns the quiz that was generated or found.
func (c *Client) SendCommit(ctx context.Context, payload []byte) (*CommitResult, error) {
	status, body, err := c.post(ctx, "/api/v1/commits", payload)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, apiError("sending the commit", status, body)
	}
	var out CommitResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("the backend answered with unexpected JSON: %w", err)
	}
	out.AlreadyRegistered = status == http.StatusOK
	return &out, nil
}

// ErrUnknownUser means the backend does not know the user ID (HTTP 401).
var ErrUnknownUser = errors.New("the backend does not know this user ID; copy it again from the web /connect page")

// ErrUnknownRepository means the repository ID is not registered to the
// user (HTTP 404).
var ErrUnknownRepository = errors.New("the repository ID is not registered to this user")

// RegisterRepository registers a repository (R0) and returns its ID.
// learningBaseSHA is HEAD at init, or "" for a repository without commits.
// With existingID nothing is registered: the backend only confirms that the
// repository belongs to the user, and name and learningBaseSHA are ignored.
func (c *Client) RegisterRepository(ctx context.Context, name, learningBaseSHA, existingID string) (string, error) {
	body := map[string]any{"name": name, "learning_base_sha": nil}
	if learningBaseSHA != "" {
		body["learning_base_sha"] = learningBaseSHA
	}
	if existingID != "" {
		body["repository_id"] = existingID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	status, resp, err := c.post(ctx, "/api/v1/repositories", payload)
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusCreated, http.StatusOK:
	case http.StatusUnauthorized:
		return "", fmt.Errorf("registering the repository as user %s failed: %w", c.cfg.UserID, ErrUnknownUser)
	case http.StatusNotFound:
		return "", fmt.Errorf("confirming repository %s failed: %w", existingID, ErrUnknownRepository)
	default:
		return "", apiError("registering the repository", status, resp)
	}
	var out struct {
		RepositoryID string `json:"repository_id"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return "", fmt.Errorf("the backend answered with unexpected JSON: %w", err)
	}
	id, err := repoid.Normalize(out.RepositoryID)
	if err != nil {
		return "", fmt.Errorf("the backend answered with an invalid repository ID: %w", err)
	}
	return id, nil
}

// PendingCommit is one commit that blocks a push.
type PendingCommit struct {
	CommitSHA string  `json:"commit_sha"`
	Reason    string  `json:"reason"` // "missing" or "not_passed"
	QuizURL   *string `json:"quiz_url"`
}

// PushCheck is the backend's answer to a push check.
type PushCheck struct {
	Allowed        bool            `json:"allowed"`
	PendingCommits []PendingCommit `json:"pending_commits"`
}

// CheckPush asks whether the given commits may be pushed.
func (c *Client) CheckPush(ctx context.Context, repositoryID string, shas []string) (*PushCheck, error) {
	payload, err := json.Marshal(map[string]any{
		"repository_id": repositoryID,
		"commit_shas":   shas,
	})
	if err != nil {
		return nil, err
	}
	status, body, err := c.post(ctx, "/api/v1/push/check", payload)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, apiError("checking the push", status, body)
	}
	var out PushCheck
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("the backend answered with unexpected JSON: %w", err)
	}
	return &out, nil
}

func (c *Client) post(ctx context.Context, path string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(userIDHeader, c.cfg.UserID)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot reach the backend at %s: %w", c.cfg.BaseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("reading the backend's answer: %w", err)
	}
	return resp.StatusCode, body, nil
}

// apiError turns a non-2xx answer into a readable error, using the
// backend's {"detail": ...} when present.
func apiError(doing string, status int, body []byte) error {
	var e struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(body, &e) == nil && e.Detail != nil {
		if s, ok := e.Detail.(string); ok {
			return fmt.Errorf("%s failed: %s (HTTP %d)", doing, s, status)
		}
		if b, err := json.Marshal(e.Detail); err == nil {
			return fmt.Errorf("%s failed: %s (HTTP %d)", doing, b, status)
		}
	}
	return fmt.Errorf("%s failed: HTTP %d", doing, status)
}
