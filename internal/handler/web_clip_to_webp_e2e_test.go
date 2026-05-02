package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestClipToWebP_E2E covers SPEC §2.9 UI gating via the toolbar batch button
// and per-card "WEBP" button. The encoder pipeline itself is exercised by the
// TestConvertWebP_* integration tests; this suite only verifies that the
// frontend renders the right buttons in the right places.
//
// Fixtures live in /clips:
//   - short.mp4   (duration sidecar 5s, ~empty file)        → isClip=true, convertable
//   - long.mp4    (duration sidecar 35s, ~empty file)       → isClip=false (>30s)
//   - big.mp4     (duration sidecar 5s, sparse 60 MiB)      → isClip=false (>50MiB)
//   - clip.gif    (tiny GIF bytes)                          → isClip=true, convertable
//   - clip.webp   (tiny WebP bytes)                         → isClip=true (SPEC §2.5.3 갱신),
//                                                             not convertable (§2.9 결과물)
//   - photo.jpg   (tiny JPEG bytes)                         → isClip=false, non-clip
func TestClipToWebP_E2E(t *testing.T) {
	dataDir := t.TempDir()
	clips := filepath.Join(dataDir, "clips")
	thumbs := filepath.Join(clips, ".thumb")
	for _, p := range []string{clips, thumbs} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// short.mp4 — small file, 5s sidecar. Browse only reads the duration
	// sidecar when the .thumb/<name>.jpg companion exists (browse.go:87),
	// so we plant a placeholder JPEG for each video fixture.
	writeBytes(t, filepath.Join(clips, "short.mp4"), []byte("dummy"))
	writeBytes(t, filepath.Join(thumbs, "short.mp4.jpg"), tinyJPEG())
	writeDurSidecar(t, thumbs, "short.mp4", 5.0)

	// long.mp4 — small file but 35s sidecar (over the 30s gate).
	writeBytes(t, filepath.Join(clips, "long.mp4"), []byte("dummy"))
	writeBytes(t, filepath.Join(thumbs, "long.mp4.jpg"), tinyJPEG())
	writeDurSidecar(t, thumbs, "long.mp4", 35.0)

	// big.mp4 — sparse 60 MiB, 5s sidecar (size gate fails).
	bigPath := filepath.Join(clips, "big.mp4")
	bf, err := os.Create(bigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := bf.Truncate(60 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	bf.Close()
	writeBytes(t, filepath.Join(thumbs, "big.mp4.jpg"), tinyJPEG())
	writeDurSidecar(t, thumbs, "big.mp4", 5.0)

	// clip.gif — tiny GIF89a header is enough for browse to identify mime.
	writeBytes(t, filepath.Join(clips, "clip.gif"), tinyGIF())
	// clip.webp — SPEC §2.5.3 says all WebPs are clips, but §2.9 excludes
	// them from the convertable input set (already a result). Browse does
	// extension-based mime mapping so any bytes work for the fixture.
	writeBytes(t, filepath.Join(clips, "clip.webp"), []byte("dummy-webp"))
	// photo.jpg — non-clip control entry.
	writeBytes(t, filepath.Join(clips, "photo.jpg"), tinyJPEG())

	server := startStickyServer(t, dataDir)
	defer server.Close()

	ctx, cancel := newStickyChromeCtx(t)
	defer cancel()
	runCtx, cancelRun := context.WithTimeout(ctx, 90*time.Second)
	defer cancelRun()

	if err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
		chromedp.Navigate(server.URL+"?path=/clips&type=clip"),
		chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
		chromedp.Sleep(250*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate /clips clip tab: %v", err)
	}

	// Scenario 1: clip tab shows "모든 움짤 WebP로 변환 (2개)".
	// Three cards are visible (short.mp4, clip.gif, clip.webp) but only the
	// two convertable ones (gif + short video) feed the batch label —
	// clip.webp is a result, not an input.
	t.Run("clip_tab_shows_batch_button", func(t *testing.T) {
		hidden, label := readWebPBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden, want visible on clip tab")
		}
		if !strings.Contains(label, "모든 움짤 WebP로 변환") || !strings.Contains(label, "2") {
			t.Errorf("label = %q, want '모든 움짤 WebP로 변환 (2개)' (webp excluded)", label)
		}
		var paths string
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.getElementById('convert-webp-all-btn').dataset.paths`, &paths),
		); err != nil {
			t.Fatalf("read dataset.paths: %v", err)
		}
		if strings.Contains(paths, "clip.webp") {
			t.Errorf("dataset.paths includes clip.webp (must be excluded): %s", paths)
		}
		if !strings.Contains(paths, "short.mp4") || !strings.Contains(paths, "clip.gif") {
			t.Errorf("dataset.paths missing convertable entries: %s", paths)
		}
	})

	// Scenario 1b: clip tab actually shows clip.webp as a card (SPEC §2.5.3
	// — WebP is now classified as a clip). Verifies the classification
	// change isn't accidentally reverted.
	t.Run("clip_tab_includes_webp_card", func(t *testing.T) {
		var got bool
		js := `(() => {
  const cards = document.querySelectorAll('.thumb-card');
  for (const c of cards) {
    const cb = c.querySelector('input[type="checkbox"]');
    if (cb && cb.getAttribute('aria-label') === 'clip.webp 선택') return true;
  }
  return false;
})()`
		if err := chromedp.Run(runCtx, chromedp.Evaluate(js, &got)); err != nil {
			t.Fatalf("eval: %v", err)
		}
		if !got {
			t.Errorf("clip.webp card not visible in clip tab — webp not classified as clip")
		}
	})

	// Scenario 2: select GIF → label switches to "선택 움짤 WebP로 변환 (1개)";
	// dataset.paths contains exactly /clips/clip.gif.
	t.Run("selection_changes_label", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			clickEntryCheckbox("clip.gif"),
			chromedp.Sleep(100*time.Millisecond),
		); err != nil {
			t.Fatalf("select clip.gif: %v", err)
		}
		hidden, label := readWebPBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden after selection")
		}
		if !strings.Contains(label, "선택 움짤 WebP로 변환") || !strings.Contains(label, "1") {
			t.Errorf("label = %q, want '선택 움짤 WebP로 변환 (1개)'", label)
		}
		var paths string
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.getElementById('convert-webp-all-btn').dataset.paths`, &paths),
		); err != nil {
			t.Fatalf("read dataset.paths: %v", err)
		}
		if !strings.Contains(paths, "clip.gif") {
			t.Errorf("dataset.paths = %s, want clip.gif", paths)
		}
		if strings.Contains(paths, "long.mp4") || strings.Contains(paths, "big.mp4") {
			t.Errorf("dataset.paths has non-clip: %s", paths)
		}
	})

	// Scenario 2b: selecting only clip.webp → button hidden because webp is
	// classified as a clip but excluded from convertable inputs.
	t.Run("webp_only_selection_hides_button", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.getElementById('clear-selection-btn').click()`, nil),
			chromedp.Sleep(100*time.Millisecond),
			clickEntryCheckbox("clip.webp"),
			chromedp.Sleep(100*time.Millisecond),
		); err != nil {
			t.Fatalf("select clip.webp only: %v", err)
		}
		hidden, _ := readWebPBatchBtn(t, runCtx)
		if !hidden {
			t.Errorf("button visible with webp-only selection, want hidden (webp not convertable)")
		}
	})

	// Scenario 3: per-card "WEBP" button present on convertable entries
	// (short.mp4 + clip.gif), absent on non-clip entries AND on clip.webp
	// (classified as clip but not a valid input — already a result).
	t.Run("card_buttons_match_clip_eligibility", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Navigate(server.URL+"?path=/clips&type=all"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate all tab: %v", err)
		}
		assertCardWebPBtn(t, runCtx, "short.mp4", true)
		assertCardWebPBtn(t, runCtx, "clip.gif", true)
		assertCardWebPBtn(t, runCtx, "long.mp4", false)
		assertCardWebPBtn(t, runCtx, "big.mp4", false)
		assertCardWebPBtn(t, runCtx, "photo.jpg", false)
		assertCardWebPBtn(t, runCtx, "clip.webp", false)
	})

	// Scenario 4: video tab hides the batch button even though clip-eligible
	// short.mp4 would surface in the all tab. Movement-of-mode rule from
	// SPEC §2.9 — batch button is gated by view.type === 'clip'.
	t.Run("other_tab_hides_batch_button", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Navigate(server.URL+"?path=/clips&type=video"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate video tab: %v", err)
		}
		hidden, _ := readWebPBatchBtn(t, runCtx)
		if !hidden {
			t.Errorf("button visible on video tab, want hidden")
		}
	})

	// Scenario 5: image tab also hides the batch button (same gate). Per
	// SPEC §2.5.3 the image tab excludes GIF/WebP clips outright, so we only
	// verify the batch button is hidden and photo.jpg lacks the WEBP button.
	t.Run("image_tab_hides_batch_button", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Navigate(server.URL+"?path=/clips&type=image"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate image tab: %v", err)
		}
		hidden, _ := readWebPBatchBtn(t, runCtx)
		if !hidden {
			t.Errorf("button visible on image tab, want hidden")
		}
		assertCardWebPBtn(t, runCtx, "photo.jpg", false)
	})

	// Scenario 6: empty selection in clip tab restores "모든 움짤 ..." label.
	// This guards the selection-clear path that browse.js uses when the
	// folder reloads or the clear-selection button fires.
	t.Run("clear_selection_restores_all_label", func(t *testing.T) {
		if err := chromedp.Run(runCtx,
			chromedp.Navigate(server.URL+"?path=/clips&type=clip"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
			clickEntryCheckbox("clip.gif"),
			chromedp.Sleep(100*time.Millisecond),
			chromedp.Evaluate(`document.getElementById('clear-selection-btn').click()`, nil),
			chromedp.Sleep(150*time.Millisecond),
		); err != nil {
			t.Fatalf("clear selection flow: %v", err)
		}
		hidden, label := readWebPBatchBtn(t, runCtx)
		if hidden {
			t.Errorf("button hidden after clear-selection")
		}
		if !strings.Contains(label, "모든 움짤 WebP로 변환") {
			t.Errorf("label = %q, want '모든 움짤 ...' after clear-selection", label)
		}
	})
}

// writeDurSidecar writes a thumb duration sidecar at <thumbs>/<name>.jpg.dur.
// Format mirrors thumb.WriteDurationSidecar (%.3f). The .jpg companion isn't
// required for browse to surface duration_sec — only the .dur file is read.
func writeDurSidecar(t *testing.T, thumbsDir, name string, sec float64) {
	t.Helper()
	path := filepath.Join(thumbsDir, name+".jpg.dur")
	data := []byte(strconv.FormatFloat(sec, 'f', 3, 64))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// readWebPBatchBtn returns the toolbar batch button's hidden flag and visible
// label. textContent stays in sync with browse.js's updateConvertWebPAllBtn.
func readWebPBatchBtn(t *testing.T, ctx context.Context) (hidden bool, label string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('convert-webp-all-btn').hidden`, &hidden),
		chromedp.Evaluate(`document.getElementById('convert-webp-all-btn').textContent`, &label),
	); err != nil {
		t.Fatalf("read btn: %v", err)
	}
	return
}

// assertCardWebPBtn fails the test if the WEBP card button presence on
// <name>'s card doesn't match want. Looks up the card via aria-label on the
// selection checkbox (same pattern as clickEntryCheckbox).
func assertCardWebPBtn(t *testing.T, ctx context.Context, name string, want bool) {
	t.Helper()
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card');
  for (const c of cards) {
    const cb = c.querySelector('input[type="checkbox"]');
    if (!cb) continue;
    if (cb.getAttribute('aria-label') === %q) {
      return c.querySelector('.webp-convert-btn') !== null;
    }
  }
  return null;
})()`, name+" 선택")
	var got bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &got)); err != nil {
		t.Fatalf("eval card btn for %s: %v", name, err)
	}
	if got != want {
		t.Errorf("card[%s] webp button = %v, want %v", name, got, want)
	}
}

// tinyGIF returns the smallest valid GIF89a payload (single 1x1 pixel).
// Browse identifies mime via extension only, but we keep the bytes valid in
// case future browse logic peeks at the header.
func tinyGIF() []byte {
	return []byte{
		0x47, 0x49, 0x46, 0x38, 0x39, 0x61, // "GIF89a"
		0x01, 0x00, 0x01, 0x00, // 1x1
		0x80, 0x00, 0x00, // GCT flag, bg index, aspect
		0x00, 0x00, 0x00, 0xff, 0xff, 0xff, // 2-color GCT
		0x21, 0xf9, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, // graphic control ext
		0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, // image descriptor
		0x02, 0x02, 0x44, 0x01, 0x00, // image data
		0x3b, // trailer
	}
}
