package panes

import "testing"

func TestFuzzyMatch(t *testing.T) {
	for _, tt := range []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"empty pattern", "", "", true},
		{"case insensitive subsequence", "CL", "Check List", true},
		{"ordered subsequence", "lc", "Check List", false},
		{"unicode case and gaps", "É界", "éclair 世界", true},
		{"bytes from different runes", "é", "è©", false},
		{"missing unicode rune", "猫", "世界", false},
		{"empty value", "a", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := FuzzyMatch(tt.pattern, tt.value); got != tt.want {
				t.Fatalf("FuzzyMatch(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
			}
		})
	}
}
