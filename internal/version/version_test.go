package version

import "testing"

func TestFormat(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"dev", "oro dev"},
		{"v0.1.0", "oro v0.1.0"},
		{"", "oro "},
	}
	for _, c := range cases {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
