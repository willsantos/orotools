package conditions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"oroborus.dev/orotools/internal/recipe"
)

// Context carries everything Evaluate needs: resolved variables and a base for
// path_exists resolution. FS is optional; when nil, the evaluator falls back to
// os.Stat against Base.
type Context struct {
	Vars map[string]any
	Base string
	FS   fs.FS
}

// Evaluate returns whether cond holds against ctx. Top-level operators are
// mutually exclusive; use all/any to combine. Errors signal malformed
// conditions, unknown variables or path traversal attempts.
func Evaluate(cond recipe.Condition, ctx Context) (bool, error) {
	n := countOperands(cond)
	if n == 0 {
		return false, errors.New("malformed condition: no operator set")
	}
	if n > 1 {
		return false, errors.New("ambiguous condition: multiple top-level operators (use all/any to combine)")
	}

	switch {
	case cond.Equals != nil:
		return evalEquals(cond, ctx)
	case cond.NotEquals != nil:
		ok, err := evalEquals(cond, ctx)
		if err != nil {
			return false, err
		}
		return !ok, nil
	case cond.All != nil:
		return evalAll(cond.All, ctx)
	case cond.Any != nil:
		return evalAny(cond.Any, ctx)
	case cond.PathExists != "":
		return evalPathExists(cond.PathExists, ctx)
	}
	return false, errors.New("malformed condition: unreachable")
}

func countOperands(c recipe.Condition) int {
	n := 0
	if c.Equals != nil {
		n++
	}
	if c.NotEquals != nil {
		n++
	}
	if c.All != nil {
		n++
	}
	if c.Any != nil {
		n++
	}
	if c.PathExists != "" {
		n++
	}
	return n
}

func evalEquals(cond recipe.Condition, ctx Context) (bool, error) {
	if cond.Variable == "" {
		return false, errors.New(`condition.variable: required with equals/not_equals`)
	}
	// Dereference the *any operand: Equals/NotEquals store the comparison target
	// behind a pointer to distinguish "operand absent" from "operand is null".
	var target any
	if cond.NotEquals != nil {
		target = *cond.NotEquals
	} else {
		target = *cond.Equals
	}
	got, ok := ctx.Vars[cond.Variable]
	if !ok {
		return false, fmt.Errorf("condition.variable %q: not present in resolved vars", cond.Variable)
	}
	return reflect.DeepEqual(got, target), nil
}

func evalAll(children []recipe.Condition, ctx Context) (bool, error) {
	for i, child := range children {
		ok, err := Evaluate(child, ctx)
		if err != nil {
			return false, fmt.Errorf("all[%d]: %w", i, err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func evalAny(children []recipe.Condition, ctx Context) (bool, error) {
	for i, child := range children {
		ok, err := Evaluate(child, ctx)
		if err != nil {
			return false, fmt.Errorf("any[%d]: %w", i, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func evalPathExists(path string, ctx Context) (bool, error) {
	if path == "" {
		return false, errors.New("condition.path_exists: required and must be non-empty")
	}
	if filepath.IsAbs(path) || containsDotDot(path) {
		return false, fmt.Errorf("condition.path_exists %q: absolute or parent traversal paths are not allowed", path)
	}
	if ctx.FS != nil {
		_, err := fs.Stat(ctx.FS, path)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("path_exists %q: %w", path, err)
	}
	_, err := os.Stat(filepath.Join(ctx.Base, path))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("path_exists %q: %w", path, err)
}

// containsDotDot reports whether path contains a ".." segment. Pattern from
// archive/zip: split on "/" and look for the literal ".." element. Used
// together with filepath.IsAbs to reject traversal attempts in path_exists.
func containsDotDot(path string) bool {
	if !strings.Contains(path, "..") {
		return false
	}
	for _, ent := range strings.Split(path, "/") {
		if ent == ".." {
			return true
		}
	}
	return false
}
