package requirements

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// constraintRe captures an optional operator and a dotted-numeric version.
	// Examples: ">=10", ">1.2", "==9.1.0", "10" (bare → ">="), "v24.0.0".
	constraintRe = regexp.MustCompile(`^\s*(>=|>|<=|<|==|!=)?\s*v?(\d+(?:\.\d+)*)\s*$`)
	// versionRe parses a dotted-numeric version with an optional leading "v".
	versionRe = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)$`)
)

// parseConstraint splits a constraint string into operator and rhs version.
// A bare version (no operator) is treated as ">=".
func parseConstraint(s string) (op, rhs string, err error) {
	m := constraintRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", fmt.Errorf("invalid constraint %q", s)
	}
	op = m[1]
	if op == "" {
		op = ">="
	}
	return op, m[2], nil
}

// parseVersion turns "v24.0.0" / "10.0.100" into a []int slice.
func parseVersion(s string) ([]int, error) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil, fmt.Errorf("invalid version %q", s)
	}
	parts := strings.Split(m[1], ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid version %q: segment %q", s, p)
		}
		out[i] = n
	}
	return out, nil
}

// pad equalises the length of a and b by appending zeros to the shorter one.
// Returns the (possibly grown) slices since append may reallocate.
func pad(a, b []int) ([]int, []int) {
	for len(a) < len(b) {
		a = append(a, 0)
	}
	for len(b) < len(a) {
		b = append(b, 0)
	}
	return a, b
}

// compareInts returns -1, 0 or 1 by lexicographic comparison of equal-length slices.
func compareInts(a, b []int) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// satisfies reports whether found meets constraint. Bare constraint (no op)
// is treated as ">=".
func satisfies(found, constraint string) (bool, error) {
	op, rhs, err := parseConstraint(constraint)
	if err != nil {
		return false, err
	}
	f, err := parseVersion(found)
	if err != nil {
		return false, err
	}
	r, err := parseVersion(rhs)
	if err != nil {
		return false, err
	}
	fc := append([]int(nil), f...)
	rc := append([]int(nil), r...)
	fc, rc = pad(fc, rc)
	cmp := compareInts(fc, rc)
	switch op {
	case ">=":
		return cmp >= 0, nil
	case ">":
		return cmp > 0, nil
	case "<=":
		return cmp <= 0, nil
	case "<":
		return cmp < 0, nil
	case "==":
		return cmp == 0, nil
	case "!=":
		return cmp != 0, nil
	default:
		return false, fmt.Errorf("unknown operator %q", op)
	}
}
