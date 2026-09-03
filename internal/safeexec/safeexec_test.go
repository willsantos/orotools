package safeexec

import "testing"

func TestValidateName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"git", true},
		{"pnpm", true},
		{"node-gyp", true},
		{"a.b-c_d", true},
		{"", false},
		{"foo;bar", false},
		{"foo|bar", false},
		{"foo bar", false},
		{"sh -c", false},
		{"/bin/sh", false},
		{"..\\x", false},
		{"$(x)", false},
		{"`x`", false},
		{"foo\tbar", false},
		{"foo\nbar", false},
		{"a\x00b", false},
	}
	for _, tt := range tests {
		err := ValidateName(tt.name)
		if tt.valid && err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", tt.name, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", tt.name)
		}
	}
}
