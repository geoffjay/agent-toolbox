package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenRunLog guards the TUI log sink: a fresh 0600 file per run inside
// the per-user log directory, never a shared cwd-relative sink.
func TestOpenRunLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	f, path, err := openRunLog()
	if err != nil {
		t.Fatalf("openRunLog() error = %v", err)
	}
	defer f.Close()

	wantDir, err := userLogDir()
	if err != nil {
		t.Fatalf("userLogDir() error = %v", err)
	}
	if dir := filepath.Dir(path); dir != wantDir {
		t.Errorf("log path dir = %q, want %q", dir, wantDir)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("log file perms = %o, want 600", got)
	}
}

// TestPruneRunLogs guards retention: only run logs count, the oldest are
// removed first, and non-log files are untouched.
func TestPruneRunLogs(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"run-20260101T000000-1.log",
		"run-20260102T000000-2.log",
		"notes.txt",
		"run-20260103T000000-3.log",
		"run-20260104T000000-4.log",
		"run-20260105T000000-5.log",
		"run-20260106T000000-6.log",
		"run-20260107T000000-7.log",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "run-20250101T000000-8.log"), 0o700); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}

	pruneRunLogs(dir)

	// The two oldest run logs are gone; everything else survives.
	for _, gone := range names[:2] {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present after pruning", gone)
		}
	}
	for _, keep := range append(names[2:], "run-20250101T000000-8.log") {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s removed by pruning: %v", keep, err)
		}
	}
}
