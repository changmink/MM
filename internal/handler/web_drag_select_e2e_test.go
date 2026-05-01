package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// TestDragSelect_E2E covers SPEC §2.5.4 rubber-band area selection.
// Six scenarios mirror Phase 27 DS-3:
//   1. short drag (<5px) does not change selection
//   2. dragging a rectangle selects intersecting visible cards
//   3. mousedown on a card does NOT trigger rubber-band (folder DnD path)
//   4. Ctrl+drag is additive (existing selection preserved + new added)
//   5. ESC during drag restores mousedown-time snapshot
//   6. viewport ≤600px disables the feature entirely
func TestDragSelect_E2E(t *testing.T) {
	dataDir := t.TempDir()
	shots := filepath.Join(dataDir, "shots")
	if err := os.Mkdir(shots, 0o755); err != nil {
		t.Fatal(err)
	}
	// 6 PNGs in a known order so the grid layout is predictable.
	for i := 1; i <= 6; i++ {
		if err := os.WriteFile(filepath.Join(shots, fmt.Sprintf("img%d.png", i)),
			tinyPNG(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

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

	// Scenario 1: short drag below movement threshold — no overlay, no selection change.
	t.Run("short_drag_below_threshold", func(t *testing.T) {
		clearSelection(t, runCtx)
		bx, by := emptyAreaXY(t, runCtx)
		if err := chromedp.Run(runCtx,
			dragRect(bx, by, bx+3, by+3, 0),
			chromedp.Sleep(80*time.Millisecond),
		); err != nil {
			t.Fatalf("short drag: %v", err)
		}
		if got := selectionSize(t, runCtx); got != 0 {
			t.Errorf("selection size = %d after short drag, want 0", got)
		}
		if overlayExists(t, runCtx) {
			t.Errorf("overlay present after short drag, want absent")
		}
	})

	// Scenario 2: rectangle that covers all 6 cards — selection size 6.
	t.Run("drag_selects_intersecting_cards", func(t *testing.T) {
		clearSelection(t, runCtx)
		gridLeft, gridTop, gridRight, gridBottom := gridBounds(t, runCtx)
		// Start above-left of grid (in empty header band) and end below-right
		// of grid so all cards are inside the rectangle.
		x1, y1 := int(gridLeft)-5, int(gridTop)-5
		x2, y2 := int(gridRight)+5, int(gridBottom)+5
		// Empty area must exist near the start point — verify no card under it.
		if err := chromedp.Run(runCtx,
			dragRect(x1, y1, x2, y2, 0),
			chromedp.Sleep(120*time.Millisecond),
		); err != nil {
			t.Fatalf("rectangle drag: %v", err)
		}
		if got := selectionSize(t, runCtx); got != 6 {
			t.Errorf("selection size = %d, want 6 (all cards in rect)", got)
		}
	})

	// Scenario 3: mousedown ON a card → no rubber-band overlay, no selection
	// change from rubber-band code path. (HTML5 dragstart owns the gesture.)
	t.Run("card_mousedown_no_rubberband", func(t *testing.T) {
		clearSelection(t, runCtx)
		cx, cy := cardCenterXY(t, runCtx, "img1.png")
		// End the drag well outside the card so mousedown/mouseup don't share
		// a target — that prevents the synthetic `click` from opening the
		// lightbox and leaking modal state into subsequent scenarios.
		if err := chromedp.Run(runCtx,
			dragRect(cx, cy, cx+400, cy+300, 0),
			chromedp.Sleep(120*time.Millisecond),
		); err != nil {
			t.Fatalf("card drag: %v", err)
		}
		if overlayExists(t, runCtx) {
			t.Errorf("overlay present after card mousedown, want absent (folder DnD path)")
		}
		// Defensive: close any lightbox that may have opened anyway.
		closeAnyModal(t, runCtx)
	})

	// Scenario 4: Ctrl+drag is additive — existing selection survives and new
	// rect-intersected cards add on top.
	t.Run("ctrl_drag_is_additive", func(t *testing.T) {
		clearSelection(t, runCtx)
		// Pre-select img5 via checkbox.
		if err := chromedp.Run(runCtx,
			clickEntryCheckbox("img5.png"),
			chromedp.Sleep(80*time.Millisecond),
		); err != nil {
			t.Fatalf("pre-select img5: %v", err)
		}
		if got := selectionSize(t, runCtx); got != 1 {
			t.Fatalf("pre-select size = %d, want 1", got)
		}
		// Ctrl+drag must START in empty area (rubber-band only fires on empty
		// mousedown — cards take the HTML5 dragstart path). Use the same
		// safe start point as the rectangle scenario; end inside img1 so the
		// rectangle right is well before img2.left → only img1 intersects.
		gridLeft, gridTop, _, _ := gridBounds(t, runCtx)
		_, img1T, img1R, _ := singleCardRect(t, runCtx, "img1.png")
		x1, y1 := int(gridLeft)-5, int(gridTop)-5
		x2, y2 := img1R-1, img1T+10
		if err := chromedp.Run(runCtx,
			dragRect(x1, y1, x2, y2, input.ModifierCtrl),
			chromedp.Sleep(120*time.Millisecond),
		); err != nil {
			t.Fatalf("ctrl drag: %v", err)
		}
		if got := selectionSize(t, runCtx); got != 2 {
			t.Errorf("selection size = %d after Ctrl+drag, want 2 (img5 kept + img1 added)", got)
		}
		if !isSelected(t, runCtx, "img5.png") {
			t.Errorf("img5 not in selection — additive failed")
		}
		if !isSelected(t, runCtx, "img1.png") {
			t.Errorf("img1 not in selection — drag did not add")
		}
	})

	// Scenario 5: ESC during drag restores mousedown-time selection snapshot.
	t.Run("esc_restores_snapshot", func(t *testing.T) {
		clearSelection(t, runCtx)
		// Pre-select img3.
		if err := chromedp.Run(runCtx,
			clickEntryCheckbox("img3.png"),
			chromedp.Sleep(80*time.Millisecond),
		); err != nil {
			t.Fatalf("pre-select img3: %v", err)
		}
		// Start a (non-additive) drag covering several cards, then ESC mid-drag.
		gridLeft, gridTop, gridRight, gridBottom := gridBounds(t, runCtx)
		x1, y1 := int(gridLeft)-5, int(gridTop)-5
		x2, y2 := int(gridRight)+5, int(gridBottom)+5
		if err := chromedp.Run(runCtx,
			input.DispatchMouseEvent(input.MouseMoved, float64(x1), float64(y1)),
			input.DispatchMouseEvent(input.MousePressed, float64(x1), float64(y1)).
				WithButton(input.Left).WithClickCount(1),
			// Sweep across to draw the rect.
			input.DispatchMouseEvent(input.MouseMoved, float64(x2), float64(y2)),
			chromedp.Sleep(60*time.Millisecond),
			// ESC during drag.
			chromedp.KeyEvent(""),
			chromedp.Sleep(80*time.Millisecond),
			// Release the button to clean up any state.
			input.DispatchMouseEvent(input.MouseReleased, float64(x2), float64(y2)).
				WithButton(input.Left),
			chromedp.Sleep(80*time.Millisecond),
		); err != nil {
			t.Fatalf("esc during drag: %v", err)
		}
		if got := selectionSize(t, runCtx); got != 1 {
			t.Errorf("selection size = %d after ESC, want 1 (snapshot of img3 only)", got)
		}
		if !isSelected(t, runCtx, "img3.png") {
			t.Errorf("img3 not in selection after ESC — snapshot restore failed")
		}
		if overlayExists(t, runCtx) {
			t.Errorf("overlay still present after ESC, want cleaned up")
		}
	})

	// Scenario 6: viewport ≤600px disables drag-select entirely.
	t.Run("mobile_viewport_disabled", func(t *testing.T) {
		clearSelection(t, runCtx)
		// Resize to mobile and reload so wireDragSelect re-runs (it checks
		// width on each mousedown via window.innerWidth, so resize alone
		// suffices — no reload needed).
		if err := chromedp.Run(runCtx,
			chromedp.EmulateViewport(500, 800),
			chromedp.Sleep(120*time.Millisecond),
		); err != nil {
			t.Fatalf("resize to mobile: %v", err)
		}
		// Drag a big rectangle in empty area.
		if err := chromedp.Run(runCtx,
			dragRect(50, 600, 450, 750, 0),
			chromedp.Sleep(120*time.Millisecond),
		); err != nil {
			t.Fatalf("drag at mobile width: %v", err)
		}
		if got := selectionSize(t, runCtx); got != 0 {
			t.Errorf("selection size = %d at mobile width, want 0 (feature disabled)", got)
		}
		if overlayExists(t, runCtx) {
			t.Errorf("overlay present at mobile width, want feature disabled")
		}
		// Restore desktop viewport for subsequent runs (idempotent in case
		// other tests share the context).
		if err := chromedp.Run(runCtx,
			chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
		); err != nil {
			t.Fatalf("restore desktop viewport: %v", err)
		}
	})
}

// dragRect simulates a left-button drag from (x1,y1) to (x2,y2). Modifiers
// are applied to mousedown/mousemove/mouseup so Ctrl/Shift+drag is honored.
func dragRect(x1, y1, x2, y2 int, mods input.Modifier) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Move to start without pressing.
		if err := chromedp.Run(ctx,
			input.DispatchMouseEvent(input.MouseMoved, float64(x1), float64(y1)).
				WithModifiers(mods),
		); err != nil {
			return err
		}
		// Press.
		if err := chromedp.Run(ctx,
			input.DispatchMouseEvent(input.MousePressed, float64(x1), float64(y1)).
				WithButton(input.Left).WithClickCount(1).WithModifiers(mods),
		); err != nil {
			return err
		}
		// Step the move so threshold + intersect logic both run.
		mid := []struct{ x, y int }{
			{(x1 + x2) / 2, (y1 + y2) / 2},
			{x2, y2},
		}
		for _, p := range mid {
			if err := chromedp.Run(ctx,
				input.DispatchMouseEvent(input.MouseMoved, float64(p.x), float64(p.y)).
					WithModifiers(mods),
			); err != nil {
				return err
			}
		}
		// Release.
		return chromedp.Run(ctx,
			input.DispatchMouseEvent(input.MouseReleased, float64(x2), float64(y2)).
				WithButton(input.Left).WithModifiers(mods),
		)
	})
}

// closeAnyModal clicks the lightbox close button (and hides any modal-overlay)
// if one is open, so leaked modal state doesn't gate subsequent mousedowns.
func closeAnyModal(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
  const lb = document.getElementById('lightbox');
  if (lb && !lb.classList.contains('hidden')) {
    document.getElementById('lb-close')?.click();
  }
  document.querySelectorAll('.modal-overlay').forEach(m => m.classList.add('hidden'));
})()`, nil),
		chromedp.Sleep(80*time.Millisecond),
	); err != nil {
		t.Fatalf("closeAnyModal: %v", err)
	}
}

func clearSelection(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
  const btn = document.getElementById('clear-selection-btn');
  if (btn && !btn.hidden) btn.click();
})()`, nil),
		chromedp.Sleep(60*time.Millisecond),
	); err != nil {
		t.Fatalf("clearSelection: %v", err)
	}
}

func selectionSize(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	// .selected class is toggled on the card by browse.js's bindEntrySelection;
	// that's what selectedPaths drives at render time. Counting visible
	// .selected cards is equivalent to selectedPaths.size for visible items.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('.thumb-card.selected, tr.selected').length`, &n),
	); err != nil {
		t.Fatalf("selectionSize: %v", err)
	}
	return n
}

func isSelected(t *testing.T, ctx context.Context, name string) bool {
	t.Helper()
	var ok bool
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card[data-path], tr[data-path]');
  for (const c of cards) {
    if (c.dataset.path.endsWith('/' + %q) && c.classList.contains('selected')) return true;
  }
  return false;
})()`, name)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		t.Fatalf("isSelected: %v", err)
	}
	return ok
}

func overlayExists(t *testing.T, ctx context.Context) bool {
	t.Helper()
	var present bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`!!document.querySelector('.drag-select-overlay')`, &present),
	); err != nil {
		t.Fatalf("overlayExists: %v", err)
	}
	return present
}

// emptyAreaXY returns viewport coords inside <main> but outside any card —
// suitable for starting a rubber-band drag.
func emptyAreaXY(t *testing.T, ctx context.Context) (int, int) {
	t.Helper()
	type point struct{ X, Y float64 }
	var p point
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
  const grid = document.querySelector('.image-grid');
  const main = document.querySelector('main');
  const gr = grid.getBoundingClientRect();
  const mr = main.getBoundingClientRect();
  // Below the grid, well within main, away from any card.
  return {X: mr.left + 30, Y: gr.bottom + 40};
})()`, &p),
	); err != nil {
		t.Fatalf("emptyAreaXY: %v", err)
	}
	return int(p.X), int(p.Y)
}

func gridBounds(t *testing.T, ctx context.Context) (left, top, right, bottom float64) {
	t.Helper()
	type box struct{ Left, Top, Right, Bottom float64 }
	var b box
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
  const grid = document.querySelector('.image-grid');
  const r = grid.getBoundingClientRect();
  return {Left: r.left, Top: r.top, Right: r.right, Bottom: r.bottom};
})()`, &b),
	); err != nil {
		t.Fatalf("gridBounds: %v", err)
	}
	return b.Left, b.Top, b.Right, b.Bottom
}

func cardCenterXY(t *testing.T, ctx context.Context, name string) (int, int) {
	t.Helper()
	type point struct{ X, Y float64 }
	var p point
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card[data-path]');
  for (const c of cards) {
    if (c.dataset.path.endsWith('/' + %q)) {
      const r = c.getBoundingClientRect();
      return {X: r.left + r.width/2, Y: r.top + r.height/2};
    }
  }
  return null;
})()`, name)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &p)); err != nil {
		t.Fatalf("cardCenterXY: %v", err)
	}
	return int(p.X), int(p.Y)
}

// singleCardRect returns a rectangle that strictly contains the named card
// (with a small margin) — useful for Ctrl+drag tests that need exactly one
// card intersected.
func singleCardRect(t *testing.T, ctx context.Context, name string) (int, int, int, int) {
	t.Helper()
	type box struct{ X1, Y1, X2, Y2 float64 }
	var b box
	js := fmt.Sprintf(`(() => {
  const cards = document.querySelectorAll('.thumb-card[data-path]');
  for (const c of cards) {
    if (c.dataset.path.endsWith('/' + %q)) {
      const r = c.getBoundingClientRect();
      return {X1: r.left + 5, Y1: r.top + 5, X2: r.right - 5, Y2: r.bottom - 5};
    }
  }
  return null;
})()`, name)
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &b)); err != nil {
		t.Fatalf("singleCardRect: %v", err)
	}
	return int(b.X1), int(b.Y1), int(b.X2), int(b.Y2)
}

// ensure clickEntryCheckbox uses strings (suppress lint about unused import in
// this file by referencing strings here when needed).
var _ = strings.Contains
