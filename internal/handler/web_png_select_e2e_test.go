package handler

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestPNGSelectedConvert_E2E covers SPEC §2.8.2 selection-aware PNG batch
// conversion via the toolbar button. Five scenarios mirror Phase 26 PS-3:
// baseline (no selection), partial PNG selection, mixed PNG + JPG selection
// (PNG-only filter), no-PNG selection (button hidden), folder navigation
// resets selection.
func TestPNGSelectedConvert_E2E(t *testing.T) {
	dataDir := t.TempDir()
	shots := filepath.Join(dataDir, "shots")
	other := filepath.Join(dataDir, "other")
	for _, p := range []string{shots, other} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 5 PNGs + 2 JPGs in /shots.
	for i := 1; i <= 5; i++ {
		writeBytes(t, filepath.Join(shots, fmt.Sprintf("img%d.png", i)), tinyPNG())
	}
	for i := 1; i <= 2; i++ {
		writeBytes(t, filepath.Join(shots, fmt.Sprintf("photo%d.jpg", i)), tinyJPEG())
	}
	// /other has 1 PNG so scenario 5 verifies the label re-counts post-nav.
	writeBytes(t, filepath.Join(other, "stray.png"), tinyPNG())

	server := startStickyServer(t, dataDir)
	defer server.Close()

	ctx, cancel := newStickyChromeCtx(t)
	defer cancel()
	runCtx, cancelRun := context.WithTimeout(ctx, 90*time.Second)
	defer cancelRun()

	if err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
		chromedp.Navigate(server.URL+"?path=/shots"),
		chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
		chromedp.Sleep(250*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate /shots: %v", err)
	}

	// Scenario 1: no selection → "모든 PNG 변환 (5개)".
	t.Run("baseline_no_selection", func(t *testing.T) {
		hidden, label := readPNGBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden, want visible")
		}
		if !strings.Contains(label, "모든 PNG 변환") || !strings.Contains(label, "5") {
			t.Errorf("label = %q, want '모든 PNG 변환 (5개)'", label)
		}
	})

	// Scenario 2: select 2 PNGs → "선택 PNG 변환 (2개)".
	t.Run("partial_png_selection", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			clickEntryCheckbox("img1.png"),
			clickEntryCheckbox("img2.png"),
			chromedp.Sleep(100*time.Millisecond),
		); err != nil {
			t.Fatalf("select 2 PNGs: %v", err)
		}
		hidden, label := readPNGBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden")
		}
		if !strings.Contains(label, "선택 PNG 변환") || !strings.Contains(label, "2") {
			t.Errorf("label = %q, want '선택 PNG 변환 (2개)'", label)
		}
	})

	// Scenario 3: also select 1 JPG → label still "선택 PNG 변환 (2개)";
	// dataset.paths contains exactly the 2 PNGs (non-PNG auto-excluded).
	t.Run("mixed_selection_excludes_non_png", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			clickEntryCheckbox("photo1.jpg"),
			chromedp.Sleep(100*time.Millisecond),
		); err != nil {
			t.Fatalf("add JPG to selection: %v", err)
		}
		hidden, label := readPNGBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden")
		}
		if !strings.Contains(label, "선택 PNG 변환") || !strings.Contains(label, "2") {
			t.Errorf("label = %q, want PNG count to stay 2 with non-PNG in selection", label)
		}
		var paths string
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.getElementById('convert-png-all-btn').dataset.paths`, &paths),
		); err != nil {
			t.Fatalf("read dataset.paths: %v", err)
		}
		if !strings.Contains(paths, "img1.png") || !strings.Contains(paths, "img2.png") {
			t.Errorf("dataset.paths = %s, want both img1.png and img2.png", paths)
		}
		if strings.Contains(paths, "photo1.jpg") {
			t.Errorf("dataset.paths contains JPG (should be PNG-only): %s", paths)
		}
	})

	// Scenario 4: clear selection, then select only JPGs → button hidden.
	t.Run("png_zero_in_selection_hides_button", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.getElementById('clear-selection-btn').click()`, nil),
			chromedp.Sleep(100*time.Millisecond),
			clickEntryCheckbox("photo1.jpg"),
			clickEntryCheckbox("photo2.jpg"),
			chromedp.Sleep(100*time.Millisecond),
		); err != nil {
			t.Fatalf("select only JPGs: %v", err)
		}
		hidden, _ := readPNGBatchBtn(t, runCtx)
		if !hidden {
			t.Errorf("button visible, want hidden when selection has 0 PNG")
		}
	})

	// Scenario 5: navigate to /other → selection cleared, label uses /other's
	// visible PNG count (1).
	t.Run("folder_nav_resets_selection", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Navigate(server.URL+"?path=/other"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate /other: %v", err)
		}
		hidden, label := readPNGBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden in /other")
		}
		if !strings.Contains(label, "모든 PNG 변환") || !strings.Contains(label, "1") {
			t.Errorf("label = %q, want '모든 PNG 변환 (1개)' (selection should reset)", label)
		}
	})
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readPNGBatchBtn(t *testing.T, ctx context.Context) (hidden bool, label string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('convert-png-all-btn').hidden`, &hidden),
		chromedp.Evaluate(`document.getElementById('convert-png-all-btn').textContent`, &label),
	); err != nil {
		t.Fatalf("read btn: %v", err)
	}
	return
}

// clickEntryCheckbox finds the card whose checkbox aria-label is "<name> 선택"
// and clicks it. Matches both .thumb-card and tr layouts (browse.js attaches
// the same aria-label pattern in both buildImageGrid and buildTable).
func clickEntryCheckbox(name string) chromedp.Action {
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card, tr');
  for (const c of cards) {
    const cb = c.querySelector('input[type="checkbox"]');
    if (!cb) continue;
    if (cb.getAttribute('aria-label') === %q) { cb.click(); return true; }
  }
  return false;
})()`, name+" 선택")
	return chromedp.Evaluate(js, nil)
}

func tinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func tinyJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
