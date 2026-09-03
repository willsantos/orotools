package recipe

import (
	"strings"
	"testing"
)

func TestValidate_OK(t *testing.T) {
	r := &Recipe{Version: 1, Name: "ok", Steps: []Step{{Type: "message", Text: "hi"}}}
	if errs := Validate(r); len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestValidate_VersionAndName(t *testing.T) {
	r := &Recipe{Version: 3}
	errs := Validate(r)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
joined := strings.Join(errMsgs(errs), "; ")
	if !strings.Contains(joined, "version") {
		t.Errorf("missing version error in %q", joined)
	}
	if !strings.Contains(joined, "name") {
		t.Errorf("missing name error in %q", joined)
	}
}

func TestValidate_StepRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		step Step
		want string
	}{
		{"exec missing command", Step{Type: "exec"}, "command"},
		{"mkdir missing path", Step{Type: "mkdir"}, "path"},
		{"copy missing source", Step{Type: "copy", Destination: "d"}, "source"},
		{"copy missing destination", Step{Type: "copy", Source: "s"}, "destination"},
		{"template missing source", Step{Type: "template", Destination: "d"}, "source"},
		{"message missing text", Step{Type: "message"}, "text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &Recipe{Version: 1, Name: "x", Steps: []Step{c.step}}
			errs := Validate(r)
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), c.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected error containing %q, got: %v", c.want, errs)
			}
		})
	}
}

func TestValidate_UnknownStepType(t *testing.T) {
	r := &Recipe{Version: 1, Name: "x", Steps: []Step{{Type: "teleport"}}}
	errs := Validate(r)
	if len(errs) == 0 {
		t.Fatalf("expected error for unknown step type")
	}
	joined := strings.Join(errMsgs(errs), "; ")
	if !strings.Contains(joined, "teleport") || !strings.Contains(joined, "mkdir|copy|template|exec|message") {
		t.Errorf("error %q must list valid types", joined)
	}
}

func TestValidate_SelectRequiresOptions(t *testing.T) {
	r := &Recipe{
		Version:   1,
		Name:      "x",
		Variables: map[string]Variable{"db": {Type: "select"}},
	}
	errs := Validate(r)
	if len(errs) == 0 {
		t.Fatalf("expected error for select without options")
	}
}

func TestValidate_ConditionOperatorRequiresVariable(t *testing.T) {
	var v any = "x"
	r := &Recipe{
		Version: 1,
		Name:    "x",
		Steps: []Step{{
			Type: "message",
			Text: "hi",
			When: &Condition{Equals: &v},
		}},
	}
	errs := Validate(r)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "variable") {
		t.Errorf("error should mention variable: %v", errs[0])
	}
}

func TestValidate_ConditionRecursion(t *testing.T) {
	var v any = "x"
	r := &Recipe{
		Version: 1,
		Name:    "x",
		Steps: []Step{{
			Type: "message",
			Text: "hi",
			When: &Condition{
				All: []Condition{{Equals: &v}},
			},
		}},
	}
	errs := Validate(r)
	if len(errs) != 1 {
		t.Fatalf("expected 1 recursive error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "when.all[0]") {
		t.Errorf("error should cite recursive path: %v", errs[0])
	}
}

func TestValidate_AccumulatesMultipleErrors(t *testing.T) {
	r := &Recipe{
		Version:   9,
		Variables: map[string]Variable{"db": {Type: "select"}},
		Steps: []Step{
			{Type: "exec"},
			{Type: "mkdir"},
		},
	}
	errs := Validate(r)
	if len(errs) < 4 {
		t.Fatalf("expected at least 4 accumulated errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_ExternalInstaller(t *testing.T) {
	r := &Recipe{
		Version: 1,
		Name:    "x",
		ExternalInstallers: map[string]ExternalInstaller{
			"empty": {Runtime: ""},
		},
	}
	errs := Validate(r)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "external_installers[empty].runtime") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected runtime error, got: %v", errs)
	}
}

func errMsgs(errs []error) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Error()
	}
	return out
}
