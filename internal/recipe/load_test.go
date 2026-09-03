package recipe

import (
	"os"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestLoad_FromMapFS(t *testing.T) {
	src, err := os.ReadFile("testdata/valid_fastify_next.yaml")
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"recipe.yaml": &fstest.MapFile{Data: src},
	}
	r, err := Load(fsys, "recipe.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.Name != "fastify-next" {
		t.Errorf("Name = %q", r.Name)
	}
}

func TestLoad_FromDirFS_EqualsMapFS(t *testing.T) {
	src, err := os.ReadFile("testdata/valid_fastify_next.yaml")
	if err != nil {
		t.Fatal(err)
	}
	mapFS := fstest.MapFS{
		"valid_fastify_next.yaml": &fstest.MapFile{Data: src},
	}
	fromMap, err := Load(mapFS, "valid_fastify_next.yaml")
	if err != nil {
		t.Fatalf("load map: %v", err)
	}
	fromDir, err := Load(os.DirFS("testdata"), "valid_fastify_next.yaml")
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if !reflect.DeepEqual(fromMap, fromDir) {
		t.Errorf("map and dir loads differ:\nmap=%+v\ndir=%+v", fromMap, fromDir)
	}
}

func TestLoad_MissingPath(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := Load(fsys, "nope.yaml")
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}
