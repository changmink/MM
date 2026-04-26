package handler

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	stickyViewportW = 1280
	stickyViewportH = 720
	stickyHeaderH   = 57 // matches CSS variable --header-h in style.css
)

// startStickyServer wires the production handler with the supplied data dir.
// The web/ assets are served from the real repo directory so the test sees
// the same HTML/CSS/JS the user would.
func startStickyServer(t *testing.T, dataDir string) *httptest.Server {
	t.Helper()
	webDir, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatalf("resolve web dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil {
		t.Fatalf("web dir missing index.html (%s): %v", webDir, err)
	}
	mux := http.NewServeMux()
	Register(mux, dataDir, webDir, nil)
	return httptest.NewServer(mux)
}

// newStickyChromeCtx allocates a headless Chrome context. Skips the test when
// no Chrome/Chromium is installed (CI without browser, fresh dev box, etc.).
func newStickyChromeCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(stickyViewportW, stickyViewportH),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithLogf(t.Logf))
	cancel := func() {
		cancelCtx()
		cancelAlloc()
	}
	return ctx, cancel
}

// rect captures the viewport-relative bounding box for a single element.
type rect struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Height float64 `json:"height"`
}

func evalRect(selector string) string {
	return fmt.Sprintf(`(() => {
  const el = document.querySelector(%q);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  return {top: r.top, bottom: r.bottom, height: r.height};
})()`, selector)
}

// pageScript scrolls the page by the given amount, waits for the JS sync
// callback (rAF + microtask flush) and returns once layout has settled.
const pageScript = `(async (y) => {
  window.scrollTo(0, y);
  await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
})`

// TestSidebarSticky_ShortTree verifies that with a tree shorter than the
// viewport, the sidebar and upload zone both stay pinned beneath the header
// once the page scrolls. AC1 + AC3 (short tree branch).
func TestSidebarSticky_ShortTree(t *testing.T) {
	dataDir := t.TempDir()
	// Few folders so sidebar < viewport; many files so the page itself can scroll.
	for i := 0; i < 3; i++ {
		if err := os.Mkdir(filepath.Join(dataDir, fmt.Sprintf("folder_%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		path := filepath.Join(dataDir, fmt.Sprintf("file_%02d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	server := startStickyServer(t, dataDir)
	defer server.Close()

	ctx, cancel := newStickyChromeCtx(t)
	defer cancel()
	runCtx, cancelRun := context.WithTimeout(ctx, 30*time.Second)
	defer cancelRun()

	var sidebar, upload rect
	var winH float64
	err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible(`#tree-root .tree-node`, chromedp.ByQuery),
		chromedp.Sleep(100*time.Millisecond),
		// Scroll past the upload zone's top margin so sticky engages but stay
		// well clear of the body bottom (containing-block release).
		chromedp.Evaluate(pageScript+"(400)", nil, awaitPromise),
		chromedp.Evaluate(`window.innerHeight`, &winH),
		chromedp.Evaluate(evalRect("#sidebar"), &sidebar),
		chromedp.Evaluate(evalRect("#upload-zone"), &upload),
	)
	if err != nil {
		t.Fatalf("chromedp run: %v", err)
	}

	if math.Abs(sidebar.Top-stickyHeaderH) > 1 {
		t.Errorf("sidebar.top = %.1f, want ~%d (pinned beneath header)", sidebar.Top, stickyHeaderH)
	}
	if sidebar.Bottom > winH+1 {
		t.Errorf("sidebar.bottom = %.1f exceeds viewport %.1f", sidebar.Bottom, winH)
	}
	if math.Abs(upload.Top-stickyHeaderH) > 1 {
		t.Errorf("upload.top = %.1f, want ~%d (pinned beneath header)", upload.Top, stickyHeaderH)
	}
}

// TestSidebarSticky_LongTree verifies that with a tree taller than the viewport
// the user can reach the last node by page-scrolling alone (no inner overflow
// scrollbar). AC2 + AC3 (long tree branch).
func TestSidebarSticky_LongTree(t *testing.T) {
	dataDir := t.TempDir()
	// 50 folders >> viewport. Each .tree-node-row is ~32px so the rendered
	// sidebar comfortably exceeds the 720px viewport.
	for i := 0; i < 50; i++ {
		if err := os.Mkdir(filepath.Join(dataDir, fmt.Sprintf("folder_%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	server := startStickyServer(t, dataDir)
	defer server.Close()

	ctx, cancel := newStickyChromeCtx(t)
	defer cancel()
	runCtx, cancelRun := context.WithTimeout(ctx, 30*time.Second)
	defer cancelRun()

	var firstVisibleAtTop, lastVisibleAtBottom bool
	var sidebarHeight, winH float64
	err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(stickyViewportW, stickyViewportH),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible(`#tree-root .tree-node`, chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
		chromedp.Evaluate(`window.innerHeight`, &winH),
		chromedp.Evaluate(`document.getElementById('sidebar').getBoundingClientRect().height`, &sidebarHeight),
		// At the top of the page, the FIRST tree node must be visible.
		chromedp.Evaluate(`(() => {
  const nodes = document.querySelectorAll('#tree-root > .tree-node');
  if (!nodes.length) return false;
  const r = nodes[0].getBoundingClientRect();
  return r.top >= 0 && r.bottom <= window.innerHeight;
})()`, &firstVisibleAtTop),
		// Scroll all the way; the LAST tree node must be visible afterwards.
		chromedp.Evaluate(pageScript+"(document.body.scrollHeight)", nil, awaitPromise),
		chromedp.Sleep(150*time.Millisecond),
		chromedp.Evaluate(`(() => {
  const nodes = document.querySelectorAll('#tree-root > .tree-node');
  if (!nodes.length) return false;
  const last = nodes[nodes.length - 1];
  const r = last.getBoundingClientRect();
  return r.top >= 0 && r.bottom <= window.innerHeight;
})()`, &lastVisibleAtBottom),
	)
	if err != nil {
		t.Fatalf("chromedp run: %v", err)
	}

	if sidebarHeight <= winH {
		t.Fatalf("sidebar height = %.1f, expected > viewport %.1f (test data not tall enough)", sidebarHeight, winH)
	}
	if !firstVisibleAtTop {
		t.Errorf("first tree node not visible at page top — sidebar may be pushed off-screen")
	}
	if !lastVisibleAtBottom {
		t.Errorf("last tree node not visible after full scroll — sticky-until-bottom failed")
	}
}

// awaitPromise lets chromedp.Evaluate await async expressions; without it the
// scroll-and-rAF script returns before layout has settled.
func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}
