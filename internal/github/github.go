// Package github provides a minimal client for fetching pull request data
// from the GitHub REST API: the unified diff, the changed file list,
// existing review comments, and prior reviews.
//
// Authentication uses a personal access token read from the GITHUB_TOKEN
// environment variable (or passed explicitly to NewClient). Unauthenticated
// access works for public repos but is rate-limited to 60 requests/hour.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is the GitHub REST API root.
const DefaultBaseURL = "https://api.github.com"

// Client is a minimal GitHub REST API client.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// NewClient builds a client. If token is empty it falls back to the
// GITHUB_TOKEN env var; if still empty the client operates
// unauthenticated (public repos only, 60 req/hr).
func NewClient(token string) *Client {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	return &Client{
		token:   token,
		baseURL: DefaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// PR is the subset of pull request fields graph-review needs.
type PR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Head    Branch `json:"head"`
	Base    Branch `json:"base"`
	HTMLURL string `json:"html_url"`
}

// Branch is a PR head/base ref.
type Branch struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo Repo   `json:"repo"`
}

// Repo is the minimal repository info nested under a Branch.
type Repo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
}

// FileInfo describes a file changed by a PR.
type FileInfo struct {
	Filename     string `json:"filename"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Changes      int    `json:"changes"`
	Patch        string `json:"patch"`
	PreviousFilename string `json:"previous_filename,omitempty"`
}

// Comment is a PR review comment.
type Comment struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	Body      string `json:"body"`
	Author    User   `json:"user"`
	CreatedAt string `json:"created_at"`
}

// Review is a PR review summary.
type Review struct {
	ID        int64  `json:"id"`
	User      User   `json:"user"`
	Body      string `json:"body"`
	State     string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
}

// User is a GitHub user.
type User struct {
	Login     string `json:"login"`
	HTMLURL   string `json:"html_url"`
}

// RepoRef identifies an owner/repo pair, e.g. "geoffjay/graph-review".
type RepoRef struct {
	Owner string
	Repo  string
}

// ParseRepoRef parses "owner/repo" into a RepoRef.
func ParseRepoRef(s string) (RepoRef, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return RepoRef{}, fmt.Errorf("invalid repo ref %q: want owner/repo", s)
	}
	return RepoRef{Owner: parts[0], Repo: parts[1]}, nil
}

// String returns "owner/repo".
func (r RepoRef) String() string { return r.Owner + "/" + r.Repo }

// GetPR fetches PR metadata for owner/repo#number.
func (c *Client) GetPR(ctx context.Context, ref RepoRef, number int) (*PR, error) {
	var pr PR
	path := fmt.Sprintf("/repos/%s/pulls/%d", ref, number)
	if err := c.getJSON(ctx, path, &pr); err != nil {
		return nil, fmt.Errorf("get PR %s#%d: %w", ref, number, err)
	}
	return &pr, nil
}

// GetDiff fetches the unified diff of a PR. Returns the raw diff text.
func (c *Client) GetDiff(ctx context.Context, ref RepoRef, number int) (string, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d", ref, number)
	endpoint := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	// The diff is the legacy text/plain payload; Accept: application/vnd.github.v3.diff
	// is the documented stable media type for it.
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return "", err
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ListFiles returns the changed file list for a PR, with per-file patches.
// Pagination is handled up to 300 files; larger PRs would need page
// iteration, which is not common for review workflows.
func (c *Client) ListFiles(ctx context.Context, ref RepoRef, number int) ([]FileInfo, error) {
	var files []FileInfo
	path := fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=300", ref, number)
	if err := c.getJSON(ctx, path, &files); err != nil {
		return nil, fmt.Errorf("list PR files %s#%d: %w", ref, number, err)
	}
	return files, nil
}

// ListComments returns the review comments (line-anchored) on a PR.
func (c *Client) ListComments(ctx context.Context, ref RepoRef, number int) ([]Comment, error) {
	var comments []Comment
	path := fmt.Sprintf("/repos/%s/pulls/%d/comments?per_page=100", ref, number)
	if err := c.getJSON(ctx, path, &comments); err != nil {
		return nil, fmt.Errorf("list PR comments %s#%d: %w", ref, number, err)
	}
	return comments, nil
}

// ListReviews returns the reviews submitted on a PR.
func (c *Client) ListReviews(ctx context.Context, ref RepoRef, number int) ([]Review, error) {
	var reviews []Review
	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", ref, number)
	if err := c.getJSON(ctx, path, &reviews); err != nil {
		return nil, fmt.Errorf("list PR reviews %s#%d: %w", ref, number, err)
	}
	return reviews, nil
}

// CloneURL returns a clone URL with the token inlined for HTTPS auth when
// a token is set; otherwise the plain clone URL. Intended for use by
// `git clone` in the PR subcommand.
func (c *Client) CloneURL(repo CloneableRepo) string {
	raw := repo.CloneableURL()
	if c.token == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword("x-access-token", c.token)
	return u.String()
}

// CloneableRepo is the minimal shape needed to build a clone URL.
type CloneableRepo interface {
	CloneableURL() string
}

// CloneableRepoFromPR builds a CloneableRepo from a PR's head repo.
func CloneableRepoFromPR(pr *PR) CloneableRepo { return &pr.Head.Repo }

// CloneableURL implements CloneableRepo.
func (r *Repo) CloneableURL() string { return r.CloneURL }

// getJSON issues a GET with the default JSON media type and decodes into v.
func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	endpoint := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func checkStatus(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	var msg string
	if len(bytes.TrimSpace(body)) > 0 {
		msg = strings.TrimSpace(string(body))
	} else {
		msg = resp.Status
	}
	return &APIError{Status: resp.StatusCode, Message: msg}
}

// APIError is returned for non-2xx GitHub responses.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github api %d: %s", e.Status, e.Message)
}

// ErrNoToken indicates no GitHub token was available.
var ErrNoToken = errors.New("no github token: set GITHUB_TOKEN")