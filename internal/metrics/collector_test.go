package metrics

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func newTestCollector(t *testing.T) *Collector {
	t.Helper()
	return New(50*time.Millisecond, 5*time.Second, t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHostInfoIsPopulated(t *testing.T) {
	info := newTestCollector(t).Info()

	if info.CPUCores <= 0 {
		t.Errorf("CPUCores = %d, want a positive count", info.CPUCores)
	}
	if info.MemoryTotal == 0 {
		t.Error("MemoryTotal is zero")
	}
}

// The collector is only useful if it reads real numbers off a real process.
// A busy loop gives it something unambiguous to measure.
func TestSamplesABusyProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the busy-loop helper is a POSIX shell script")
	}

	cmd := exec.Command("/bin/sh", "-c", "while true; do :; done")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start busy process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	collector := newTestCollector(t)
	target := Target{ID: "busy", PID: cmd.Process.Pid}
	ctx := context.Background()

	// The first sample establishes the CPU baseline; a percentage needs two.
	first := collector.sampleTarget(ctx, target, time.Now())
	if first.CPUPercent != 0 {
		t.Errorf("the first sample has no baseline, expected 0%%, got %.1f", first.CPUPercent)
	}
	if first.MemoryBytes == 0 {
		t.Error("memory should be readable from the very first sample")
	}

	time.Sleep(300 * time.Millisecond)
	second := collector.sampleTarget(ctx, target, time.Now())

	if second.CPUPercent < 10 {
		t.Errorf("a busy loop should report meaningful CPU, got %.1f%%", second.CPUPercent)
	}
	if second.MemoryBytes == 0 {
		t.Error("MemoryBytes is zero for a live process")
	}
	if second.Processes < 1 {
		t.Errorf("Processes = %d", second.Processes)
	}
}

// Restarting a server reuses the instance ID with a new PID. The stale
// baseline must be discarded, or the first sample after a restart reports a
// nonsense percentage computed against a different process.
func TestCPUBaselineResetsWhenThePIDChanges(t *testing.T) {
	collector := newTestCollector(t)
	ctx := context.Background()

	collector.previous["srv"] = cpuSnapshot{
		pid:     1234,
		seconds: 10_000, // a long-running process's worth of CPU time
		at:      time.Now().Add(-time.Second),
	}

	sample := collector.sampleTarget(ctx, Target{ID: "srv", PID: 4321}, time.Now())
	if sample.CPUPercent != 0 {
		t.Errorf("expected 0%% after a PID change, got %.1f", sample.CPUPercent)
	}
}

func TestStoppedInstanceRecordsZero(t *testing.T) {
	collector := newTestCollector(t)

	collector.previous["srv"] = cpuSnapshot{pid: 42, seconds: 5, at: time.Now()}
	sample := collector.sampleTarget(context.Background(), Target{ID: "srv", PID: 0}, time.Now())

	if sample.CPUPercent != 0 || sample.MemoryBytes != 0 {
		t.Errorf("a stopped instance should read zero, got %+v", sample)
	}
	if _, ok := collector.previous["srv"]; ok {
		t.Error("the CPU baseline should be cleared when the process is gone")
	}
}

func TestSeriesIsCappedAndOrdered(t *testing.T) {
	collector := New(time.Second, 3*time.Second, t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	base := time.Now()
	for i := range 10 {
		collector.append("srv", Sample{Time: base.Add(time.Duration(i) * time.Second), MemoryBytes: uint64(i)})
	}

	series := collector.Series("srv")
	if len(series) != 3 {
		t.Fatalf("expected the series capped at 3, got %d", len(series))
	}
	// The oldest entries are the ones dropped.
	for i, sample := range series {
		if want := uint64(7 + i); sample.MemoryBytes != want {
			t.Errorf("series[%d].MemoryBytes = %d, want %d", i, sample.MemoryBytes, want)
		}
	}
}

// Deleting an instance must not leave its history behind for the next
// instance that happens to reuse the ID.
func TestCollectForgetsRemovedInstances(t *testing.T) {
	collector := newTestCollector(t)
	ctx := context.Background()

	collector.collect(ctx, []Target{{ID: "gone", PID: 0}})
	if len(collector.Series("gone")) == 0 {
		t.Fatal("expected a sample for the tracked instance")
	}

	collector.collect(ctx, nil)
	if len(collector.Series("gone")) != 0 {
		t.Error("history for a removed instance was retained")
	}
}

func TestRunStopsWithTheContext(t *testing.T) {
	collector := newTestCollector(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		collector.Run(ctx, func() []Target { return nil })
		close(done)
	}()

	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}

	if len(collector.HostSeries()) == 0 {
		t.Error("expected at least one host sample from the ticker")
	}
}
