package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// TestClipHoverPlayback_E2E covers SPEC §2.5.6 — GIF/WebP cards render a
// static thumb URL by default and toggle to the stream URL on hover (desktop)
// or IntersectionObserver entry (mobile). The static-first-frame thumb
// generation itself is exercised by the existing thumb_test.go; this suite
// verifies the client-side src toggling and the clip-tab grid width override.
//
// Mobile emulation uses Page.addScriptToEvaluateOnNewDocument to stub
// window.matchMedia BEFORE browse.js evaluates HOVER_CAPABLE at module
// import time. matchMedia stub is the most reliable cross-platform way to
// flip the (hover: hover) branch — chromedp's user-agent or
// Emulation.setTouchEmulationEnabled don't directly affect matchMedia.
func TestClipHoverPlayback_E2E(t *testing.T) {
	dataDir := t.TempDir()
	clips := filepath.Join(dataDir, "clips")
	thumbs := filepath.Join(clips, ".thumb")
	for _, p := range []string{clips, thumbs} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// clip.gif — valid GIF89a so /api/thumb produces a real first-frame jpg.
	writeBytes(t, filepath.Join(clips, "clip.gif"), tinyGIF())
	// clip.webp — image/webp content. Browse maps mime by extension; the
	// hover/IO toggle test only inspects img.src strings, so the bytes
	// don't need to be a valid WebP.
	writeBytes(t, filepath.Join(clips, "clip.webp"), []byte("dummy-webp"))
	// photo.jpg — non-clip control. No data-clip-card attribute expected.
	writeBytes(t, filepath.Join(clips, "photo.jpg"), tinyJPEG())

	server := startStickyServer(t, dataDir)
	defer server.Close()

	t.Run("desktop_hover_toggles_src", func(t *testing.T) {
		ctx, cancel := newStickyChromeCtx(t)
		defer cancel()
		runCtx, cancelRun := context.WithTimeout(ctx, 60*time.Second)
		defer cancelRun()

		if err := chromedp.Run(runCtx,
			chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
			chromedp.Navigate(server.URL+"?path=/clips&type=clip"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate: %v", err)
		}

		// Initial src on clip.gif card should be /api/thumb.
		gifSelector := `(() => {
  const card = [...document.querySelectorAll('.thumb-card')]
    .find(c => c.querySelector('input[type=checkbox]')?.getAttribute('aria-label') === 'clip.gif 선택');
  return card ? card.querySelector('img').src : '';
})()`
		var initialSrc string
		if err := chromedp.Run(runCtx, chromedp.Evaluate(gifSelector, &initialSrc)); err != nil {
			t.Fatalf("read initial src: %v", err)
		}
		if !strings.Contains(initialSrc, "/api/thumb") {
			t.Errorf("initial src = %q, want /api/thumb", initialSrc)
		}

		// Dispatch mouseenter → src should switch to /api/stream.
		hoverScript := `(() => {
  const card = [...document.querySelectorAll('.thumb-card')]
    .find(c => c.querySelector('input[type=checkbox]')?.getAttribute('aria-label') === 'clip.gif 선택');
  card.dispatchEvent(new MouseEvent('mouseenter', {bubbles: true}));
  return card.querySelector('img').src;
})()`
		var hoverSrc string
		if err := chromedp.Run(runCtx, chromedp.Evaluate(hoverScript, &hoverSrc)); err != nil {
			t.Fatalf("hover: %v", err)
		}
		if !strings.Contains(hoverSrc, "/api/stream") {
			t.Errorf("hover src = %q, want /api/stream", hoverSrc)
		}

		// mouseleave → src returns to /api/thumb.
		leaveScript := `(() => {
  const card = [...document.querySelectorAll('.thumb-card')]
    .find(c => c.querySelector('input[type=checkbox]')?.getAttribute('aria-label') === 'clip.gif 선택');
  card.dispatchEvent(new MouseEvent('mouseleave', {bubbles: true}));
  return card.querySelector('img').src;
})()`
		var leaveSrc string
		if err := chromedp.Run(runCtx, chromedp.Evaluate(leaveScript, &leaveSrc)); err != nil {
			t.Fatalf("leave: %v", err)
		}
		if !strings.Contains(leaveSrc, "/api/thumb") {
			t.Errorf("leave src = %q, want /api/thumb", leaveSrc)
		}
	})

	t.Run("mobile_intersection_toggles_src", func(t *testing.T) {
		ctx, cancel := newStickyChromeCtx(t)
		defer cancel()
		runCtx, cancelRun := context.WithTimeout(ctx, 60*time.Second)
		defer cancelRun()

		// Stub matchMedia so HOVER_CAPABLE evaluates to false. Must run
		// BEFORE browse.js executes — addScriptToEvaluateOnNewDocument
		// installs the stub on every navigation in this tab.
		stubScript := `Object.defineProperty(window, 'matchMedia', {
  configurable: true,
  writable: true,
  value: (q) => ({
    matches: false,
    media: q,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }),
});`

		if err := chromedp.Run(runCtx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				_, err := page.AddScriptToEvaluateOnNewDocument(stubScript).Do(ctx)
				return err
			}),
			chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
			chromedp.Navigate(server.URL+"?path=/clips&type=clip"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			// IntersectionObserver fires asynchronously after layout. Give
			// a tick for the initial intersection callback to run.
			chromedp.Sleep(400*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate with stub: %v", err)
		}

		// Card is in viewport (small fixture set, no scroll needed) →
		// IntersectionObserver should set src to /api/stream.
		var visibleSrc string
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`(() => {
  const card = [...document.querySelectorAll('.thumb-card')]
    .find(c => c.querySelector('input[type=checkbox]')?.getAttribute('aria-label') === 'clip.gif 선택');
  return card ? card.querySelector('img').src : '';
})()`, &visibleSrc),
		); err != nil {
			t.Fatalf("read visible src: %v", err)
		}
		if !strings.Contains(visibleSrc, "/api/stream") {
			t.Errorf("visible src under IO = %q, want /api/stream (IO should activate)", visibleSrc)
		}

		// Verify HOVER_CAPABLE was actually false by checking that the
		// card has data-clip-state set (only the IO branch sets it).
		var clipState string
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`(() => {
  const card = [...document.querySelectorAll('.thumb-card')]
    .find(c => c.querySelector('input[type=checkbox]')?.getAttribute('aria-label') === 'clip.gif 선택');
  return card ? card.dataset.clipState || '' : '';
})()`, &clipState),
		); err != nil {
			t.Fatalf("read clipState: %v", err)
		}
		if clipState == "" {
			t.Errorf("data-clip-state empty — IO branch likely not taken (matchMedia stub failed?)")
		}
	})

	t.Run("clip_tab_grid_uses_240px", func(t *testing.T) {
		ctx, cancel := newStickyChromeCtx(t)
		defer cancel()
		runCtx, cancelRun := context.WithTimeout(ctx, 60*time.Second)
		defer cancelRun()

		if err := chromedp.Run(runCtx,
			chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
			chromedp.Navigate(server.URL+"?path=/clips&type=clip"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(150*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate: %v", err)
		}

		var hasClipClass bool
		var gridCols string
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.querySelector('.image-grid').classList.contains('image-grid-clip')`, &hasClipClass),
			chromedp.Evaluate(`getComputedStyle(document.querySelector('.image-grid')).gridTemplateColumns`, &gridCols),
		); err != nil {
			t.Fatalf("read grid: %v", err)
		}
		if !hasClipClass {
			t.Errorf("image-grid-clip class missing in clip tab")
		}
		// 240px viewport baseline: gridTemplateColumns should expand into
		// pixel-per-column values. Each column ≥ 240px.
		if gridCols == "" || gridCols == "none" {
			t.Errorf("gridTemplateColumns = %q, want computed track list", gridCols)
		}
		// Quick sanity check — extract first track px and ensure ≥240.
		// Format example: "240.5px 240.5px 240.5px ...". A track <240 means
		// the override didn't take effect.
		if !strings.Contains(gridCols, "px") {
			t.Errorf("gridTemplateColumns = %q, want px-based tracks", gridCols)
		}
		assertFirstTrackAtLeast(t, gridCols, 240)
	})

	t.Run("non_clip_tab_keeps_default_grid", func(t *testing.T) {
		ctx, cancel := newStickyChromeCtx(t)
		defer cancel()
		runCtx, cancelRun := context.WithTimeout(ctx, 60*time.Second)
		defer cancelRun()

		if err := chromedp.Run(runCtx,
			chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
			chromedp.Navigate(server.URL+"?path=/clips&type=image"),
			chromedp.WaitVisible(`.thumb-card`, chromedp.ByQuery),
			chromedp.Sleep(150*time.Millisecond),
		); err != nil {
			t.Fatalf("navigate: %v", err)
		}

		var hasClipClass bool
		if err := chromedp.Run(runCtx,
			chromedp.Evaluate(`document.querySelector('.image-grid').classList.contains('image-grid-clip')`, &hasClipClass),
		); err != nil {
			t.Fatalf("read grid: %v", err)
		}
		if hasClipClass {
			t.Errorf("image-grid-clip class present in image tab — should be clip-tab only")
		}
	})
}

// assertFirstTrackAtLeast parses the first "<n>px" token from a CSS
// gridTemplateColumns computed value and fails if it's below minPx.
func assertFirstTrackAtLeast(t *testing.T, computed string, minPx int) {
	t.Helper()
	tracks := strings.Fields(computed)
	if len(tracks) == 0 {
		t.Fatalf("no grid tracks parsed from %q", computed)
	}
	first := tracks[0]
	pxIdx := strings.Index(first, "px")
	if pxIdx <= 0 {
		t.Fatalf("first track %q lacks px suffix", first)
	}
	var px float64
	if _, err := fmt.Sscanf(first[:pxIdx], "%f", &px); err != nil {
		t.Fatalf("parse px from %q: %v", first, err)
	}
	if int(px) < minPx {
		t.Errorf("first grid track = %v px, want ≥ %d px (clip override)", px, minPx)
	}
}
