// Package delivery previews project delivery metadata and official pipeline refs.
package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DistributionPin stays empty until an official immutable tag is independently verified.
const DistributionPin = ""

var errNoEvidence = errors.New("insufficient project evidence to infer stack; specify stack explicitly")

// DetectStack is partial evidence-based detection. Evidence must be a regular,
// non-symlink manifest directly under root. Mixed stacks are not classified.
func DetectStack(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	checks := []struct{ file, stack string }{{"go.mod", "go"}, {"*.sln", "dotnet"}, {"*.csproj", "dotnet"}, {"pyproject.toml", "python"}, {"requirements.txt", "python"}, {"composer.json", "php"}, {"package.json", "node"}, {"pnpm-lock.yaml", "node"}, {"yarn.lock", "node"}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, check := range checks {
		for _, entry := range entries {
			matched := entry.Name() == check.file
			if strings.Contains(check.file, "*") {
				matched, _ = filepath.Match(check.file, entry.Name())
			}
			if matched && regularEvidence(root, entry.Name()) {
				return check.stack, nil
			}
		}
	}
	return "", errNoEvidence
}

// Generate fails closed until an official tag, matching template contract, and
// schema validator have been verified. No aspirational YAML is returned.
func Generate(root, application, provider string) ([]byte, []byte, error) {
	if _, err := DetectStack(root); err != nil {
		return nil, nil, err
	}
	if provider != "azure-devops" {
		return nil, nil, fmt.Errorf("unsupported delivery destination %q", provider)
	}
	return nil, nil, errors.New("delivery generation unavailable: verified official template pin and schema validation are required")
}

// WritePreview writes files under root; it is not a dry-run. It exclusively
// creates targets and removes files it created if a later write fails.
func WritePreview(root string, config, refs []byte) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("project root must be a real directory")
	}
	secureRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer secureRoot.Close()
	dir := filepath.Join(root, ".oroborus")
	if err := ensureRealDir(dir); err != nil {
		return err
	}
	paths := []struct {
		path string
		data []byte
	}{{filepath.Join(".oroborus", "delivery.yaml"), config}, {"azure-pipelines.yml", refs}}
	created := make([]createdFile, 0, len(paths))
	for _, item := range paths {
		if err := ensureSafeParents(root, filepath.Join(root, item.path)); err != nil {
			rollbackCreatedRoot(secureRoot, created)
			return err
		}
		f, err := secureRoot.OpenFile(item.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			rollbackCreatedRoot(secureRoot, created)
			return fmt.Errorf("refusing to overwrite or follow unsafe path %s: %w", item.path, err)
		}
		created = append(created, createdFile{item.path, fileInfo(f)})
		_, err = f.Write(item.data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			rollbackCreatedRoot(secureRoot, created)
			return err
		}
	}
	return nil
}

func regularEvidence(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, name))
	return err == nil && info.Mode().IsRegular()
}

func ensureRealDir(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = os.Mkdir(path, 0755); err == nil {
			return nil
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New(".oroborus must be a real directory, not a symlink")
	}
	return nil
}

func ensureSafeParents(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("output path escapes project root")
	}
	for dir := filepath.Dir(path); dir != root && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe output parent %s", dir)
		}
	}
	return nil
}

type createdFile struct {
	path string
	info os.FileInfo
}

func fileInfo(f *os.File) os.FileInfo { info, _ := f.Stat(); return info }
func rollbackCreatedRoot(root *os.Root, paths []createdFile) {
	for i := len(paths) - 1; i >= 0; i-- {
		current, err := root.Lstat(paths[i].path)
		if err == nil && current.Mode()&os.ModeSymlink == 0 && paths[i].info != nil && os.SameFile(current, paths[i].info) {
			_ = root.Remove(paths[i].path)
		}
	}
}
