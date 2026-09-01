package github

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CloneRepo clones the PR's head repo at the head SHA into a temp
// directory and returns the absolute path. The caller is responsible for
// removing the directory when done (use the returned cleanup function).
//
// A shallow clone (--depth 1) is used to keep it fast; the head SHA is
// fetched explicitly so the checkout is pinned to the PR head even if the
// branch tip moved between fetch and clone.
func (c *Client) CloneRepo(ctx context.Context, pr *PR) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "agent-toolbox-clone-*")
	if err != nil {
		return "", nil, fmt.Errorf("create clone dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	headRepo := &pr.Head.Repo

	// Shallow clone the head ref, then pin to the exact SHA. Auth goes
	// through an extra header rather than the clone URL, so the token
	// never lands in the cloned repo's config or git error output.
	cloneArgs := append(c.gitAuthArgs(),
		"clone", "--depth", "1", "--branch", pr.Head.Ref,
		headRepo.CloneableURL(), tmp)
	if err := runGit(ctx, "", cloneArgs...); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone: %w", err)
	}

	// Pin to the exact PR head SHA in case the branch moved.
	if pr.Head.SHA != "" {
		if err := c.pinHead(ctx, tmp, headRepo.CloneableURL(), pr.Head.SHA); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("pin PR head: %w", err)
		}
	}

	abs, err := filepath.Abs(tmp)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolve clone path: %w", err)
	}
	return abs, cleanup, nil
}

func trimErr(out []byte) string {
	s := string(out)
	if len(s) > 512 {
		s = s[len(s)-512:]
	}
	return s
}

// pinHead checks out the PR head SHA in the freshly cloned repo, fetching
// the SHA directly if the shallow clone does not contain it yet. The SHA
// is validated before it is handed to git.
func (c *Client) pinHead(ctx context.Context, dir, cloneURL, sha string) error {
	if !validSHA(sha) {
		return fmt.Errorf("invalid PR head SHA %q", sha)
	}
	if err := runGit(ctx, dir, "checkout", sha); err == nil {
		return nil
	}
	fetchArgs := append(c.gitAuthArgs(), "fetch", "--depth", "1", cloneURL, sha)
	if err := runGit(ctx, dir, fetchArgs...); err != nil {
		return fmt.Errorf("git fetch %s: %w", sha, err)
	}
	if err := runGit(ctx, dir, "checkout", sha); err != nil {
		return fmt.Errorf("git checkout %s: %w", sha, err)
	}
	return nil
}

// runGit runs git with args in dir. The command is cancelled with ctx,
// and failures carry the tail of the command's stderr for context.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- variable parts are the GitHub API clone URL and a validSHA-checked SHA.
	cmd.Dir = dir
	if _, err := cmd.Output(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("%w: %s", err, trimErr(ee.Stderr))
		}
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

// validSHA reports whether s is a full git object name: 40 lowercase hex
// characters (SHA-1) or 64 (SHA-256). The PR head SHA comes from the
// GitHub API and is passed to git subprocesses, so it is validated
// before it reaches a command line.
func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && (len(b) == 20 || len(b) == 32)
}

// gitAuthArgs returns git -c options that authenticate HTTPS operations
// via an Authorization header instead of embedding the token in the
// URL. The empty credential.helper disables any system helper that
// could prompt or intercept.
func (c *Client) gitAuthArgs() []string {
	if c.token == "" {
		return nil
	}
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + c.token))
	return []string{
		"-c", "http.extraHeader=Authorization: Basic " + auth,
		"-c", "credential.helper=",
	}
}
