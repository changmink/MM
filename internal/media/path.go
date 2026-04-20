package media

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrPathTraversal = errors.New("invalid path")

// SafePath joins root + rel and ensures the result stays within root.
func SafePath(root, rel string) (string, error) {
	clean := filepath.Join(root, filepath.FromSlash("/"+strings.TrimLeft(rel, "/\\")))
	rootClean := filepath.Clean(root)
	// HasPrefix alone is insufficient: "/data" is a prefix of "/data2"
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return "", ErrPathTraversal
	}
	return clean, nil
}
