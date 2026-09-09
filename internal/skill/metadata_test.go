package skill_test

import (
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"

	"oroborus.dev/orotools/internal/skill"
)

// mustSemver parses a version for fixtures; it fails the test on error.
func mustSemver(t *testing.T, v string) semver.Version {
	t.Helper()
	sv, err := semver.StrictNewVersion(v)
	if err != nil {
		t.Fatalf("mustSemver(%q): %v", v, err)
	}
	return *sv
}

func mustMetadata(t *testing.T, doc string) skill.Metadata {
	t.Helper()
	md, err := skill.ParseMetadata(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	return md
}

func TestParseMetadataValid(t *testing.T) {
	md := mustMetadata(t, "---\nname: code-review\ndescription: Review guidelines\nmetadata:\n  version: \"1.2.3\"\n---\n# Body ignored\n")
	if md.Name != "code-review" {
		t.Errorf("Name = %q", md.Name)
	}
	if md.Description != "Review guidelines" {
		t.Errorf("Description = %q", md.Description)
	}
	if got := md.Version.String(); got != "1.2.3" {
		t.Errorf("Version = %q", got)
	}
}

func TestParseMetadataToleratesExtraFrontmatter(t *testing.T) {
	md := mustMetadata(t, "---\nname: s\ndescription: d\nlicense: MIT\nmetadata:\n  version: 1.0.0\n  author: oro\n---\nbody\n")
	if md.Name != "s" || md.Description != "d" {
		t.Errorf("unexpected metadata: %+v", md)
	}
	if md.Version.String() != "1.0.0" {
		t.Errorf("Version = %q", md.Version.String())
	}
}

func TestParseMetadataPrereleaseAndBuild(t *testing.T) {
	md := mustMetadata(t, "---\nname: s\ndescription: d\nmetadata:\n  version: 1.0.0-rc.1+build.7\n---\n")
	if md.Version.Prerelease() != "rc.1" || md.Version.Metadata() != "build.7" {
		t.Errorf("Version = %q", md.Version.String())
	}
}

func TestParseMetadataErrors(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":       "no fence at all\n",
		"unclosed frontmatter": "---\nname: s\n",
		"invalid yaml":         "---\nname: [unclosed\n---\n",
		"missing name":         "---\ndescription: d\nmetadata:\n  version: 1.0.0\n---\n",
		"missing description":  "---\nname: s\nmetadata:\n  version: 1.0.0\n---\n",
		"missing version":      "---\nname: s\ndescription: d\n---\n",
		"v prefix":             "---\nname: s\ndescription: d\nmetadata:\n  version: v1.0.0\n---\n",
		"partial semver":       "---\nname: s\ndescription: d\nmetadata:\n  version: 1.2\n---\n",
		"not semver":           "---\nname: s\ndescription: d\nmetadata:\n  version: abc\n---\n",
	}
	for label, doc := range cases {
		t.Run(label, func(t *testing.T) {
			if _, err := skill.ParseMetadata(strings.NewReader(doc)); err == nil {
				t.Fatalf("ParseMetadata succeeded, want error for %s", label)
			}
		})
	}
}

func TestParseMetadataVersionComparison(t *testing.T) {
	old := mustMetadata(t, "---\nname: s\ndescription: d\nmetadata:\n  version: 1.0.0\n---\n")
	new := mustMetadata(t, "---\nname: s\ndescription: d\nmetadata:\n  version: 1.1.0\n---\n")
	if c := new.Version.Compare(&old.Version); c <= 0 {
		t.Errorf("Compare 1.1.0 vs 1.0.0 = %d, want > 0", c)
	}
}

// Compile-time guard: Metadata.Version stays a semver.Version so callers can
// compare without re-parsing.
var _ semver.Version = skill.Metadata{}.Version

func TestParseMetadataRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"../evil", "sub/dir", "sub\\dir", ".", ".."} {
		doc := "---\nname: " + name + "\ndescription: d\nmetadata:\n  version: 1.0.0\n---\n"
		if _, err := skill.ParseMetadata(strings.NewReader(doc)); err == nil {
			t.Errorf("ParseMetadata accepted unsafe name %q", name)
		}
	}
}
