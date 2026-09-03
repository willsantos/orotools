package executor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/planner"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

// fakeExecRunner records Run calls and optionally returns a fixed error.
type fakeExecRunner struct {
	calls []fakeCall
	err   error
}

type fakeCall struct {
	dir   string
	name  string
	args  []string
}

func (f *fakeExecRunner) Run(dir, name string, args []string, _, _ io.Writer) error {
	f.calls = append(f.calls, fakeCall{dir, name, append([]string(nil), args...)})
	return f.err
}

// buildPlan assembles a planner.Plan from raw recipe steps. The executor only
// consumes plan.Steps[i].Step, so categories are left zero.
func buildPlan(steps ...recipe.Step) *planner.Plan {
	p := &planner.Plan{}
	for _, s := range steps {
		p.Steps = append(p.Steps, planner.PlannedStep{Step: s})
	}
	return p
}

func baseCtx(t *testing.T, sources fstest.MapFS) (Context, string) {
	t.Helper()
	dir := t.TempDir()
	return Context{
		Project: variables.Project{Name: "Minha API", Slug: "minha-api", Path: dir},
		Vars:    map[string]any{"database": "postgres"},
		Base:    dir,
		Sources: sources,
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
	}, dir
}

func assetFS() fstest.MapFS {
	return fstest.MapFS{
		"templates/docker-compose.yml.tmpl": &fstest.MapFile{
			Data: []byte("name: {{.Project.Name}}\nslug: {{.Project.Slug}}\ndb: {{.Vars.database}}\n"),
		},
		"files/raw.txt": &fstest.MapFile{Data: []byte("raw-content\n")},
	}
}

func TestMkdir_CreateAndSkip(t *testing.T) {
	ctx, dir := baseCtx(t, nil)
	ex := Executor{}
	r := ex.Execute(buildPlan(
		recipe.Step{Type: "mkdir", Path: "apps/api"},
		recipe.Step{Type: "mkdir", Path: "apps/api"}, // already exists → skip
	), ctx)
	if len(r) != 2 {
		t.Fatalf("expected 2 results, got %d", len(r))
	}
	if r[0].Action != ActCreated || r[0].Error != nil {
		t.Errorf("first mkdir should create, got %+v", r[0])
	}
	if r[1].Action != ActSkipped || r[1].Error != nil {
		t.Errorf("second mkdir should skip, got %+v", r[1])
	}
	if _, err := os.Stat(filepath.Join(dir, "apps", "api")); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestMkdir_TraversalFatal(t *testing.T) {
	ctx, _ := baseCtx(t, nil)
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "mkdir", Path: "../escape"},
	), ctx)
	if r[0].Error == nil || r[0].Severity != SevFatal {
		t.Errorf("expected fatal traversal error, got %+v", r[0])
	}
}

func TestCopy_CreateAndSkip(t *testing.T) {
	ctx, dir := baseCtx(t, assetFS())
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "copy", Source: "files/raw.txt", Destination: "out/raw.txt"},
		recipe.Step{Type: "copy", Source: "files/raw.txt", Destination: "out/raw.txt"}, // exists → skip
	), ctx)
	if r[0].Action != ActCreated || r[1].Action != ActSkipped {
		t.Errorf("copy create/skip wrong: %+v %+v", r[0], r[1])
	}
	b, err := os.ReadFile(filepath.Join(dir, "out", "raw.txt"))
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(b) != "raw-content\n" {
		t.Errorf("content = %q", b)
	}
}

func TestCopy_SourceMissingFatal(t *testing.T) {
	ctx, _ := baseCtx(t, assetFS())
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "copy", Source: "files/missing.txt", Destination: "out/x"},
	), ctx)
	if r[0].Error == nil || r[0].Severity != SevFatal {
		t.Errorf("expected fatal missing source, got %+v", r[0])
	}
}

func TestTemplate_CreateSkipOverwrite(t *testing.T) {
	ctx, dir := baseCtx(t, assetFS())

	// create
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "template", Source: "templates/docker-compose.yml.tmpl", Destination: "dc.yml"},
	), ctx)
	if r[0].Action != ActCreated {
		t.Errorf("template create wrong: %+v", r[0])
	}
	b, _ := os.ReadFile(filepath.Join(dir, "dc.yml"))
	if string(b) != "name: Minha API\nslug: minha-api\ndb: postgres\n" {
		t.Errorf("rendered = %q", b)
	}

	// create mode → skip existing
	r = Executor{}.Execute(buildPlan(
		recipe.Step{Type: "template", Source: "templates/docker-compose.yml.tmpl", Destination: "dc.yml"},
	), ctx)
	if r[0].Action != ActSkipped {
		t.Errorf("template create-mode should skip existing: %+v", r[0])
	}

	// overwrite mode → replace
	r = Executor{}.Execute(buildPlan(
		recipe.Step{Type: "template", Source: "templates/docker-compose.yml.tmpl", Destination: "dc.yml", Mode: "overwrite"},
	), ctx)
	if r[0].Action != ActOverwritten {
		t.Errorf("template overwrite-mode should overwrite: %+v", r[0])
	}
}

func TestTemplate_ParseErrorFatal(t *testing.T) {
	src := fstest.MapFS{
		"bad.tmpl": &fstest.MapFile{Data: []byte("{{ .Bad }}")},
	}
	ctx, _ := baseCtx(t, src)
	// .Bad is not a field of the anon struct → executes with <no value> normally;
	// force a real parse error instead:
	src = fstest.MapFS{
		"bad.tmpl": &fstest.MapFile{Data: []byte("{{ .Project.Name }")},
	}
	ctx.Sources = src
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "template", Source: "bad.tmpl", Destination: "x"},
	), ctx)
	if r[0].Error == nil || r[0].Severity != SevFatal {
		t.Errorf("expected parse fatal, got %+v", r[0])
	}
}

func TestExec_RunsAndRecords(t *testing.T) {
	ctx, dir := baseCtx(t, nil)
	fake := &fakeExecRunner{}
	r := Executor{Runner: fake}.Execute(buildPlan(
		recipe.Step{Type: "exec", Command: "dotnet", Args: []string{"new", "webapi"}},
		recipe.Step{Type: "exec", Command: "pnpm", Args: []string{"install"}, WorkingDirectory: "apps/web"},
	), ctx)
	if r[0].Action != ActRan || r[1].Action != ActRan {
		t.Errorf("exec actions wrong: %+v %+v", r[0], r[1])
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 runner calls, got %d", len(fake.calls))
	}
	if fake.calls[0].dir != dir || fake.calls[0].name != "dotnet" {
		t.Errorf("call[0] wrong: %+v", fake.calls[0])
	}
	if fake.calls[1].dir != filepath.Join(dir, "apps", "web") {
		t.Errorf("working_directory not joined: %+v", fake.calls[1])
	}
}

func TestExec_NonZeroFatal(t *testing.T) {
	ctx, _ := baseCtx(t, nil)
	fake := &fakeExecRunner{err: errors.New("exit status 1")}
	r := Executor{Runner: fake}.Execute(buildPlan(
		recipe.Step{Type: "exec", Command: "dotnet", Args: []string{"new"}},
	), ctx)
	if r[0].Error == nil || r[0].Severity != SevFatal {
		t.Errorf("expected fatal exec failure, got %+v", r[0])
	}
}

func TestMessage(t *testing.T) {
	ctx, _ := baseCtx(t, nil)
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "message", Text: "Project created."},
	), ctx)
	if r[0].Action != ActMessaged {
		t.Errorf("message action wrong: %+v", r[0])
	}
	if !bytes.Contains(ctx.Stdout.(*bytes.Buffer).Bytes(), []byte("Project created.")) {
		t.Errorf("stdout missing message")
	}
}

func TestSkipIf_PathExists(t *testing.T) {
	ctx, dir := baseCtx(t, nil)
	// pre-create apps/api so skip_if path_exists matches
	if err := os.MkdirAll(filepath.Join(dir, "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Executor{}.Execute(buildPlan(
		recipe.Step{
			Type: "exec", Command: "dotnet", Args: []string{"new"},
			SkipIf: &recipe.Condition{PathExists: "apps/api"},
		},
	), ctx)
	if r[0].Action != ActSkipped || r[0].Error != nil {
		t.Errorf("expected skip via path_exists, got %+v", r[0])
	}
}

func TestSkipIf_NotPresentStillRuns(t *testing.T) {
	ctx, _ := baseCtx(t, nil)
	fake := &fakeExecRunner{}
	r := Executor{Runner: fake}.Execute(buildPlan(
		recipe.Step{
			Type: "exec", Command: "dotnet", Args: []string{"new"},
			SkipIf: &recipe.Condition{PathExists: "apps/missing"},
		},
	), ctx)
	if r[0].Action != ActRan {
		t.Errorf("expected run when path does not exist, got %+v", r[0])
	}
}

func TestExecute_ContinueOnFatal(t *testing.T) {
	ctx, dir := baseCtx(t, assetFS())
	// step 1 fatal (traversal), step 2 valid — both must produce Results
	_ = dir
	r := Executor{}.Execute(buildPlan(
		recipe.Step{Type: "mkdir", Path: "../escape"},
		recipe.Step{Type: "mkdir", Path: "ok"},
	), ctx)
	if len(r) != 2 {
		t.Fatalf("expected 2 results (continue-on-fatal), got %d", len(r))
	}
	if r[0].Severity != SevFatal {
		t.Errorf("first should be fatal: %+v", r[0])
	}
	if r[1].Action != ActCreated {
		t.Errorf("second should still run: %+v", r[1])
	}
	if !HasFatal(r) {
		t.Errorf("HasFatal should be true")
	}
}

func TestSafeJoin(t *testing.T) {
	cases := []struct {
		base, dest, want string
		wantErr          bool
	}{
		{"/b", "a/c", "/b/a/c", false},
		{"/b", "./a", "/b/a", false},
		{"/b", "../escape", "", true},
		{"/b", "../../etc", "", true},
		{"/b", "", "", true},
	}
	for _, c := range cases {
		got, err := safeJoin(c.base, c.dest)
		if c.wantErr {
			if err == nil {
				t.Errorf("safeJoin(%q,%q) expected error", c.base, c.dest)
			}
			continue
		}
		if err != nil {
			t.Errorf("safeJoin(%q,%q) err: %v", c.base, c.dest, err)
			continue
		}
		// normalize: filepath.Clean for comparison
		if filepath.Clean(got) != filepath.Clean(c.want) {
			t.Errorf("safeJoin(%q,%q) = %q, want %q", c.base, c.dest, got, c.want)
		}
	}
}
