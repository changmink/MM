package media

import (
	"path/filepath"
	"testing"
)

func TestSafePath(t *testing.T) {
	root := filepath.Join("/data")

	valid := []struct {
		rel  string
		want string
	}{
		{"/movies/film.mp4", filepath.Join(root, "movies", "film.mp4")},
		{"movies/film.mp4", filepath.Join(root, "movies", "film.mp4")},
		{"/", root},
		{"", root},
	}
	for _, c := range valid {
		got, err := SafePath(root, c.rel)
		if err != nil {
			t.Errorf("SafePath(%q, %q) unexpected error: %v", root, c.rel, err)
		}
		if got != c.want {
			t.Errorf("SafePath(%q, %q) = %q, want %q", root, c.rel, got, c.want)
		}
	}

	traversal := []string{
		"../../etc/passwd",
		"../../../etc/shadow",
		"/../../etc/passwd",
		// 형제 디렉터리: /data가 /data2의 prefix라도 거부되어야 한다.
		"/../data2/evil",
	}
	for _, rel := range traversal {
		_, err := SafePath(root, rel)
		if err != ErrPathTraversal {
			t.Errorf("SafePath(%q, %q) = nil, want ErrPathTraversal", root, rel)
		}
	}
}
