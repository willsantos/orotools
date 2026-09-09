package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Digest computes the canonical content digest of a file tree rooted at root
// (skills-manager FR-19). For every regular file, ordered by its
// slash-separated path relative to root, it feeds:
//
//	path NUL executable-bit NUL byte-length NUL bytes
//
// into SHA-256. Directories are excluded; symlinks and special file types are
// an error. Added, removed, modified or re-modified-executable files all
// change the digest (drift detection).
func Digest(fsys fs.FS, root string) (string, error) {
	var paths []string
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("digest: %q não é arquivo regular", p)
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		rel, err := relFromRoot(root, p)
		if err != nil {
			return "", err
		}
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return "", err
		}
		exec := "0"
		if info.Mode()&0o111 != 0 {
			exec = "1"
		}
		f, err := fsys.Open(p)
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write([]byte(exec))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(info.Size(), 10)))
		h.Write([]byte{0})
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// relFromRoot converts a walk path to a slash-separated path relative to root.
// The root itself yields an empty path.
func relFromRoot(root, p string) (string, error) {
	if p == root {
		return "", nil
	}
	if root == "." || root == "" {
		return strings.TrimPrefix(p, "./"), nil
	}
	rel := strings.TrimPrefix(p, root)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("digest: caminho %q fora da raiz %q", p, root)
	}
	return rel, nil
}
