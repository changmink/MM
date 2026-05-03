package media

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrPathTraversal = errors.New("invalid path")

// SafePath는 root + rel을 합친 결과가 root 범위 안에 있음을 보장한다.
func SafePath(root, rel string) (string, error) {
	clean := filepath.Join(root, filepath.FromSlash("/"+strings.TrimLeft(rel, "/\\")))
	rootClean := filepath.Clean(root)
	// HasPrefix만으로는 부족하다: "/data"는 "/data2"의 prefix이기도 하다.
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return "", ErrPathTraversal
	}
	return clean, nil
}
