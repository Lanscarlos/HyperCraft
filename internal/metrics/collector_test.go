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

// The first host sample has no interval behind it, so it must not turn a
// since-boot counter into a transfer rate — on a machine with any uptime that
// would draw a spike of hundreds of megabytes a second at the left of the chart.
func TestFirstNetworkSampleHasNoRate(t *testing.T) {
	collector := newTestCollector(t)
	ctx := context.Background()

	collector.collectHost(ctx, time.Now())
	first := collector.HostSeries()[0]
	if first.NetRecvPerSec != 0 || first.NetSentPerSec != 0 {
		t.Errorf("the first sample has no baseline, expected 0, got %+v", first)
	}
	if usage := collector.Net(); usage.RecvBytes != 0 || usage.SentBytes != 0 {
		t.Errorf("nothing has been counted yet, got %+v", usage)
	}

	collector.collectHost(ctx, time.Now().Add(time.Second))
	second := collector.HostSeries()[1]
	if second.NetRecvPerSec < 0 || second.NetSentPerSec < 0 {
		t.Errorf("a rate cannot be negative, got %+v", second)
	}
	for _, name := range collector.Net().Interfaces {
		if collector.isLoopback(name) {
			t.Errorf("loopback interface %q is being counted as network traffic", name)
		}
	}
}

// A bridge and the veth plugged into it carry the same bytes the physical card
// carries. Counting all three reports three times the traffic — on a machine
// running Docker, which is most of them.
func TestBridgesAndTunnelsAreNotCounted(t *testing.T) {
	for _, name := range []string{"docker0", "veth1a2b3c", "br-9f8e", "virbr0", "tun0", "wg0", "ifb0"} {
		if !isVirtual(name) {
			t.Errorf("%q should not be counted as a physical interface", name)
		}
	}
	for _, name := range []string{"eth0", "enp3s0", "wlan0", "ens5", "venet0", "em1"} {
		if isVirtual(name) {
			t.Errorf("%q is a real interface and must be counted", name)
		}
	}
}

// An interface that is reset or unplugged reports a counter lower than the
// last one. That is not traffic, and unsigned arithmetic would turn it into
// sixteen exabytes of it.
func TestNetworkCounterResetReadsAsZero(t *testing.T) {
	if got := since(5, 9); got != 0 {
		t.Errorf("since(5, 9) = %d, want 0", got)
	}
	if got := since(9, 5); got != 4 {
		t.Errorf("since(9, 5) = %d, want 4", got)
	}
}

// Without the flags — a container with a restricted /proc — the conventional
// names still have to be recognised, or the panel reports a machine talking to
// itself as network traffic.
func TestLoopbackIsExcludedByNameWhenFlagsAreMissing(t *testing.T) {
	collector := newTestCollector(t)
	collector.loopback = nil

	for _, name := range []string{"lo", "lo0", "Loopback Pseudo-Interface 1"} {
		if !collector.isLoopback(name) {
			t.Errorf("%q should be treated as loopback", name)
		}
	}
	if collector.isLoopback("eth0") {
		t.Error("eth0 is not loopback")
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
