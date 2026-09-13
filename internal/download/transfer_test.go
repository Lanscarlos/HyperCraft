package download

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func runOne(t *testing.T, r Request) Job {
	t.Helper()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	if _, err := q.Submit(r); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })
	jobs := q.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	return jobs[0]
}

func serve(body string) func(context.Context, *Progress) ([]Attempt, error) {
	return func(context.Context, *Progress) ([]Attempt, error) {
		return []Attempt{{Route: "test", Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		}}}, nil
	}
}

// Azul's metadata under-reports a package by 9 bytes while publishing the right
// SHA-256 for it. A size check that outranked the digest turned a perfectly
// good JDK into a failed install.
func TestMisdeclaredSizeIsAcceptedWhenTheChecksumMatches(t *testing.T) {
	body := "the actual bytes"
	job := runOne(t, Request{
		Kind: KindJava, Title: "jdk", DedupeKey: "jdk",
		Total: int64(len(body)) + 9, Digest: Digest{Algo: "sha256", Value: sum(body)},
		Attempts: serve(body),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "rt-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
}

func TestPublishedChecksumMismatchIsRejected(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Digest:   Digest{Algo: "sha256", Value: sum("what upstream promised")},
		Attempts: serve("something else entirely"),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	if !strings.Contains(job.Error, "SHA-256") {
		t.Fatalf("error = %q, want it to name SHA-256", job.Error)
	}
}

// Without a digest the declared size is the only check there is, so it has to
// be exact — every GitHub release asset lands here.
func TestMisdeclaredSizeIsRejectedWithoutAChecksum(t *testing.T) {
	body := "four"
	job := runOne(t, Request{
		Kind: KindPlugin, Title: "jar", DedupeKey: "jar",
		Total: int64(len(body)) + 10, Digest: Digest{},
		Attempts: serve(body),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
}

// "The connection dropped, run it again" is very different advice from "this
// source is serving the wrong file", so a short body says which it was.
func TestATruncatedBodyIsReportedAsTruncated(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Total: 1024, Digest: Digest{Algo: "sha256", Value: sum("full body")},
		Attempts: serve("short"),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "", nil },
	})
	if !strings.Contains(job.Error, "下载中断") {
		t.Fatalf("error = %q, want it to say the transfer was cut short", job.Error)
	}
}

// A route that is down costs a retry rather than the install. This is why
// every route list ends at the origin.
func TestAFailingRouteFallsThroughToTheNextOne(t *testing.T) {
	body := "served by the second"
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Digest: Digest{Algo: "sha256", Value: sum(body)},
		Attempts: func(context.Context, *Progress) ([]Attempt, error) {
			return []Attempt{
				{Route: "mirror", Open: func(context.Context) (io.ReadCloser, error) {
					return nil, errors.New("mirror is down")
				}},
				{Route: "official", Open: func(context.Context) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(body)), nil
				}},
			}, nil
		},
		Install: func(context.Context, string, string, *Progress) (string, error) { return "core-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
	if job.Route != "official" {
		t.Fatalf("route = %q, want the one that actually answered", job.Route)
	}
}

// The temp file is what Install is handed, and a failed job must not leave one
// behind that looks installable.
func TestAFailedDownloadLeavesNoFileBehind(t *testing.T) {
	dir := t.TempDir()
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		TempDir:  dir,
		Digest:   Digest{Algo: "sha256", Value: sum("promised")},
		Attempts: serve("delivered"),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("temp dir still holds %v", left)
	}
	_ = filepath.Join
}

func sum512(body string) string {
	h := sha512.Sum512([]byte(body))
	return hex.EncodeToString(h[:])
}

// Modrinth publishes sha512 and no sha256, so this is the only check a plugin
// coming from there ever gets. Before the kernel carried SHA512 at all, such a
// download was verified against nothing and still reported as done.
func TestASHA512OnlyRequestIsVerifiedAgainstIt(t *testing.T) {
	body := "what modrinth promised"
	job := runOne(t, Request{
		Kind: KindPlugin, Title: "jar", DedupeKey: "jar",
		Total: int64(len(body)), Digest: Digest{Algo: "sha512", Value: sum512(body)},
		Attempts: serve(body),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "p-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
}

func TestASHA512MismatchIsRejected(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindPlugin, Title: "jar", DedupeKey: "jar",
		Digest:   Digest{Algo: "sha512", Value: sum512("what modrinth promised")},
		Attempts: serve("something else entirely"),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	if !strings.Contains(job.Error, "SHA-512") {
		t.Fatalf("error = %q, want it to name SHA-512", job.Error)
	}
}

// The size gate keys off "neither digest published", not off SHA-256 alone.
// Written the other way this passes for GitHub and silently turns every
// Modrinth download whose declared size is off by a byte into a failure —
// the same class of bug the Azul comment in transfer.go records.
func TestAMisdeclaredSizeIsAcceptedWhenOnlySHA512IsPublished(t *testing.T) {
	body := "the actual bytes"
	job := runOne(t, Request{
		Kind: KindPlugin, Title: "jar", DedupeKey: "jar",
		Total: int64(len(body)) + 9, Digest: Digest{Algo: "sha512", Value: sum512(body)},
		Attempts: serve(body),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "p-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
}

// Every shelf records its downloads by this digest. The transfer already has
// it; making Install re-hash a 200 MB archive to learn it would be waste that
// only looks free.
func TestInstallIsHandedTheDigestOfWhatArrived(t *testing.T) {
	body := "bytes with no published digest at all"
	var got string
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Total:    int64(len(body)),
		Attempts: serve(body),
		Install: func(_ context.Context, _, sha string, _ *Progress) (string, error) {
			got = sha
			return "core-1", nil
		},
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
	if got != sum(body) {
		t.Fatalf("Install got digest %q, want %q", got, sum(body))
	}
}

// Most shelves do not know the digest at submit time: the build is resolved on
// the worker, and the digest arrives with it. If Describe's digest were dropped
// — written to a copy of the Request nobody reads again — the download would
// verify nothing, silently, while the mirror it was routed through is safe to
// use only because of that check.
func TestADigestLearnedWhileResolvingIsStillChecked(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Attempts: func(_ context.Context, pub *Progress) ([]Attempt, error) {
			pub.Describe(Description{Digest: Digest{Algo: "sha256", Value: sum("what upstream promised")}})
			return []Attempt{{Route: "mirror", Open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("what the mirror served")), nil
			}}}, nil
		},
		Install: func(context.Context, string, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed — the mirror served bytes the origin did not promise", job.State)
	}
	if !strings.Contains(job.Error, "SHA-256") {
		t.Fatalf("error = %q, want it to name the checksum", job.Error)
	}
}

// The same path, arriving correct.
func TestADigestLearnedWhileResolvingPassesWhenItMatches(t *testing.T) {
	body := "what upstream promised"
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Attempts: func(_ context.Context, pub *Progress) ([]Attempt, error) {
			pub.Describe(Description{Digest: Digest{Algo: "sha256", Value: sum(body)}, FileName: "paper.jar", Total: int64(len(body))})
			return []Attempt{{Route: "mirror", Open: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(body)), nil
			}}}, nil
		},
		Install: func(context.Context, string, string, *Progress) (string, error) { return "core-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
	if job.FileName != "paper.jar" {
		t.Fatalf("FileName = %q, want what Describe set", job.FileName)
	}
}

// A shelf points TempDir at its own library, and a fresh panel has downloaded
// nothing — so that directory does not exist yet. The kernel owns the part file
// and therefore owns creating the directory under it.
func TestTheTempDirectoryIsCreatedIfItIsNotThereYet(t *testing.T) {
	body := "bytes"
	dir := filepath.Join(t.TempDir(), "not", "created", "yet")
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		TempDir:  dir,
		Total:    int64(len(body)),
		Attempts: serve(body),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "core-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
}

// Nothing here gets to choose the algorithm: sha256 from PaperMC, Adoptium and
// MongoDB, sha512 from Modrinth, sha1 from Maven for PostgreSQL, md5 from
// Oracle for MySQL. Refusing the weak ones would not make those downloads
// safer — it would make them unchecked, which is what the shared kernel very
// nearly did to two of the three database engines.
func TestEveryPublishedAlgorithmIsActuallyChecked(t *testing.T) {
	body := "what upstream promised"
	digests := map[string]func(string) string{
		"sha256": func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) },
		"sha512": func(s string) string { h := sha512.Sum512([]byte(s)); return hex.EncodeToString(h[:]) },
		"sha1":   func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) },
		"md5":    func(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) },
	}
	for algo, of := range digests {
		t.Run(algo+" accepts what matches", func(t *testing.T) {
			job := runOne(t, Request{
				Kind: KindDatabase, Title: algo, DedupeKey: algo,
				Digest:   Digest{Algo: algo, Value: of(body)},
				Attempts: serve(body),
				Install:  func(context.Context, string, string, *Progress) (string, error) { return "x", nil },
			})
			if job.State != StateDone {
				t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
			}
		})
		t.Run(algo+" rejects what does not", func(t *testing.T) {
			job := runOne(t, Request{
				Kind: KindDatabase, Title: algo, DedupeKey: algo,
				Digest:   Digest{Algo: algo, Value: of("something else")},
				Attempts: serve(body),
				Install:  func(context.Context, string, string, *Progress) (string, error) { return "", nil },
			})
			if job.State != StateFailed {
				t.Fatalf("state = %q, want failed", job.State)
			}
			if !strings.Contains(job.Error, algoName(algo)) {
				t.Fatalf("error = %q, want it to name %s", job.Error, algoName(algo))
			}
		})
	}
}

// An algorithm this build does not know is the same position as upstream
// publishing nothing — not a reason to refuse the download outright.
func TestAnUnknownAlgorithmIsTreatedAsNoDigest(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindDatabase, Title: "x", DedupeKey: "x",
		Digest:   Digest{Algo: "whirlpool", Value: "ffff"},
		Attempts: serve("bytes"),
		Install:  func(context.Context, string, string, *Progress) (string, error) { return "x", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
}

// A Request with no Attempts is a caller bug, but it must not take the daemon
// with it: this runs on a worker goroutine inside the process that holds every
// server, so an unrecovered panic there stops the whole fleet.
func TestARequestWithNoSourceFailsRatherThanPanicking(t *testing.T) {
	job := runOne(t, Request{Kind: KindCore, Title: "nothing", DedupeKey: "nothing"})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	if job.Error == "" {
		t.Fatal("the job failed without saying why")
	}
}
