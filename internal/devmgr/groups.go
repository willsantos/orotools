package devmgr

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// foldSpecial covers lowercase Latin letters with no canonical NFD
// decomposition — stroke/ligature letters. Every decomposable diacritic
// (á, ğ, č, ş, ą, ...) is handled by the NFD + Mn strip in GroupKey.
var foldSpecial = strings.NewReplacer(
	"ß", "ss", "æ", "ae", "œ", "oe", "ø", "o",
	"đ", "d", "ð", "d", "ħ", "h", "ŧ", "t", "ł", "l", "þ", "th",
)

// GroupKey normalizes a group name for matching and grouping: trim,
// lower-case, fold Latin diacritics — `Clientes`, `clientes` and
// `CLIENTES` collapse into one key (dev-project-groups FR-4). Diacritic
// folding is NFD decomposition + removal of combining marks (review of
// PR #197: comprehensive instead of a hand-picked table), so any Latin
// letter with a canonical decomposition folds, not just pt-BR ones.
func GroupKey(s string) string {
	t := foldSpecial.Replace(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	for _, r := range norm.NFD.String(t) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Groups returns the distinct project groups in first-appearance order,
// deduplicated by GroupKey: case/accent variants are one group and the
// first spelling in config order is the display form. Blank groups are not
// groups; the set is derived from the projects — there is no registry
// (dev-project-groups FR-3).
func (c *Config) Groups() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, k := range c.order {
		p, ok := c.projects[k]
		if !ok || p == nil {
			continue
		}
		g := strings.TrimSpace(p.Group)
		if g == "" {
			continue
		}
		key := GroupKey(g)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, g)
	}
	return out
}
