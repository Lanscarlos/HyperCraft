package download

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

// ErrChecksum is returned when the downloaded bytes do not match the digest
// the request published.
var ErrChecksum = errors.New("checksum mismatch")

// maxUnknownSize caps a download whose size the request did not declare, and
// also whatever size *is* declared once a checksum makes that declared size
// advisory rather than load-bearing (see transfer).
const maxUnknownSize = 512 << 20

// progressWriter reports the running total as bytes go past.
type progressWriter struct {
	to      io.Writer
	report  func(int64)
	written int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.to.Write(p)
	w.written += int64(n)
	w.report(w.written)
	return n, err
}

// transfer walks r's Attempts, most preferred first, streaming the body of
// whichever one opens to temp while verifying it against whichever digest r
// published. It records which attempt actually answered into the job's Route,
// and returns the SHA-256 of what arrived — the identity every shelf records
// its downloads by, whether or not the request published a digest to check.
func transfer(ctx context.Context, q *Queue, e *entry, r Request, temp string) (string, error) {
	attempts, err := r.Attempts(ctx)
	if err != nil {
		return "", err
	}
	if len(attempts) == 0 {
		return "", errors.New("no attempts to try")
	}

	var body io.ReadCloser
	var route string
	var lastErr error
	for _, attempt := range attempts {
		opened, openErr := attempt.Open(ctx)
		if openErr == nil && opened != nil {
			body, route = opened, attempt.Route
			break
		}
		// An Attempt that hands back a body *and* an error is a caller bug, but
		// leaking the connection on top of it helps nobody.
		if opened != nil {
			opened.Close()
		}
		lastErr = openErr
		// A cancelled job must not march down the fallback list pretending a
		// mirror was at fault — the same rule the original Fetch enforced.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if body == nil {
		if lastErr == nil {
			// Every attempt opened nothing without saying why. Also a caller
			// bug, and one that would otherwise read as a silent success.
			lastErr = errors.New("no attempt produced a body")
		}
		return "", lastErr
	}
	defer body.Close()

	q.mu.Lock()
	e.pub.Route = route
	q.mu.Unlock()

	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}

	verifiable := r.SHA256 != "" || r.SHA512 != ""
	// sized marks the case where the declared size is the only check there
	// is — every GitHub release asset lands here — and so has to be exact.
	//
	// Where the request does publish a digest it is both the stronger check
	// and the more reliable one: Azul's Java metadata under-reports a package
	// by 9 bytes while publishing the right SHA-256 for it, which turned a
	// perfectly good install into "exceeds the declared size" until the gate
	// moved (see javaruntime.Installer.download). So with a digest the size
	// drives the progress bar, and the cap falls back to the same ceiling an
	// undeclared size gets — there to bound the disk a runaway redirect can
	// eat, not to verify anything.
	sized := r.Total > 0 && !verifiable
	limit := int64(maxUnknownSize)
	if sized {
		limit = r.Total
	}

	// The SHA-256 is always computed, whatever the request published: it is
	// the identity a library records things by and a fleet is reconciled
	// against, not the proof. A second digest is only computed when there is
	// a published one to compare it to, because hashing 30 MB twice for
	// nothing is a cost every download would pay.
	digest := sha256.New()
	writers := []io.Writer{file, digest}
	var wide hash.Hash
	if r.SHA512 != "" {
		wide = sha512.New()
		writers = append(writers, wide)
	}
	progress := &progressWriter{
		to: io.MultiWriter(writers...),
		report: func(n int64) {
			q.mu.Lock()
			e.pub.Downloaded = n
			q.mu.Unlock()
		},
	}

	// One byte past the limit, so an exactly-sized body still succeeds while an
	// oversized one is caught instead of silently truncated.
	written, copyErr := io.Copy(progress, io.LimitReader(body, limit+1))
	// A close error on the last flush is the difference between a whole file and
	// a truncated one, so it is checked rather than deferred away.
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	switch {
	case sized && written > limit:
		return "", fmt.Errorf("下载的内容比声明的 %d 字节还多", limit)
	case sized && written != r.Total:
		return "", fmt.Errorf("收到 %d 字节，应为 %d", written, r.Total)
	case written > limit:
		return "", fmt.Errorf("下载超过 %d 字节的上限，已中止", limit)
	}

	// The digest a request published, when it published one, is finally
	// compared rather than only recorded.
	sum := hex.EncodeToString(digest.Sum(nil))
	return sum, verifyDigest(r, sum, wide, written)
}

// verifyDigest checks the bytes against whichever digest r published. sum is
// the SHA-256 of what arrived and wide the SHA-512 of it, non-nil only when
// there was a published SHA-512 to check.
func verifyDigest(r Request, sum string, wide hash.Hash, written int64) error {
	algo, got, want := "SHA-256", sum, r.SHA256
	if wide != nil {
		algo, got, want = "SHA-512", hex.EncodeToString(wide.Sum(nil)), r.SHA512
	}
	if want == "" || strings.EqualFold(got, want) {
		return nil
	}
	// A short body is the one checksum failure with an obvious cause, and
	// "the connection dropped, run it again" is very different advice from
	// "this source is serving the wrong file".
	if r.Total > 0 && written < r.Total {
		return fmt.Errorf("%w: 下载中断，只收到 %d 字节，应为 %d", ErrChecksum, written, r.Total)
	}
	return fmt.Errorf("%w: %s 不符，算出 %s，应为 %s", ErrChecksum, algo, got, strings.ToLower(want))
}

// Extracting moves the job into its install phase. Called by the Install hook
// before it starts unpacking: a 200 MB JDK spends real time there and a bar
// that sat at 100% would read as a hang.
func (p *Progress) Extracting() {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	p.entry.pub.State = StateExtracting
	// The download half's numbers would otherwise be read as the unpack's.
	p.entry.pub.Downloaded, p.entry.pub.Total = 0, 0
}

// Set reports how far the install phase has got. Zero total means "working,
// length unknown", which is what an archive with no entry count looks like.
func (p *Progress) Set(downloaded, total int64) {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	p.entry.pub.Downloaded, p.entry.pub.Total = downloaded, total
}
