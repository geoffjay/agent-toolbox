package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if base := filepath.Base(path); !strings.HasSuffix(base, fmt.Sprintf("-%08d.log", os.Getpid())) {
		t.Errorf("log name = %q, want a zero-padded pid so names sort in creation order", base)
	}
}

// TestSanitize guards the control-byte stripper: escape sequences from
// untrusted PR text must never reach a terminal, while ordinary text —
// newlines, tabs, and multibyte runes included — passes through.
func TestSanitize(t *testing.T) {
	got := sanitize("ok\ttext\nwith é ✓\r\x1b]52;c;aGVsbG8=\x07\u0085\a")
	want := "ok\ttext\nwith é ✓\r]52;c;aGVsbG8="
	if got != want {
		t.Errorf("sanitize() = %q, want %q", got, want)
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
