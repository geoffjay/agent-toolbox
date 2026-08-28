package github

import (
	"context"
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
	tmp, err := os.MkdirTemp("", "graph-review-clone-*")
	if err != nil {
		return "", nil, fmt.Errorf("create clone dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	headRepo := &pr.Head.Repo
	cloneURL := c.CloneURL(headRepo)

	// Shallow clone the head ref, then pin to the exact SHA.
	cmd := exec.CommandContext(ctx, "git",
		"clone", "--depth", "1", "--branch", pr.Head.Ref, cloneURL, tmp)
	if out, err := cmd.Output(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git clone: %w: %s", err, trimErr(out))
	}

	// Pin to the exact PR head SHA in case the branch moved.
	if pr.Head.SHA != "" {
		checkout := exec.CommandContext(ctx, "git", "checkout", pr.Head.SHA)
		checkout.Dir = tmp
		if _, err := checkout.Output(); err != nil {
			// Fall back to fetching the SHA directly; shallow clones may
			// not have it yet.
			fetch := exec.CommandContext(ctx, "git", "fetch", "--depth", "1", cloneURL, pr.Head.SHA)
			fetch.Dir = tmp
			if out2, ferr := fetch.Output(); ferr != nil {
				cleanup()
				return "", nil, fmt.Errorf("git fetch %s: %w: %s", pr.Head.SHA, ferr, trimErr(out2))
			}
			checkout2 := exec.CommandContext(ctx, "git", "checkout", pr.Head.SHA)
			checkout2.Dir = tmp
			if out3, err := checkout2.Output(); err != nil {
				cleanup()
				return "", nil, fmt.Errorf("git checkout %s: %w: %s", pr.Head.SHA, err, trimErr(out3))
			}
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
