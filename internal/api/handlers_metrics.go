package api

import (
	"net/http"
	"runtime"

	"github.com/lanscarlos/hypercraft/internal/metrics"
)

type instanceMetricsResponse struct {
	IntervalSeconds float64          `json:"intervalSeconds"`
	CPUCores        int              `json:"cpuCores"`
	MemoryTotal     uint64           `json:"memoryTotal"`
	MaxMemoryMB     int              `json:"maxMemoryMB"`
	Samples         []metrics.Sample `json:"samples"`
}

// handleInstanceMetrics returns the retained CPU/memory history for one server.
//
// The whole window comes back on every call rather than only new points: it is
// a few kilobytes, and it means a reopened tab draws a complete chart instead
// of filling in from empty.
func (s *Server) handleInstanceMetrics(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if s.metrics == nil {
		writeError(w, http.StatusServiceUnavailable, "metrics collector is not running")
		return
	}

	info := s.metrics.Info()
	writeJSON(w, http.StatusOK, instanceMetricsResponse{
		IntervalSeconds: s.metrics.Interval().Seconds(),
		CPUCores:        info.CPUCores,
		MemoryTotal:     info.MemoryTotal,
		MaxMemoryMB:     inst.Config().MaxMemoryMB,
		Samples:         s.metrics.Series(inst.Config().ID),
	})
}

type systemResponse struct {
	Version         string               `json:"version"`
	GoVersion       string               `json:"goVersion"`
	IntervalSeconds float64              `json:"intervalSeconds"`
	Host            metrics.HostInfo     `json:"host"`
	Disk            metrics.DiskUsage    `json:"disk"`
	Samples         []metrics.HostSample `json:"samples"`
	Panel           panelUsage           `json:"panel"`
	Instances       instanceCounts       `json:"instances"`
}

// panelUsage is the panel's own footprint, which is worth showing next to the
// servers' — the whole point of a Go daemon here is that it is a rounding
// error beside a JVM.
type panelUsage struct {
	HeapBytes  uint64 `json:"heapBytes"`
	Goroutines int    `json:"goroutines"`
}

type instanceCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
}

func (s *Server) handleSystem(w http.ResponseWriter, _ *http.Request) {
	var counts instanceCounts
	for _, inst := range s.mgr.List() {
		counts.Total++
		if inst.State().Running() {
			counts.Running++
		}
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	resp := systemResponse{
		Version:   s.version,
		GoVersion: runtime.Version(),
		Panel: panelUsage{
			HeapBytes:  mem.HeapAlloc,
			Goroutines: runtime.NumGoroutine(),
		},
		Instances: counts,
	}
	if s.metrics != nil {
		resp.IntervalSeconds = s.metrics.Interval().Seconds()
		resp.Host = s.metrics.Info()
		resp.Disk = s.metrics.Disk()
		resp.Samples = s.metrics.HostSeries()
	}
	writeJSON(w, http.StatusOK, resp)
}
