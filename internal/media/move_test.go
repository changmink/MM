package media

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMoveFile_SimpleMove(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "file.txt")
	dest := filepath.Join(root, "dest")
	writeFile(t, src, []byte("hello"))
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := MoveFile(src, dest)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	want := filepath.Join(dest, "file.txt")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src still exists after move")
	}
	data, err := os.ReadFile(want)
	if err != nil || string(data) != "hello" {
		t.Errorf("dest content = %q (err=%v), want %q", data, err, "hello")
	}
}

func TestMoveFile_ConflictAddsSuffix(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "file.txt")
	dest := filepath.Join(root, "dest")
	writeFile(t, src, []byte("new"))
	writeFile(t, filepath.Join(dest, "file.txt"), []byte("old"))

	got, err := MoveFile(src, dest)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	want := filepath.Join(dest, "file_1.txt")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// existing file untouched
	data, _ := os.ReadFile(filepath.Join(dest, "file.txt"))
	if string(data) != "old" {
		t.Errorf("existing file overwritten: %q", data)
	}
}

func TestMoveFile_MultipleConflicts(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "file.txt")
	dest := filepath.Join(root, "dest")
	writeFile(t, src, []byte("x"))
	writeFile(t, filepath.Join(dest, "file.txt"), []byte("a"))
	writeFile(t, filepath.Join(dest, "file_1.txt"), []byte("b"))
	writeFile(t, filepath.Join(dest, "file_2.txt"), []byte("c"))

	got, err := MoveFile(src, dest)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if filepath.Base(got) != "file_3.txt" {
		t.Errorf("got %q, want file_3.txt", filepath.Base(got))
	}
}

func TestMoveFile_NoExtension(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "README")
	dest := filepath.Join(root, "dest")
	writeFile(t, src, []byte("doc"))
	writeFile(t, filepath.Join(dest, "README"), []byte("existing"))

	got, err := MoveFile(src, dest)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if filepath.Base(got) != "README_1" {
		t.Errorf("got %q, want README_1", filepath.Base(got))
	}
}

func TestMoveFile_SidecarsMoveAlongside(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	destDir := filepath.Join(root, "dest")
	src := filepath.Join(srcDir, "clip.mp4")
	writeFile(t, src, []byte("video"))
	writeFile(t, filepath.Join(srcDir, ".thumb", "clip.mp4.jpg"), []byte("thumb"))
	writeFile(t, filepath.Join(srcDir, ".thumb", "clip.mp4.jpg.dur"), []byte("12.5"))
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := MoveFile(src, destDir)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got != filepath.Join(destDir, "clip.mp4") {
		t.Errorf("dest path = %q", got)
	}
	if _, err := os.Stat(filepath.Join(destDir, ".thumb", "clip.mp4.jpg")); err != nil {
		t.Errorf("thumb sidecar missing at dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, ".thumb", "clip.mp4.jpg.dur")); err != nil {
		t.Errorf("dur sidecar missing at dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, ".thumb", "clip.mp4.jpg")); !os.IsNotExist(err) {
		t.Error("src thumb still present after move")
	}
	if _, err := os.Stat(filepath.Join(srcDir, ".thumb", "clip.mp4.jpg.dur")); !os.IsNotExist(err) {
		t.Error("src dur still present after move")
	}
}

func TestMoveFile_SidecarsRenamedOnConflict(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	destDir := filepath.Join(root, "dest")
	src := filepath.Join(srcDir, "clip.mp4")
	writeFile(t, src, []byte("video"))
	writeFile(t, filepath.Join(srcDir, ".thumb", "clip.mp4.jpg"), []byte("thumb"))
	writeFile(t, filepath.Join(srcDir, ".thumb", "clip.mp4.jpg.dur"), []byte("99.0"))
	// existing dest file forces _1 suffix
	writeFile(t, filepath.Join(destDir, "clip.mp4"), []byte("existing"))

	got, err := MoveFile(src, destDir)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if filepath.Base(got) != "clip_1.mp4" {
		t.Fatalf("got %q, want clip_1.mp4", filepath.Base(got))
	}
	if _, err := os.Stat(filepath.Join(destDir, ".thumb", "clip_1.mp4.jpg")); err != nil {
		t.Errorf("renamed thumb sidecar missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, ".thumb", "clip_1.mp4.jpg.dur")); err != nil {
		t.Errorf("renamed dur sidecar missing: %v", err)
	}
}

func TestMoveFile_MissingSidecarOK(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	destDir := filepath.Join(root, "dest")
	src := filepath.Join(srcDir, "doc.txt")
	writeFile(t, src, []byte("x"))
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := MoveFile(src, destDir); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
}

func TestMoveDir_Success(t *testing.T) {
	root := t.TempDir()
	srcParent := filepath.Join(root, "a")
	srcDir := filepath.Join(srcParent, "sub")
	destDir := filepath.Join(root, "b")
	writeFile(t, filepath.Join(srcDir, "foo.txt"), []byte("hello"))
	writeFile(t, filepath.Join(srcDir, ".thumb", "foo.txt.jpg"), []byte("thumb"))
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := MoveDir(srcDir, destDir)
	if err != nil {
		t.Fatalf("MoveDir: %v", err)
	}
	want := filepath.Join(destDir, "sub")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Error("src directory still exists after move")
	}
	if data, err := os.ReadFile(filepath.Join(want, "foo.txt")); err != nil || string(data) != "hello" {
		t.Errorf("dest file content = %q (err=%v)", data, err)
	}
	if _, err := os.Stat(filepath.Join(want, ".thumb", "foo.txt.jpg")); err != nil {
		t.Errorf(".thumb sidecar missing at dest: %v", err)
	}
}

func TestMoveDir_DestExists(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	destDir := filepath.Join(root, "dest")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destDir, "src"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := MoveDir(srcDir, destDir)
	if !errors.Is(err, ErrDestExists) {
		t.Errorf("got %v, want ErrDestExists", err)
	}
	// src untouched
	if _, err := os.Stat(srcDir); err != nil {
		t.Errorf("src removed despite rejected move: %v", err)
	}
}

func TestMoveDir_Circular_Self(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "a")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := MoveDir(srcDir, srcDir)
	if !errors.Is(err, ErrCircular) {
		t.Errorf("got %v, want ErrCircular", err)
	}
}

func TestMoveDir_Circular_Descendant(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "a")
	descendant := filepath.Join(srcDir, "b", "c")
	if err := os.MkdirAll(descendant, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := MoveDir(srcDir, descendant)
	if !errors.Is(err, ErrCircular) {
		t.Errorf("got %v, want ErrCircular", err)
	}
}

func TestMoveDir_PrefixFalsePositive(t *testing.T) {
	// /tmp/a → /tmp/ab must succeed: ab is NOT a descendant of a.
	root := t.TempDir()
	srcDir := filepath.Join(root, "a")
	destDir := filepath.Join(root, "ab")
	writeFile(t, filepath.Join(srcDir, "x.txt"), []byte("x"))
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := MoveDir(srcDir, destDir)
	if err != nil {
		t.Fatalf("MoveDir: %v", err)
	}
	want := filepath.Join(destDir, "a")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMoveDir_ErrorCases(t *testing.T) {
	t.Run("src missing", func(t *testing.T) {
		root := t.TempDir()
		destDir := filepath.Join(root, "dest")
		if err := os.MkdirAll(destDir, 0755); err != nil {
			t.Fatal(err)
		}
		_, err := MoveDir(filepath.Join(root, "ghost"), destDir)
		if !errors.Is(err, ErrSrcNotFound) {
			t.Errorf("got %v, want ErrSrcNotFound", err)
		}
	})

	t.Run("src is a file", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "file.txt")
		destDir := filepath.Join(root, "dest")
		writeFile(t, src, []byte("a"))
		if err := os.MkdirAll(destDir, 0755); err != nil {
			t.Fatal(err)
		}
		_, err := MoveDir(src, destDir)
		if !errors.Is(err, ErrSrcNotDir) {
			t.Errorf("got %v, want ErrSrcNotDir", err)
		}
	})

	t.Run("dest missing", func(t *testing.T) {
		root := t.TempDir()
		srcDir := filepath.Join(root, "src")
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		_, err := MoveDir(srcDir, filepath.Join(root, "ghost"))
		if !errors.Is(err, ErrDestNotFound) {
			t.Errorf("got %v, want ErrDestNotFound", err)
		}
	})

	t.Run("dest is a file", func(t *testing.T) {
		root := t.TempDir()
		srcDir := filepath.Join(root, "src")
		destFile := filepath.Join(root, "regular.txt")
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, destFile, []byte("not a dir"))
		_, err := MoveDir(srcDir, destFile)
		if !errors.Is(err, ErrDestNotDir) {
			t.Errorf("got %v, want ErrDestNotDir", err)
		}
	})
}

func TestMoveFile_ErrorCases(t *testing.T) {
	t.Run("src missing", func(t *testing.T) {
		dest := t.TempDir()
		_, err := MoveFile(filepath.Join(dest, "ghost"), dest)
		if !errors.Is(err, ErrSrcNotFound) {
			t.Errorf("got %v, want ErrSrcNotFound", err)
		}
	})

	t.Run("src is directory", func(t *testing.T) {
		root := t.TempDir()
		srcDir := filepath.Join(root, "src")
		destDir := filepath.Join(root, "dest")
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(destDir, 0755); err != nil {
			t.Fatal(err)
		}
		_, err := MoveFile(srcDir, destDir)
		if !errors.Is(err, ErrSrcIsDir) {
			t.Errorf("got %v, want ErrSrcIsDir", err)
		}
	})

	t.Run("dest is a file", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "file.txt")
		notDir := filepath.Join(root, "regular.txt")
		writeFile(t, src, []byte("a"))
		writeFile(t, notDir, []byte("b"))
		_, err := MoveFile(src, notDir)
		if !errors.Is(err, ErrDestNotDir) {
			t.Errorf("got %v, want ErrDestNotDir", err)
		}
	})

	t.Run("dest missing", func(t *testing.T) {
		root := t.TempDir()
		src := filepath.Join(root, "file.txt")
		writeFile(t, src, []byte("a"))
		_, err := MoveFile(src, filepath.Join(root, "ghost"))
		if !errors.Is(err, ErrDestNotFound) {
			t.Errorf("got %v, want ErrDestNotFound", err)
		}
	})
}
