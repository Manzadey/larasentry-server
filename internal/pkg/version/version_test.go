package version

import (
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"v2.0.0", "1.9.9", 1},
		{"1.2.0-rc1", "1.2.0", -1},
		{"v1.10.0", "v1.9.0", 1},
	}

	for _, tt := range tests {
		got := Compare(tt.v1, tt.v2, nil)
		if (tt.expected > 0 && got <= 0) || (tt.expected < 0 && got >= 0) || (tt.expected == 0 && got != 0) {
			t.Errorf("Compare(%q, %q) = %d; want sign %d", tt.v1, tt.v2, got, tt.expected)
		}
	}
}
