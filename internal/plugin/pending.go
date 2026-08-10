package plugin

// Changes that have been made but have not happened yet.
//
// Every plugin operation in this package writes a file into a directory a
// running server read once, at startup, and will not read again. Installing,
// removing, switching a version and switching a plugin off all take effect on
// the next boot and not before — which is the single fact a plugin page has to
// get across, and the one an on/off switch that flips instantly denies.
//
// So a change is recorded here as well as applied, and the page shows the
// count. The record is what turns "I disabled it, why is it still loaded" into
// a banner that says 3 项变更待重启生效 with a restart button next to it.
//
// Pending is relative to a process, not to a clock. A change is pending if it
// was made after the server's current process started; the same change against
// a stopped server is not pending, because there is nothing running that has
// missed it. That makes a restart self-clearing — the new process starts after
// every recorded change, so nothing is pending — and means no code has to
// remember to clear anything on a path that might not run.

import (
	"encoding/json"
	"os"
	"path"
	"sort"
	"sync"
	"time"
)

// The actions worth naming in the banner. What they have in common is that the
// running server cannot see any of them.
const (
	ActionInstall = "install"
	ActionUpgrade = "upgrade"
	ActionRemove  = "remove"
	ActionEnable  = "enable"
	ActionDisable = "disable"
)

// Change is one plugin operation waiting on a restart.
type Change struct {
	// Key is the listing key the change was made against, so a row can ask
	// "am I one of the pending ones" without matching on names.
	Key    string    `json:"key"`
	Name   string    `json:"name"`
	Action string    `json:"action"`
	At     time.Time `json:"at"`
}

// Label is the change as the banner lists it.
func (c Change) Label() string {
	switch c.Action {
	case ActionInstall:
		return "安装 " + c.Name
	case ActionUpgrade:
		return "更换 " + c.Name + " 的版本"
	case ActionRemove:
		return "移除 " + c.Name
	case ActionEnable:
		return "启用 " + c.Name
	case ActionDisable:
		return "停用 " + c.Name
	default:
		return c.Name
	}
}

// Pending records, per instance, the plugin changes made since the panel last
// looked. It is stored next to the install records and in the same shape: a
// small JSON file the panel is the only writer of.
type Pending struct {
	path string

	mu      sync.Mutex
	changes map[string][]Change
	loaded  bool
}

func NewPending(path string) *Pending { return &Pending{path: path} }

// Record notes a change against an instance.
//
// One entry per key: an operator who disables a plugin, re-enables it and
// disables it again has made one pending change, not three, and a banner that
// counted three would be counting clicks rather than differences. The newest
// action wins because it is the one the next boot will see.
func (p *Pending) Record(instanceID string, change Change) error {
	if change.At.IsZero() {
		change.At = time.Now()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	all := p.load()

	list := all[instanceID]
	for i := range list {
		if list[i].Key != change.Key {
			continue
		}
		// Install followed by remove is not "a removal is pending" — it is a
		// plugin the running server never saw arrive and will never see leave.
		// Dropping the pair keeps the count honest.
		if list[i].Action == ActionInstall && change.Action == ActionRemove {
			all[instanceID] = append(list[:i:i], list[i+1:]...)
			if len(all[instanceID]) == 0 {
				delete(all, instanceID)
			}
			return p.save(all)
		}
		list[i] = change
		all[instanceID] = list
		return p.save(all)
	}
	all[instanceID] = append(list, change)
	return p.save(all)
}

// Since returns the changes a process started at this time has not seen,
// newest first.
//
// A zero start time means the instance is not running: nothing is pending,
// because the next start will pick up everything anyway. This is also where
// the file is trimmed — a change older than the current process has already
// taken effect, and keeping it would grow the file forever.
func (p *Pending) Since(instanceID string, startedAt time.Time) []Change {
	if startedAt.IsZero() {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	all := p.load()

	list := all[instanceID]
	out := make([]Change, 0, len(list))
	kept := make([]Change, 0, len(list))
	for _, change := range list {
		if change.At.After(startedAt) {
			out = append(out, change)
			kept = append(kept, change)
		}
	}
	if len(kept) != len(list) {
		if len(kept) == 0 {
			delete(all, instanceID)
		} else {
			all[instanceID] = kept
		}
		// A failed write costs a stale file, not a wrong answer: the same
		// filter runs on every read.
		_ = p.save(all)
	}

	sort.Slice(out, func(a, b int) bool { return out[a].At.After(out[b].At) })
	return out
}

// Forget drops an instance's changes, for when the instance is deleted.
func (p *Pending) Forget(instanceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	all := p.load()
	if _, ok := all[instanceID]; !ok {
		return nil
	}
	delete(all, instanceID)
	return p.save(all)
}

func (p *Pending) load() map[string][]Change {
	if p.loaded {
		return p.changes
	}
	p.loaded = true
	p.changes = map[string][]Change{}

	data, err := os.ReadFile(p.path)
	if err != nil {
		return p.changes
	}
	var stored map[string][]Change
	if err := json.Unmarshal(data, &stored); err != nil || stored == nil {
		// Losing this file costs a banner, not a change: the jars are already
		// where they belong. Not worth refusing to start over.
		return p.changes
	}
	p.changes = stored
	return p.changes
}

func (p *Pending) save(all map[string][]Change) error {
	p.changes = all
	if err := os.MkdirAll(path.Dir(p.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}

	temp := p.path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, p.path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
