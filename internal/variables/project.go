package variables

import (
	"errors"
	"strings"
	"unicode"
)

// Project holds the variables derived from the project name (spec section 12).
type Project struct {
	Name      string
	Slug      string
	Namespace string
	Path      string
}

// deriveProject builds Project from name. The name is trimmed; words are split
// on whitespace runs (collapsed). Slug is lowercased words joined by "-";
// Namespace is the PascalCase-join of words; Path is "./" + Slug.
// Accent/special chars are preserved (no ASCII fold) per context.md decision.
func deriveProject(name string) (Project, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Project{}, errors.New("Project.Name: required and must be non-empty")
	}
	words := strings.Fields(trimmed)

	slug := strings.ToLower(strings.Join(words, "-"))
	ns := buildNamespace(words)

	return Project{
		Name:      trimmed,
		Slug:      slug,
		Namespace: ns,
		Path:      "./" + slug,
	}, nil
}

// buildNamespace returns the PascalCase-join of words: each word is lowercased
// then has its first rune title-cased, then all words are concatenated without
// separator. This matches the spec section 12 example ("Minha API" -> "MinhaApi").
func buildNamespace(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if w == "" {
			continue
		}
		lowered := strings.ToLower(w)
		runes := []rune(lowered)
		runes[0] = unicode.ToTitle(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}
