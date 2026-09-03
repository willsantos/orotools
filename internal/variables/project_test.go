package variables

import (
	"strings"
	"testing"
)

func TestDeriveProject(t *testing.T) {
	cases := []struct {
		name string
		want Project
	}{
		{"Minha API", Project{Name: "Minha API", Slug: "minha-api", Namespace: "MinhaApi", Path: "./minha-api"}},
		{"API v2", Project{Name: "API v2", Slug: "api-v2", Namespace: "ApiV2", Path: "./api-v2"}},
		{"Foo   Bar", Project{Name: "Foo   Bar", Slug: "foo-bar", Namespace: "FooBar", Path: "./foo-bar"}},
		{"  espaços  ", Project{Name: "espaços", Slug: "espaços", Namespace: "Espaços", Path: "./espaços"}},
		{"únicapalavra", Project{Name: "únicapalavra", Slug: "únicapalavra", Namespace: "Únicapalavra", Path: "./únicapalavra"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := deriveProject(c.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("deriveProject(%q) = %+v, want %+v", c.name, got, c.want)
			}
		})
	}
}

func TestDeriveProject_Empty(t *testing.T) {
	for _, name := range []string{"", "   ", "\t\n"} {
		_, err := deriveProject(name)
		if err == nil {
			t.Errorf("deriveProject(%q) expected error, got nil", name)
		}
		if err != nil && !strings.Contains(err.Error(), "Project.Name") {
			t.Errorf("error should cite Project.Name: %v", err)
		}
	}
}
