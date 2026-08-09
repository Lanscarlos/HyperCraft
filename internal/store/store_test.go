package store

import (
	"os"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/config"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(config.NewPaths(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestResumeRoundTrip(t *testing.T) {
	st := newTestStore(t)

	if err := st.SaveResume([]string{"abc", "def"}); err != nil {
		t.Fatalf("SaveResume: %v", err)
	}
	got, err := st.TakeResume()
	if err != nil {
		t.Fatalf("TakeResume: %v", err)
	}
	if len(got) != 2 || got[0] != "abc" || got[1] != "def" {
		t.Fatalf("TakeResume = %v, want [abc def]", got)
	}

	// Taking consumes the list: a server that fails to come back must not be
	// retried on every subsequent boot.
	again, err := st.TakeResume()
	if err != nil {
		t.Fatalf("second TakeResume: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second TakeResume = %v, want empty", again)
	}
	if _, err := os.Stat(st.paths.ResumeFile()); !os.IsNotExist(err) {
		t.Errorf("resume file still on disk after being taken (err=%v)", err)
	}
}

func TestTakeResumeWithNoFile(t *testing.T) {
	st := newTestStore(t)

	// The ordinary case: the panel started without an update pending.
	got, err := st.TakeResume()
	if err != nil {
		t.Fatalf("TakeResume on a fresh data dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("TakeResume = %v, want empty", got)
	}
}

func TestSaveResumeEmptyClearsPreviousList(t *testing.T) {
	st := newTestStore(t)

	if err := st.SaveResume([]string{"abc"}); err != nil {
		t.Fatal(err)
	}
	// An update started with nothing running must not resurrect the previous
	// update's servers.
	if err := st.SaveResume(nil); err != nil {
		t.Fatal(err)
	}
	got, err := st.TakeResume()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("TakeResume = %v, want empty", got)
	}
}
