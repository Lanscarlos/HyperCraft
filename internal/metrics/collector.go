// Package metrics samples CPU and memory usage for the managed servers and
// for the host they run on.
//
// Sampling happens in the panel daemon on a fixed tick, not on request: the
// charts then show a real timeline that survives the browser being closed,
// and a page refresh gets the whole retained window immediately instead of
// starting from an empty graph.
package metrics

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Sample is one measurement of a server process tree.
type Sample struct {
	Time time.Time `json:"time"`
	// CPUPercent is top-style: 100 means one core fully busy, so a server
	// pinning its main thread reads as ~100 no matter how many cores the box
	// has. Normalising by core count would make a saturated Minecraft server
	// look idle on a 32-core machine.
	CPUPercent float64 `json:"cpuPercent"`
	// MemoryBytes is resident set size, summed over the process tree.
	MemoryBytes uint64 `json:"memoryBytes"`
	// Processes counts the tree members that were measured.
	Processes int `json:"processes"`
}

// HostSample is one measurement of the machine as a whole.
type HostSample struct {
	Time          time.Time `json:"time"`
	CPUPercent    float64   `json:"cpuPercent"` // 0-100 across all cores
	MemoryUsed    uint64    `json:"memoryUsed"`
	MemoryPercent float64   `json:"memoryPercent"`
	// NetRecvPerSec and NetSentPerSec are bytes per second over the interval
	// that ended at Time, summed over every non-loopback interface. Rates
	// rather than the kernel's counters: those are monotonic totals since boot,
	// and a chart of one is a straight line at whatever the machine has done
	// since it was switched on. The first sample after start has no interval to
	// measure and reports zero.
	NetRecvPerSec float64 `json:"netRecvPerSec"`
	NetSentPerSec float64 `json:"netSentPerSec"`
}

// HostInfo is the machine description, gathered once at startup.
type HostInfo struct {
	Hostname    string `json:"hostname"`
	Platform    string `json:"platform"`
	CPUCores    int    `json:"cpuCores"`
	MemoryTotal uint64 `json:"memoryTotal"`
}

// DiskUsage reports free space where the instances live.
type DiskUsage struct {
	Path    string  `json:"path"`
	Total   uint64  `json:"total"`
	Free    uint64  `json:"free"`
	Percent float64 `json:"percent"`
}

// NetUsage is what the network has carried, as of the latest sample.
//
// The two totals are counted from the panel's own start rather than passed
// through from the kernel's since-boot counters: a number that starts at
// whatever a six-month uptime happens to have accumulated answers a question
// nobody asked. Since the panel started is a window the operator can reason
// about — it is the same window the charts cover.
type NetUsage struct {
	// Interfaces are the ones being counted: loopback, bridges and tunnels are
	// not among them (see readNet). Published rather than kept private because
	// "which card is this" is the first question a number like this raises.
	Interfaces []string `json:"interfaces"`
	RecvBytes  uint64   `json:"recvBytes"`
	SentBytes  uint64   `json:"sentBytes"`
	RecvPerSec float64  `json:"recvPerSec"`
	SentPerSec float64  `json:"sentPerSec"`
}

// Target is one thing to sample: an instance and the PID it currently runs as
// (0 when stopped).
type Target struct {
	ID  string
	PID int
}

// sampleTimeout bounds one collection pass. Reading /proc is normally
// instant, but a machine under heavy IO pressure can stall, and a stuck
// sampler must not wedge the ticker.
const sampleTimeout = 5 * time.Second

// Collector holds the rolling history for every instance plus the host.
type Collector struct {
	log      *slog.Logger
	interval time.Duration
	retain   int
	diskPath string

	mu       sync.RWMutex
	series   map[string][]Sample
	previous map[string]cpuSnapshot
	hostRing []HostSample
	info     HostInfo
	diskInfo DiskUsage
	netInfo  NetUsage
	netPrev  netSnapshot
	// loopback names, so a machine's own chatter with itself is not reported as
	// network traffic. Read once: interfaces come and go, loopback does not.
	loopback map[string]struct{}
}

// cpuSnapshot is the baseline a CPU percentage is measured against.
type cpuSnapshot struct {
	pid     int
	seconds float64
	at      time.Time
}

// netSnapshot is the baseline a transfer rate is measured against.
type netSnapshot struct {
	recv uint64
	sent uint64
	at   time.Time
	ok   bool
}

// New creates a collector retaining `window` worth of samples at `interval`.
func New(interval, window time.Duration, diskPath string, logger *slog.Logger) *Collector {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	retain := int(window / interval)
	if retain < 2 {
		retain = 2
	}

	c := &Collector{
		log:      logger,
		interval: interval,
		retain:   retain,
		diskPath: diskPath,
		series:   make(map[string][]Sample),
		previous: make(map[string]cpuSnapshot),
	}
	c.readHostInfo()
	return c
}

// Interval is the sampling period, published so the UI can size its x-axis.
func (c *Collector) Interval() time.Duration { return c.interval }

// Info returns the static host description.
func (c *Collector) Info() HostInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info
}

// Run samples every target until the context is cancelled. targets is called
// once per tick so instances created or stopped in between are picked up.
func (c *Collector) Run(ctx context.Context, targets func() []Target) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.collect(ctx, targets())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect(ctx, targets())
		}
	}
}

func (c *Collector) collect(ctx context.Context, targets []Target) {
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	now := time.Now()
	c.collectHost(ctx, now)

	live := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		live[target.ID] = struct{}{}
		c.append(target.ID, c.sampleTarget(ctx, target, now))
	}

	// Drop history for instances that no longer exist.
	c.mu.Lock()
	for id := range c.series {
		if _, ok := live[id]; !ok {
			delete(c.series, id)
			delete(c.previous, id)
		}
	}
	c.mu.Unlock()
}

// sampleTarget measures one instance's process tree.
func (c *Collector) sampleTarget(ctx context.Context, target Target, now time.Time) Sample {
	sample := Sample{Time: now}

	if target.PID <= 0 {
		// Stopped: record an explicit zero so the chart shows the drop rather
		// than a gap the eye reads as "still running".
		c.mu.Lock()
		delete(c.previous, target.ID)
		c.mu.Unlock()
		return sample
	}

	proc, err := process.NewProcessWithContext(ctx, int32(target.PID))
	if err != nil {
		return sample
	}

	// A jar launched directly is one process, but a start.sh wrapper or a
	// Forge relauncher puts the real JVM in a child. Summing the tree is the
	// only way those instances report anything but zero.
	tree := append([]*process.Process{proc}, descendants(ctx, proc, 0)...)

	var cpuSeconds float64
	for _, member := range tree {
		if times, err := member.TimesWithContext(ctx); err == nil {
			cpuSeconds += times.User + times.System
		}
		if info, err := member.MemoryInfoWithContext(ctx); err == nil && info != nil {
			sample.MemoryBytes += info.RSS
		}
	}
	sample.Processes = len(tree)

	c.mu.Lock()
	prev, ok := c.previous[target.ID]
	c.previous[target.ID] = cpuSnapshot{pid: target.PID, seconds: cpuSeconds, at: now}
	c.mu.Unlock()

	// A percentage needs two readings of the same process. The first sample
	// after a start or restart has no baseline, so it reports 0 rather than a
	// meaningless since-boot average.
	if ok && prev.pid == target.PID {
		if elapsed := now.Sub(prev.at).Seconds(); elapsed > 0 {
			percent := (cpuSeconds - prev.seconds) / elapsed * 100
			if percent > 0 {
				sample.CPUPercent = percent
			}
		}
	}
	return sample
}

// maxTreeDepth stops a pathological process tree from turning one sample into
// an unbounded walk.
const maxTreeDepth = 4

func descendants(ctx context.Context, proc *process.Process, depth int) []*process.Process {
	if depth >= maxTreeDepth {
		return nil
	}
	children, err := proc.ChildrenWithContext(ctx)
	if err != nil {
		// No children is reported as an error by gopsutil on some platforms.
		return nil
	}

	out := make([]*process.Process, 0, len(children))
	for _, child := range children {
		out = append(out, child)
		out = append(out, descendants(ctx, child, depth+1)...)
	}
	return out
}

func (c *Collector) collectHost(ctx context.Context, now time.Time) {
	sample := HostSample{Time: now}

	// Interval 0 means "since the previous call", which is exactly our tick.
	if percents, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(percents) > 0 {
		sample.CPUPercent = percents[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil && vm != nil {
		sample.MemoryUsed = vm.Used
		sample.MemoryPercent = vm.UsedPercent
	}

	usage, diskErr := disk.UsageWithContext(ctx, c.diskPath)
	net, netOK := c.readNet(ctx)

	c.mu.Lock()
	if netOK {
		// Two readings of a monotonic counter, the same shape as the per-process
		// CPU percentage above. A counter that went *backwards* is an interface
		// that was reset or unplugged rather than negative traffic, and is
		// dropped: the alternative is a spike of several gigabytes a second on
		// the chart every time a VPN reconnects.
		if prev := c.netPrev; prev.ok {
			recv := since(net.recv, prev.recv)
			sent := since(net.sent, prev.sent)
			if elapsed := now.Sub(prev.at).Seconds(); elapsed > 0 {
				sample.NetRecvPerSec = float64(recv) / elapsed
				sample.NetSentPerSec = float64(sent) / elapsed
			}
			c.netInfo.RecvBytes += recv
			c.netInfo.SentBytes += sent
		}
		c.netPrev = netSnapshot{recv: net.recv, sent: net.sent, at: now, ok: true}
		c.netInfo.Interfaces = net.names
		c.netInfo.RecvPerSec = sample.NetRecvPerSec
		c.netInfo.SentPerSec = sample.NetSentPerSec
	}
	c.hostRing = appendCapped(c.hostRing, sample, c.retain)
	if diskErr == nil && usage != nil {
		c.diskInfo = DiskUsage{
			Path:    c.diskPath,
			Total:   usage.Total,
			Free:    usage.Free,
			Percent: usage.UsedPercent,
		}
	}
	c.mu.Unlock()
}

// netTotals is one reading of the machine's interface counters.
type netTotals struct {
	recv  uint64
	sent  uint64
	names []string
}

// readNet sums the byte counters of every interface that carries traffic off
// the machine.
//
// Per-interface rather than gopsutil's own aggregate, which is every interface
// added together — and most machines have several that are the same bytes
// counted twice. Loopback is where a plugin talking to a local database shows
// up, and it never left the box. A docker bridge, a veth pair or a VPN tunnel
// carry traffic that is counted again on the physical card it eventually goes
// out of. Summing all of them gives a number that is two or three times the
// truth on exactly the machines people run panels on.
func (c *Collector) readNet(ctx context.Context) (netTotals, bool) {
	counters, err := psnet.IOCountersWithContext(ctx, true)
	if err != nil || len(counters) == 0 {
		return netTotals{}, false
	}

	out := netTotals{names: make([]string, 0, len(counters))}
	for _, counter := range counters {
		if c.isLoopback(counter.Name) || isVirtual(counter.Name) {
			continue
		}
		out.recv += counter.BytesRecv
		out.sent += counter.BytesSent
		out.names = append(out.names, counter.Name)
	}

	// Every interface filtered out, and yet the machine plainly has one — an
	// unusual naming scheme, or a host that really is nothing but bridges.
	// Better to over-count than to draw a flat zero and call it monitoring.
	if len(out.names) == 0 {
		for _, counter := range counters {
			if c.isLoopback(counter.Name) {
				continue
			}
			out.recv += counter.BytesRecv
			out.sent += counter.BytesSent
			out.names = append(out.names, counter.Name)
		}
	}
	return out, true
}

func (c *Collector) isLoopback(name string) bool {
	if _, ok := c.loopback[name]; ok {
		return true
	}
	// Nothing reported its flags — a container with a restricted /proc, an
	// unsupported platform. The conventional names are a worse test than the
	// flag and a much better one than counting loopback as a network card.
	if len(c.loopback) == 0 {
		return name == "lo" || name == "lo0" || strings.HasPrefix(name, "Loopback")
	}
	return false
}

// virtualPrefixes name the interfaces whose bytes are somebody else's bytes:
// container bridges and the veth ends that plug into them, VM taps, tunnels
// and the shaping devices traffic is mirrored onto. Everything they carry is
// counted again on the interface it actually leaves by.
var virtualPrefixes = []string{
	"br-", "bridge", "cni", "docker", "flannel", "ifb", "kube",
	"lxcbr", "tap", "tun", "veth", "virbr", "vnet", "wg",
}

func isVirtual(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// since is the growth of a monotonic counter, treating any decrease as a reset.
func since(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func (c *Collector) readHostInfo() {
	info := HostInfo{}
	if stat, err := host.Info(); err == nil && stat != nil {
		info.Hostname = stat.Hostname
		info.Platform = stat.Platform
		if stat.PlatformVersion != "" {
			info.Platform += " " + stat.PlatformVersion
		}
	}
	if cores, err := cpu.Counts(true); err == nil {
		info.CPUCores = cores
	}
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		info.MemoryTotal = vm.Total
	}

	loopback := make(map[string]struct{})
	if interfaces, err := psnet.Interfaces(); err == nil {
		for _, iface := range interfaces {
			for _, flag := range iface.Flags {
				if flag == "loopback" {
					loopback[iface.Name] = struct{}{}
				}
			}
		}
	}

	c.mu.Lock()
	c.info = info
	c.loopback = loopback
	c.mu.Unlock()
}

func (c *Collector) append(id string, sample Sample) {
	c.mu.Lock()
	c.series[id] = appendCapped(c.series[id], sample, c.retain)
	c.mu.Unlock()
}

// appendCapped adds one element, dropping the oldest once the cap is reached.
func appendCapped[T any](ring []T, value T, cap int) []T {
	if len(ring) >= cap {
		// Shift in place; at a few hundred elements this is cheaper than the
		// bookkeeping a real circular buffer would need here.
		copy(ring, ring[len(ring)-cap+1:])
		ring = ring[:cap-1]
	}
	return append(ring, value)
}

// Series returns an instance's retained samples, oldest first.
func (c *Collector) Series(id string) []Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()

	samples := c.series[id]
	out := make([]Sample, len(samples))
	copy(out, samples)
	return out
}

// HostSeries returns the host's retained samples, oldest first.
func (c *Collector) HostSeries() []HostSample {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]HostSample, len(c.hostRing))
	copy(out, c.hostRing)
	return out
}

// Disk returns the most recent free-space reading for the data directory.
func (c *Collector) Disk() DiskUsage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.diskInfo
}

// Net returns the latest transfer rates and what has moved since start.
func (c *Collector) Net() NetUsage {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := c.netInfo
	// The slice is handed out; a caller must not be able to write into the
	// collector's copy of it, and the JSON encoder runs outside the lock.
	out.Interfaces = append([]string(nil), c.netInfo.Interfaces...)
	return out
}
