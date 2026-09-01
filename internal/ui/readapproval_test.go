package ui

import (
	"strings"
	"testing"
)

func TestReadApproval(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    bool
		wantErr bool
	}{
		{"y", "y\n", true, false},
		{"yes", "Yes\n", true, false},
		{"yes without newline", "y", true, false},
		{"n", "n\n", false, false},
		{"empty line declines", "\n", false, false},
		{"garbage declines", "maybe\n", false, false},
		{"empty stdin errors", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readApproval(strings.NewReader(tt.input), &strings.Builder{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("readApproval() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("readApproval() = %v, want %v", got, tt.want)
			}
		})
	}
}
