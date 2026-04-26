package urlfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
)

// downloadOne fetches urlStr through client and writes the body to destPath
// (created with O_CREATE|O_EXCL — caller must guarantee destPath is unique
// within the materializeHLS temp directory). Two caps gate the transfer:
//
//   - perResourceMax > 0: rejects responses whose Content-Length exceeds the
//     cap (preflight) and aborts the read once the body grows past the cap
//     (runtime). 0 disables this check.
//   - remainingBytes: shared cumulative counter for the entire HLS materialize
//     session. Every byte read is debited; once the counter would go negative
//     the download aborts.
//
// Cap breaches return errHLSTooLarge with any partial file removed. HTTP
// errors return fmt.Errorf("http %d", status); dial / TLS / private-network
// errors propagate as-is from client.Do (the protected client surfaces
// errPrivateNetwork via *url.Error, so errors.Is(err, errPrivateNetwork)
// works at the call site).
func downloadOne(
	ctx context.Context,
	client *http.Client,
	urlStr string,
	destPath string,
	perResourceMax int64,
	remainingBytes *atomic.Int64,
) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http %d", resp.StatusCode)
	}

	// Preflight Content-Length when the server provided one. Skipping when
	// ContentLength <= 0 (chunked / unknown) is fine — runtime cap will
	// catch oversize bodies.
	if cl := resp.ContentLength; cl > 0 {
		if perResourceMax > 0 && cl > perResourceMax {
			return 0, errHLSTooLarge
		}
		if cl > remainingBytes.Load() {
			return 0, errHLSTooLarge
		}
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}

	written, copyErr := copyWithCaps(f, resp.Body, perResourceMax, remainingBytes)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(destPath)
		return written, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		return written, closeErr
	}
	return written, nil
}

// copyWithCaps streams src into dst while enforcing a per-resource cap and a
// shared cumulative counter. Sequential by design — atomic.Int64 keeps it
// safe for any future parallel materializeHLS variant. On cap breach returns
// errHLSTooLarge after writing the bytes that fit; the caller removes the
// partial file.
func copyWithCaps(dst io.Writer, src io.Reader, perResourceMax int64, remaining *atomic.Int64) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if perResourceMax > 0 && written+int64(n) > perResourceMax {
				return written, errHLSTooLarge
			}
			// Check before debit so the counter cannot go negative — keeps
			// other in-flight reads' view of the counter accurate.
			if remaining.Load() < int64(n) {
				return written, errHLSTooLarge
			}
			remaining.Add(-int64(n))

			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}
