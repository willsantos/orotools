package requirements

import "testing"

func TestProbes(t *testing.T) {
	cases := []struct {
		name  string
		parse func([]byte) (string, error)
		in    string
		want  string
	}{
		{"plain", parsePlain, "9.0.0\n", "9.0.0"},
		{"plain spaces", parsePlain, "  10.0.100  \n", "10.0.100"},
		{"leadingV node", parseLeadingV, "v24.0.0\n", "24.0.0"},
		{"leadingV bare", parseLeadingV, "1.2.3\n", "1.2.3"},
		{"git", parseGitPrefix, "git version 2.34.1\n", "2.34.1"},
		{"go", parseGoPrefix, "go version go1.23.0 linux/amd64\n", "1.23.0"},
		{"ruby", parseRubyPrefix, "ruby 3.3.0 (2023-12-25 revision 5124f9ac75) [x86_64-linux]\n", "3.3.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.parse([]byte(c.in))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestProbes_Unparseable(t *testing.T) {
	cases := []struct {
		name  string
		parse func([]byte) (string, error)
		in    string
	}{
		{"git garbage", parseGitPrefix, "not git output\n"},
		{"go garbage", parseGoPrefix, "rustc 1.70\n"},
		{"ruby garbage", parseRubyPrefix, "python 3.11\n"},
		{"plain empty", parsePlain, "   \n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.parse([]byte(c.in)); err == nil {
				t.Errorf("expected error for %q", c.in)
			}
		})
	}
}

func TestProbes_RegistryCoversOfficialTools(t *testing.T) {
	required := []string{"dotnet", "node", "pnpm", "ruby"}
	for _, cmd := range required {
		if _, ok := probes[cmd]; !ok {
			t.Errorf("probe missing for %q (required by official recipes)", cmd)
		}
	}
}
