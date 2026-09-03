package agent

import "oroborus.dev/orotools/internal/recipe"

// FlattenExternal turns recipe external agent refs into manifest-style flat names.
func FlattenExternal(external []recipe.ExternalAgent) []string {
	out := make([]string, 0, len(external))
	for _, e := range external {
		if len(e.Agents) == 0 {
			out = append(out, e.Installer)
			continue
		}
		out = append(out, e.Agents...)
	}
	return out
}
