package skill

import "oroborus.dev/orotools/internal/recipe"

// FlattenExternal turns recipe external skill refs into manifest-style flat names.
func FlattenExternal(external []recipe.ExternalSkill) []string {
	out := make([]string, 0, len(external))
	for _, e := range external {
		if len(e.Skills) == 0 {
			out = append(out, e.Installer)
			continue
		}
		out = append(out, e.Skills...)
	}
	return out
}
