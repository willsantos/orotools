package executor

import (
	"fmt"
	"path/filepath"
	"strings"
)

// safeJoin joins base and dest and ensures the result stays inside base,
// rejecting path traversal attempts (spec section 70 item 10).
func safeJoin(base, dest string) (string, error) {
	if dest == "" {
		return "", fmt.Errorf("destination is empty")
	}
	joined := filepath.Join(base, dest)
	rel, err := filepath.Rel(base, joined)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be made relative to base %q: %w", dest, base, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base %q", dest, base)
	}
	return joined, nil
}
