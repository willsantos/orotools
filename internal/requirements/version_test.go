package requirements

import "testing"

func TestParseConstraint(t *testing.T) {
	cases := []struct {
		in    string
		op    string
		rhs   string
		wantErr bool
	}{
		{">=10", ">=", "10", false},
		{">1.2", ">", "1.2", false},
		{"==9.1.0", "==", "9.1.0", false},
		{"!=3", "!=", "3", false},
		{"<=2.0", "<=", "2.0", false},
		{"<5", "<", "5", false},
		{"10", ">=", "10", false},         // bare → ">="
		{"v24.0.0", ">=", "24.0.0", false}, // bare with v prefix
		{"  >=12  ", ">=", "12", false},   // spaces
		{"~=10", "", "", true},            // unsupported op
		{"abc", "", "", true},             // garbage
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			op, rhs, err := parseConstraint(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if op != c.op || rhs != c.rhs {
				t.Errorf("parseConstraint(%q) = (%q,%q), want (%q,%q)", c.in, op, rhs, c.op, c.rhs)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in  string
		out []int
		err bool
	}{
		{"10", []int{10}, false},
		{"v24.0.0", []int{24, 0, 0}, false},
		{"10.0.100", []int{10, 0, 100}, false},
		{"1.2.beta", nil, true},
		{"", nil, true},
	}
	for _, c := range cases {
		got, err := parseVersion(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseVersion(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVersion(%q) unexpected error: %v", c.in, err)
			continue
		}
		if len(got) != len(c.out) {
			t.Errorf("parseVersion(%q) = %v, want %v", c.in, got, c.out)
			continue
		}
		for i := range got {
			if got[i] != c.out[i] {
				t.Errorf("parseVersion(%q) = %v, want %v", c.in, got, c.out)
			}
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		found, constraint string
		want              bool
	}{
		{"24.0.0", ">=24", true},
		{"24.1.0", ">=24", true},
		{"23.9.9", ">=24", false},
		{"10.0.100", ">=10", true},
		{"9.0.0", ">=10", false},
		{"10.0.100", "==10", false},   // 10.0.100 != 10.0.0
		{"10.0.0", "==10", true},
		{"1.2.3", ">1.2.2", true},
		{"1.2.2", ">1.2.2", false},
		{"1.2.0", "<=1.2", true},
		{"1.3.0", "<=1.2", false},
		{"3.0.0", "!=3", false},
		{"3.1.0", "!=3", true},
		{"10", ">=10", true}, // bare major
		{"v9.0.0", ">=10", false},
	}
	for _, c := range cases {
		t.Run(c.found+"_"+c.constraint, func(t *testing.T) {
			got, err := satisfies(c.found, c.constraint)
			if err != nil {
				t.Fatalf("satisfies(%q,%q) err: %v", c.found, c.constraint, err)
			}
			if got != c.want {
				t.Errorf("satisfies(%q,%q) = %v, want %v", c.found, c.constraint, got, c.want)
			}
		})
	}
}

func TestSatisfies_InvalidConstraint(t *testing.T) {
	if _, err := satisfies("1.0.0", "~=1"); err == nil {
		t.Fatal("expected error for invalid constraint")
	}
	if _, err := satisfies("not-a-version", ">=1"); err == nil {
		t.Fatal("expected error for invalid found version")
	}
}
