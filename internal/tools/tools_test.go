package tools

import (
	"strings"
	"testing"
)

// TestNumberLines guards the read_file numbering: the gutter is the same
// %4d| shape the numbered diff uses, blank lines keep their numbers, and
// the trailing newline is trimmed before numbering.
func TestNumberLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "a\nb\nc", "   1|a\n   2|b\n   3|c"},
		{"blank interior line", "a\n\nb", "   1|a\n   2|\n   3|b"},
		{"trailing newline trimmed", "a\nb\n", "   1|a\n   2|b"},
		{"empty", "", ""},
		{"single line", "x", "   1|x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := numberLines(tc.in); got != tc.want {
				t.Errorf("numberLines(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// The gutter widens instead of truncating at 9999.
	var in strings.Builder
	for i := 1; i <= 10000; i++ {
		if i > 1 {
			in.WriteString("\n")
		}
		in.WriteString("x")
	}
	got := numberLines(in.String())
	first, last, _ := strings.Cut(got, "10000|")
	if !strings.HasSuffix(first, "9999|x\n") || !strings.HasPrefix(last, "x") {
		t.Errorf("numberLines gutter broke at the 4→5 digit boundary:\n%.40s…\n…%.40s", got, last)
	}
}
