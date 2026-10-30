package recipe

import "testing"

func TestValidate_OnboardingStep(t *testing.T) {
	valid := &Recipe{Version: 1, Name: "x", Steps: []Step{
		{ID: "adopt", Type: "onboarding"},
		{ID: "adopt-protected", Type: "onboarding", ProtectBranches: []string{"main", "release/1.0"}},
	}}
	if errs := Validate(valid); len(errs) != 0 {
		t.Errorf("valid onboarding steps rejected: %v", errs)
	}

	cases := []struct {
		name string
		step Step
		want string
	}{
		{"empty-branch", Step{Type: "onboarding", ProtectBranches: []string{"main", ""}}, "protect_branches[1]: branch name is empty"},
		{"duplicate-branch", Step{Type: "onboarding", ProtectBranches: []string{"main", "main"}}, `protect_branches[1]: duplicate branch "main"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Recipe{Version: 1, Name: "x", Steps: []Step{tc.step}}
			errs := Validate(r)
			if len(errs) != 1 || errs[0].Error() != "steps[0]."+tc.want {
				t.Errorf("Validate = %v, want [steps[0].%s]", errs, tc.want)
			}
		})
	}
}
