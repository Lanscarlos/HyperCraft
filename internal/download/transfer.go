package download

import (
	"context"
	"crypto/md5"  //nolint:gosec // see newHash
	"crypto/sha1" //nolint:gosec // see newHash
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

// Digest is a checksum an upstream published, named by the algorithm it used.
//
// Nothing here gets to choose the algorithm — sha256 from PaperMC, Adoptium and
// MongoDB, sha512 from Modrinth, sha1 from Maven, md5 from Oracle — so all of
// them are accepted for what they are worth. Refusing the weak ones would not
// make those downloads safer; it would make them unchecked.
//
// Modrinth's sha1 is the one exception, and it is the caller's to make: it
// publishes both sha512 and sha1, so the plugin package asks for the stronger
// and never offers the weaker.
type Digest struct {
	// Algo is one of sha256, sha512, sha1, md5. An unknown one is treated as no
	// digest at all rather than as a failure: it means this build of the panel
	// does not know how to check what upstream published, which is the same
	// position as upstream publishing nothing.
	Algo  string
	Value string
}

// algoName is how an algorithm is written for a person reading an error. The
// wire names are lower case and unhyphenated; nobody writes them that way.
func algoName(algo string) string {
	switch algo {
	case "sha256":
		return "SHA-256"
	case "sha512":
		return "SHA-512"
	case "sha1":
		return "SHA-1"
	case "md5":
		return "MD5"
	}
	return algo
}

// ok reports whether there is something to check against.
func (d Digest) ok() bool { return d.Algo != "" && d.Value != "" && newHash(d.Algo) != nil }

// newHash is the hash upstream published a checksum with.
func newHash(algo string) hash.Hash {
	switch algo {
	case "sha256":
		return sha256.New()
	case "sha512":
		return sha512.New()
	case "sha1":
		return sha1.New() //nolint:gosec // upstream publishes nothing stronger
	case "md5":
		return md5.New() //nolint:gosec // ditto
	}
	return nil
}

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
func transfer(ctx context.Context, q *Queue, e *entry, r Request, temp string, pub *Progress) (string, error) {
	if r.Attempts == nil {
		// A caller bug, but one that must not take the daemon with it: this
		// runs on a worker goroutine inside the process that holds every server,
		// and an unrecovered panic here stops the whole fleet. Failing the job
		// says the same thing and says it on the row.
		return "", errors.New("这个下载没有可用的来源")
	}
	attempts, err := r.Attempts(ctx, pub)
	if err != nil {
		return "", err
	}
	// Re-read after Attempts, not before: resolving the download is where most
	// shelves learn the digest, and Describe writes it onto the entry. The copy
	// handed in was taken at Submit, when there was nothing to know.
	q.mu.Lock()
	r = e.req
	q.mu.Unlock()
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

	verifiable := r.Digest.ok()
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
	// A second hash only when there is a published one to compare against and
	// it is not the one already being computed, because hashing 200 MB twice
	// for nothing is a cost every download would pay.
	var published hash.Hash
	if verifiable && r.Digest.Algo != "sha256" {
		published = newHash(r.Digest.Algo)
		writers = append(writers, published)
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
	return sum, verifyDigest(r, sum, published, written)
}

// verifyDigest checks the bytes against whatever digest r published. sum is the
// SHA-256 of what arrived, which is always computed; published is the hash of
// the algorithm upstream actually used, non-nil only when that was something
// else.
func verifyDigest(r Request, sum string, published hash.Hash, written int64) error {
	if !r.Digest.ok() {
		return nil
	}
	algo, got := "SHA-256", sum
	if published != nil {
		algo, got = algoName(r.Digest.Algo), hex.EncodeToString(published.Sum(nil))
	}
	if strings.EqualFold(got, r.Digest.Value) {
		return nil
	}
	want := r.Digest.Value
	// A short body is the one checksum failure with an obvious cause, and
	// "the connection dropped, run it again" is very different advice from
	// "this source is serving the wrong file".
	if r.Total > 0 && written < r.Total {
		return fmt.Errorf("%w: 下载中断，只收到 %d 字节，应为 %d", ErrChecksum, written, r.Total)
	}
	return fmt.Errorf("%w: %s 不符，算出 %s，应为 %s", ErrChecksum, algo, got, strings.ToLower(want))
}

// Description is what a caller learns only once it has resolved the download.
//
// Every field is optional: an empty string or a zero leaves what is already on
// the job, so a caller can fill in the file name without blanking the title it
// set at submit time. Meta is merged key by key for the same reason.
type Description struct {
	Title    string
	Subtitle string
	FileName string
	Total    int64
	Meta     map[string]string
	// Digest is what the origin's metadata published for this file.
	//
	// They are here and not only on the Request because most shelves do not
	// know them at submit time: the build is resolved on the worker, minutes
	// after the button was pressed, and the digest comes with it. A Request
	// that carries one keeps it; this fills in the rest.
	//
	// Getting this wrong is silent. Before it existed, a core download resolved
	// its digest inside Attempts, wrote it to a copy of the Request nobody read
	// again, and verified nothing at all — while the mirror it had just gained
	// was safe to use *only* because of that check.
	Digest Digest
}

// Describe fills in what Submit could not know.
//
// A job is queued before anything has talked to the upstream, so at that point
// the panel often knows only which plugin was asked for — not which release
// that resolves to, which jar of it, or how big. Without this the row would
// still say what it said when the button was pressed.
func (p *Progress) Describe(d Description) {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	job := p.entry.pub
	if d.Title != "" {
		job.Title = d.Title
	}
	if d.Subtitle != "" {
		job.Subtitle = d.Subtitle
	}
	if d.FileName != "" {
		job.FileName = d.FileName
	}
	if d.Total > 0 {
		job.Total = d.Total
		// Also onto the request, which is what transfer reads: Total is not just
		// the bar's denominator, it is the size gate when no digest was
		// published and the difference between reporting a short body as "the
		// connection dropped" and as "this source is serving the wrong file".
		p.entry.req.Total = d.Total
	}
	for k, v := range d.Meta {
		if job.Meta == nil {
			job.Meta = map[string]string{}
		}
		job.Meta[k] = v
	}
	if d.Digest.Value != "" {
		p.entry.req.Digest = d.Digest
	}
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
