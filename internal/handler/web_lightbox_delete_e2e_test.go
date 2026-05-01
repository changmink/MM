package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// TestLightboxDelete_E2E covers SPEC §2.5.5 lightbox-internal deletion.
// Six scenarios mirror Phase 28 LD-4:
//  1. image lightbox 🗑 click → next image displayed (3-image folder)
//  2. image lightbox 🗑 click on last remaining image → lightbox closes
//  3. image lightbox `Delete` key → next image displayed
//  4. video lightbox 🗑 click → lightbox closes + folder grid refreshes
//  5. confirm() dismissed → no DELETE, lightbox state preserved
//  6. `Delete` key while lightbox is closed → no-op (no card mutation)
//
// confirm() is replaced per-scenario with `window.confirm = () => <bool>` so
// subtests don't have to coordinate a shared dialog listener.
func TestLightboxDelete_E2E(t *testing.T) {
	dataDir := t.TempDir()
	// One subfolder per scenario keeps mutations isolated.
	for _, p := range []string{
		"advance", "last", "keyboard", "video", "cancel", "inactive",
	} {
		if err := os.Mkdir(filepath.Join(dataDir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// /advance: 3 images — scenario 1 (next-image after delete)
	for i := 1; i <= 3; i++ {
		writeBytes(t, filepath.Join(dataDir, "advance", fmt.Sprintf("img%d.png", i)), tinyPNG())
	}
	// /last: 1 image — scenario 2 (close after deleting the only image)
	writeBytes(t, filepath.Join(dataDir, "last", "only.png"), tinyPNG())
	// /keyboard: 3 images — scenario 3 (Delete key)
	for i := 1; i <= 3; i++ {
		writeBytes(t, filepath.Join(dataDir, "keyboard", fmt.Sprintf("k%d.png", i)), tinyPNG())
	}
	// /video: 1 video — scenario 4 (video lightbox close)
	// Bytes don't need to be a real MP4 — the lightbox just needs the entry to
	// exist and the server to classify it as type=video by extension.
	writeBytes(t, filepath.Join(dataDir, "video", "clip.mp4"), []byte("fake-mp4"))
	// /cancel: 2 images — scenario 5 (confirm dismissed → no change)
	for i := 1; i <= 2; i++ {
		writeBytes(t, filepath.Join(dataDir, "cancel", fmt.Sprintf("c%d.png", i)), tinyPNG())
	}
	// /inactive: 2 images — scenario 6 (Delete key with lightbox closed)
	for i := 1; i <= 2; i++ {
		writeBytes(t, filepath.Join(dataDir, "inactive", fmt.Sprintf("i%d.png", i)), tinyPNG())
	}

	server := startStickyServer(t, dataDir)
	defer server.Close()

	ctx, cancel := newStickyChromeCtx(t)
	defer cancel()
	runCtx, cancelRun := context.WithTimeout(ctx, 120*time.Second)
	defer cancelRun()

	if err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
	); err != nil {
		t.Fatalf("emulate viewport: %v", err)
	}

	// Scenario 1: 3 images, open first, delete → second image shown.
	t.Run("image_delete_advances", func(t *testing.T) {
		navigateToFolder(t, runCtx, server.URL, "/advance")
		setConfirmAccept(t, runCtx, true)
		openImageLightbox(t, runCtx, "img1.png")
		clickLightboxDelete(t, runCtx)
		waitForBrowseReload(t, runCtx)
		if hidden := lightboxHidden(t, runCtx); hidden {
			t.Errorf("lightbox hidden after delete; want still open showing next image")
		}
		got := currentLightboxImage(t, runCtx)
		if got == "" || got == "img1.png" {
			t.Errorf("lightbox image = %q after delete; want img2.png (next)", got)
		}
		if fileExists(filepath.Join(dataDir, "advance", "img1.png")) {
			t.Errorf("img1.png still on disk after delete")
		}
	})

	// Scenario 2: 1 image, delete → lightbox closes.
	t.Run("image_delete_last_closes", func(t *testing.T) {
		navigateToFolder(t, runCtx, server.URL, "/last")
		setConfirmAccept(t, runCtx, true)
		openImageLightbox(t, runCtx, "only.png")
		clickLightboxDelete(t, runCtx)
		waitForBrowseReload(t, runCtx)
		if !lightboxHidden(t, runCtx) {
			t.Errorf("lightbox still open after deleting last image; want closed")
		}
		if fileExists(filepath.Join(dataDir, "last", "only.png")) {
			t.Errorf("only.png still on disk after delete")
		}
	})

	// Scenario 3: Delete key triggers same flow as 🗑 click.
	t.Run("image_delete_keyboard", func(t *testing.T) {
		navigateToFolder(t, runCtx, server.URL, "/keyboard")
		setConfirmAccept(t, runCtx, true)
		openImageLightbox(t, runCtx, "k1.png")
		if err := chromedp.Run(runCtx,
			chromedp.KeyEvent(kb.Delete), // Delete key
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("press Delete: %v", err)
		}
		waitForBrowseReload(t, runCtx)
		got := currentLightboxImage(t, runCtx)
		if got == "" || got == "k1.png" {
			t.Errorf("lightbox image = %q after Delete key; want k2.png", got)
		}
		if fileExists(filepath.Join(dataDir, "keyboard", "k1.png")) {
			t.Errorf("k1.png still on disk after Delete key")
		}
	})

	// Scenario 4: video lightbox → 🗑 → close + folder refresh.
	t.Run("video_delete_closes", func(t *testing.T) {
		navigateToFolder(t, runCtx, server.URL, "/video")
		setConfirmAccept(t, runCtx, true)
		openVideoLightbox(t, runCtx, "clip.mp4")
		clickLightboxDelete(t, runCtx)
		waitForBrowseReload(t, runCtx)
		if !lightboxHidden(t, runCtx) {
			t.Errorf("video lightbox still open after delete; want closed")
		}
		if fileExists(filepath.Join(dataDir, "video", "clip.mp4")) {
			t.Errorf("clip.mp4 still on disk after delete")
		}
		// Grid should now be empty (folder refresh applied).
		if got := visibleCardCount(t, runCtx); got != 0 {
			t.Errorf("visible cards = %d after delete; want 0", got)
		}
	})

	// Scenario 5: confirm dismissed → DELETE not sent, file remains.
	t.Run("confirm_cancel_no_change", func(t *testing.T) {
		navigateToFolder(t, runCtx, server.URL, "/cancel")
		setConfirmAccept(t, runCtx, false)
		openImageLightbox(t, runCtx, "c1.png")
		clickLightboxDelete(t, runCtx)
		// Give DOM a beat in case any work mistakenly proceeds.
		if err := chromedp.Run(runCtx, chromedp.Sleep(200*time.Millisecond)); err != nil {
			t.Fatalf("sleep: %v", err)
		}
		if lightboxHidden(t, runCtx) {
			t.Errorf("lightbox closed after confirm dismiss; want still open")
		}
		if got := currentLightboxImage(t, runCtx); got != "c1.png" {
			t.Errorf("lightbox image = %q after confirm dismiss; want c1.png unchanged", got)
		}
		if !fileExists(filepath.Join(dataDir, "cancel", "c1.png")) {
			t.Errorf("c1.png deleted despite confirm dismiss")
		}
	})

	// Scenario 6: Delete key with lightbox closed → no-op.
	t.Run("delete_key_inactive_when_closed", func(t *testing.T) {
		navigateToFolder(t, runCtx, server.URL, "/inactive")
		// confirm() should never fire — but pre-arm it to "false" so a stray
		// fire would still leave files intact and surface the bug as a hang.
		setConfirmAccept(t, runCtx, false)
		// Sanity: lightbox must be hidden before pressing Delete.
		if !lightboxHidden(t, runCtx) {
			t.Fatalf("lightbox open before scenario 6 — fixture leak")
		}
		if err := chromedp.Run(runCtx,
			chromedp.KeyEvent(kb.Delete),
			chromedp.Sleep(200*time.Millisecond),
		); err != nil {
			t.Fatalf("press Delete (closed): %v", err)
		}
		// Both files still present.
		for _, name := range []string{"i1.png", "i2.png"} {
			if !fileExists(filepath.Join(dataDir, "inactive", name)) {
				t.Errorf("%s deleted by Delete key while lightbox closed", name)
			}
		}
	})
}

// navigateToFolder loads the given path and waits for at least one card to render.
// If the folder is video-only, .thumb-card still applies (buildVideoGrid uses it).
func navigateToFolder(t *testing.T, ctx context.Context, baseURL, path string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(baseURL+"?path="+path),
		chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	); err != nil {
		t.Fatalf("navigate %s: %v", path, err)
	}
}

// setConfirmAccept overrides window.confirm in the page so subtests don't have
// to coordinate a single dialog listener. Pass true to auto-accept, false to
// auto-dismiss.
func setConfirmAccept(t *testing.T, ctx context.Context, accept bool) {
	t.Helper()
	js := fmt.Sprintf(`window.confirm = () => %t;`, accept)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, nil)); err != nil {
		t.Fatalf("override confirm: %v", err)
	}
}

// openImageLightbox clicks the image card matching name, waits for #lightbox
// to lose .hidden and for an <img> to mount in #lb-content.
func openImageLightbox(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card[data-path]');
  for (const c of cards) {
    if (c.dataset.path.endsWith('/' + %q)) { c.querySelector('img').click(); return true; }
  }
  return false;
})()`, name)
	var clicked bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(js, &clicked),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("open image lightbox %s: %v", name, err)
	}
	if !clicked {
		t.Fatalf("card %s not found", name)
	}
	if hidden := lightboxHidden(t, ctx); hidden {
		t.Fatalf("lightbox hidden after clicking %s", name)
	}
}

// openVideoLightbox is the video-card variant — the click target is the same
// <img> element inside the .thumb-card.
func openVideoLightbox(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card[data-path]');
  for (const c of cards) {
    if (c.dataset.path.endsWith('/' + %q)) { c.querySelector('img').click(); return true; }
  }
  return false;
})()`, name)
	var clicked bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(js, &clicked),
		chromedp.Sleep(150*time.Millisecond),
	); err != nil {
		t.Fatalf("open video lightbox %s: %v", name, err)
	}
	if !clicked {
		t.Fatalf("video card %s not found", name)
	}
}

// clickLightboxDelete dispatches a click on the 🗑 button via element.click() —
// MouseEvent dispatch would also work but JS click is simpler and avoids
// pixel-level coordinate calculations for the absolutely-positioned button.
func clickLightboxDelete(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('lb-delete').click()`, nil),
		chromedp.Sleep(250*time.Millisecond),
	); err != nil {
		t.Fatalf("click lb-delete: %v", err)
	}
}

// waitForBrowseReload gives the async fetch + renderView pipeline time to
// settle after deleteCurrentLightboxItem fires `browse(currentPath, false)`.
func waitForBrowseReload(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond)); err != nil {
		t.Fatalf("wait reload: %v", err)
	}
}

func lightboxHidden(t *testing.T, ctx context.Context) bool {
	t.Helper()
	var hidden bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.getElementById('lightbox').classList.contains('hidden')`, &hidden),
	); err != nil {
		t.Fatalf("lightbox hidden check: %v", err)
	}
	return hidden
}

// currentLightboxImage returns the file basename in #lb-content > img src,
// or "" if no img is present.
func currentLightboxImage(t *testing.T, ctx context.Context) string {
	t.Helper()
	var name string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
  const img = document.querySelector('#lb-content img');
  if (!img) return '';
  // src looks like /api/stream?path=%2Fadvance%2Fimg2.png
  const u = new URL(img.src);
  const p = decodeURIComponent(u.searchParams.get('path') || '');
  const i = p.lastIndexOf('/');
  return i < 0 ? p : p.substring(i + 1);
})()`, &name),
	); err != nil {
		t.Fatalf("current lightbox image: %v", err)
	}
	return name
}

func visibleCardCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.thumb-card[data-path]').length`, &n),
	); err != nil {
		t.Fatalf("visible card count: %v", err)
	}
	return n
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

