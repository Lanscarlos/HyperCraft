package plugin

// Checking the books against the shelf.
//
// A panel that only keeps records eventually lies. The record says LuckPerms
// 5.5.71; the directory holds whatever was last put there by an operator over
// SFTP, by a restored backup, by the plugin rewriting its own jar, or by nobody
// at all because somebody deleted it. None of those are exotic — over a year of
// running servers, all four happen — and every one of them turns the version
// number on the page into a claim the panel has no basis for.
//
// So the directory is read and hashed, and the answer is one of four:
//
//	ok       the file is there and is byte-for-byte the one the record names
//	drift    the record's file is there and is not that file any more
//	missing  the record names a file that is not there
//	foreign  a jar is there that no record names
//
// Drift is not automatically an incident. Anticheats that fetch signature
// updates, Geyser's self-updating builds and a handful of others rewrite their
// own jar in normal operation, which is why 允许自更新 exists per plugin: with
// it on, drift is still recorded and still shown, it just stops being reported
// as something wrong. Without that switch the alarm fires weekly on a plugin
// that is working correctly, and an alarm like that gets ignored — including
// the week it means something.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// The four states a reconciled file can be in. Empty is the fifth: a managed
// row nothing has checked yet, which is not the same as agreement.
const (
	ReconOK      = "ok"
	ReconDrift   = "drift"
	ReconMissing = "missing"
	ReconForeign = "foreign"
)

// Report is what one pass over an instance's plugin directories found.
type Report struct {
	InstanceID string    `json:"instanceId"`
	At         time.Time `json:"at"`
	// Checked is how many recorded plugins were compared, Foreign how many jars
	// were found that no record claims.
	Checked int `json:"checked"`
	OK      int `json:"ok"`
	Drift   int `json:"drift"`
	Missing int `json:"missing"`
	Foreign int `json:"foreign"`
	// Findings are the rows that are not ok, which is the only part anybody
	// reads. A clean pass reports its counts and an empty list.
	Findings []Finding `json:"findings"`
}

// Clean reports whether the books and the directory agree.
func (r Report) Clean() bool { return r.Drift == 0 && r.Missing == 0 && r.Foreign == 0 }

// Finding is one file the reconciliation has something to say about.
type Finding struct {
	State    string `json:"state"`
	Dir      string `json:"dir"`
	FileName string `json:"fileName"`
	// PluginID is set for a record; empty for a foreign jar, which by
	// definition belongs to nothing the panel tracks.
	PluginID string `json:"pluginId,omitempty"`
	Name     string `json:"name"`
	// Expected is the digest the ledger holds, Found what is on disk. A
	// missing file has no Found; a foreign jar has no Expected.
	Expected string `json:"expected,omitempty"`
	Found    string `json:"found,omitempty"`
	// Jar is what a foreign or drifted file declares itself to be, which is
	// the only way to answer "what is this thing" for a jar nobody recorded.
	Jar *JarInfo `json:"jar,omitempty"`
	// Adoptable is set when a foreign jar turns out to be one of the library's
	// own downloads that simply was not installed through the panel.
	Adoptable *Adoptable `json:"adoptable,omitempty"`
	// Allowed marks a drift on a plugin with 允许自更新 on: recorded, shown,
	// and deliberately not counted as a problem.
	Allowed bool `json:"allowed,omitempty"`
}

// Reconcile hashes every jar in an instance's plugin directories and compares
// it with the ledger.
//
// Deliberately not part of the ordinary listing. It reads and digests every jar
// on the server — tens of megabytes each, dozens of them — and doing that on
// every page load would be a plugin page that gets slower the more plugins you
// have. It runs when the instance starts, when the operator asks, and at the
// end of every upgrade transaction, which are the three moments the answer can
// have changed.
func (m *Instances) Reconcile(instanceID, directory string) (Report, error) {
	report := Report{InstanceID: instanceID, At: time.Now(), Findings: []Finding{}}
	browser := serverfiles.New(directory)

	// Everything on disk, by dir/name, hashed once. The same file is looked at
	// from both sides below — as a record's file, and as a candidate foreign
	// jar — and hashing it twice would double the cost of the slow part.
	onDisk, err := m.scan(browser)
	if err != nil {
		return Report{}, err
	}

	// Held across the whole pass: the findings are derived from the records at
	// the same moment the records are updated, and a listing that slipped in
	// between would show half of each. Nothing under this lock touches the
	// network or the library's own lock in a way that could wait on it.
	m.mu.Lock()
	defer m.mu.Unlock()
	records := m.load()
	book := m.ledgerFor(records, instanceID)

	claimed := map[string]bool{}
	for i := range book.Plugins {
		record := &book.Plugins[i]
		report.Checked++
		record.CheckedAt = report.At

		item, libErr := m.library.Get(record.PluginID)
		name := record.PluginID
		if libErr == nil {
			name = item.Name
		}

		found, ok := onDisk[record.Dir+"/"+record.FileName]
		if !ok {
			record.Recon = ReconMissing
			record.ObservedSHA = ""
			report.Missing++
			report.Findings = append(report.Findings, Finding{
				State: ReconMissing, Dir: record.Dir, FileName: record.FileName,
				PluginID: record.PluginID, Name: name, Expected: record.SHA256,
			})
			continue
		}
		claimed[record.Dir+"/"+record.FileName] = true
		record.ObservedSHA = found.sha

		// A record written before the panel stored digests has nothing to
		// compare against. Adopting what is on disk is the only honest move —
		// the alternative is reporting drift on every plugin installed by an
		// older release — and it is recorded as the baseline from here on.
		if record.SHA256 == "" {
			record.SHA256 = found.sha
			if found.jar != nil {
				record.PluginName = found.jar.Name
			}
		}

		if strings.EqualFold(record.SHA256, found.sha) {
			record.Recon = ReconOK
			report.OK++
			continue
		}

		record.Recon = ReconDrift
		allowed := libErr == nil && item.Policy.AllowSelfUpdate
		if !allowed {
			report.Drift++
		}
		report.Findings = append(report.Findings, Finding{
			State: ReconDrift, Dir: record.Dir, FileName: record.FileName,
			PluginID: record.PluginID, Name: name,
			Expected: record.SHA256, Found: found.sha, Jar: found.jar, Allowed: allowed,
		})
	}

	// What is left on disk belongs to nobody. Named from its own descriptor
	// rather than from its file name, because "unknown-1.jar" tells an
	// operator nothing and "AntiCheat 3.2" tells them everything.
	for key, file := range onDisk {
		if claimed[key] {
			continue
		}
		report.Foreign++
		finding := Finding{
			State: ReconForeign, Dir: file.dir, FileName: file.name,
			Name: file.name, Found: file.sha, Jar: file.jar,
		}
		if file.jar != nil && file.jar.Name != "" {
			finding.Name = file.jar.Name
		}
		finding.Adoptable = m.matchDigest(file.sha)
		report.Findings = append(report.Findings, finding)
	}

	// Worst first, then by name: this list is read from the top and the top
	// should be the thing that most needs doing.
	sort.SliceStable(report.Findings, func(a, b int) bool {
		if rank := findingRank(report.Findings[a]) - findingRank(report.Findings[b]); rank != 0 {
			return rank < 0
		}
		return strings.ToLower(report.Findings[a].Name) < strings.ToLower(report.Findings[b].Name)
	})

	book.ReconciledAt = report.At
	return report, m.save(records)
}

// Accept takes the file on disk as the new truth for one record.
//
// The other half of what a drift finding offers. "Restore the library's copy"
// is the right move when the file was tampered with; this is the right move
// when it was not — a plugin that updated itself, a hotfix jar somebody dropped
// in deliberately — and without it the only way to clear the finding would be
// to overwrite a file the operator wanted.
//
// It re-reads the jar rather than trusting the last scan: the whole point is
// that this file changes, and the digest and declared version recorded here are
// what every future comparison is made against.
func (m *Instances) Accept(instanceID, directory, pluginID string) (Entry, error) {
	record := m.record(instanceID, pluginID)
	if record == nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrNotInstalled, pluginID)
	}

	browser := serverfiles.New(directory)
	path, enabled, ok := m.locate(browser, record.Dir, record.FileName)
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s/%s 不在那儿，没有可以采纳的文件",
			ErrNotInstalled, record.Dir, record.FileName)
	}
	file, info, closer, err := browser.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer closer()

	digest, err := fileDigest(file)
	if err != nil {
		return Entry{}, err
	}
	updated := *record
	updated.SHA256, updated.ObservedSHA = digest, digest
	updated.Recon, updated.CheckedAt = ReconOK, time.Now()
	if jar, read := ReadJarInfo(file, info.Size()); read {
		updated.PluginName = jar.Name
		// The version on the row now describes this file rather than the
		// release it came from, and saying so is the difference between a
		// number that is true and one that used to be.
		if jar.Version != "" {
			updated.Version = jar.Version
		}
	}
	if err := m.put(instanceID, updated); err != nil {
		return Entry{}, err
	}

	name := pluginID
	if item, err := m.library.Get(pluginID); err == nil {
		name = item.Name
	}
	return Entry{
		Key:       keyPluginPrefix + pluginID,
		PluginID:  pluginID,
		Name:      name,
		FileName:  updated.FileName,
		Dir:       updated.Dir,
		Enabled:   enabled,
		Managed:   true,
		Size:      info.Size(),
		Tag:       updated.Tag,
		Version:   updated.Version,
		SHA256:    digest,
		RecordSHA: digest,
		Recon:     ReconOK,
		CheckedAt: updated.CheckedAt,
	}, nil
}

// isMissingDir reports the "this server has never started" case, which every
// directory read here has to survive rather than fail on.
func isMissingDir(err error) bool { return errors.Is(err, serverfiles.ErrNotFound) }

// Foreign lists the jars in an instance's plugin directories that no record
// claims, without hashing anything.
//
// The cheap half of the reconciliation, and the half the library page can
// afford on every load. Whether a file is in the ledger is a name lookup;
// whether it is still the file the ledger describes is a few megabytes of
// reading, and only the second one needs Reconcile. Each jar is still asked
// what it is — that is a couple of kilobytes out of the zip directory, and
// without it the page can only say "there is a file called paper-1.jar here",
// which answers nothing.
func (m *Instances) Foreign(instanceID, directory string) []Finding {
	browser := serverfiles.New(directory)

	recorded := map[string]bool{}
	for _, record := range m.recordsFor(instanceID) {
		recorded[record.Dir+"/"+record.FileName] = true
	}

	dirs := map[string]bool{DefaultTargetDir: true}
	for _, item := range m.library.List() {
		dirs[item.TargetDir] = true
	}

	out := []Finding{}
	for dir := range dirs {
		entries, err := browser.List(dir)
		if err != nil {
			continue
		}
		for _, file := range entries {
			if file.IsDir {
				continue
			}
			name, _, ok := jarName(file.Name)
			if !ok || recorded[dir+"/"+name] {
				continue
			}
			finding := Finding{State: ReconForeign, Dir: dir, FileName: name, Name: name}
			handle, info, closer, err := browser.Open(dir + "/" + file.Name)
			if err == nil {
				if jar, read := ReadJarInfo(handle, info.Size()); read {
					finding.Jar = &jar
					if jar.Name != "" {
						finding.Name = jar.Name
					}
				}
				closer()
			}
			out = append(out, finding)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name)
	})
	return out
}

func findingRank(f Finding) int {
	switch {
	case f.State == ReconMissing:
		return 0
	case f.State == ReconDrift && !f.Allowed:
		return 1
	case f.State == ReconForeign:
		return 2
	default:
		return 3
	}
}

// scanned is one jar on disk, read once.
type scanned struct {
	dir, name string
	sha       string
	size      int64
	enabled   bool
	jar       *JarInfo
}

// scan digests every jar under the directories an instance's plugins can live
// in, and asks each one what it is.
func (m *Instances) scan(browser *serverfiles.Browser) (map[string]scanned, error) {
	dirs := map[string]bool{DefaultTargetDir: true}
	for _, item := range m.library.List() {
		dirs[item.TargetDir] = true
	}

	out := map[string]scanned{}
	for dir := range dirs {
		entries, err := browser.List(dir)
		if err != nil {
			// A server that has never started has no plugins directory. That
			// is an empty result, not a failure to reconcile.
			if isMissingDir(err) {
				continue
			}
			return nil, err
		}
		for _, file := range entries {
			if file.IsDir {
				continue
			}
			name, enabled, ok := jarName(file.Name)
			if !ok {
				continue
			}
			item := scanned{dir: dir, name: name, size: file.Size, enabled: enabled}
			handle, info, closer, err := browser.Open(dir + "/" + file.Name)
			if err != nil {
				continue
			}
			if digest, err := fileDigest(handle); err == nil {
				item.sha = digest
			}
			if jar, ok := ReadJarInfo(handle, info.Size()); ok {
				item.jar = &jar
			}
			closer()
			if item.sha != "" {
				out[dir+"/"+name] = item
			}
		}
	}
	return out, nil
}

// matchDigest finds the library download a digest belongs to, if any.
func (m *Instances) matchDigest(sha string) *Adoptable {
	if sha == "" {
		return nil
	}
	for _, item := range m.library.List() {
		if version, artifact, ok := item.FindArtifact(sha); ok {
			return &Adoptable{
				PluginID: item.ID,
				Name:     item.Name,
				Tag:      version.Tag,
				Version:  version.Version,
				FileName: artifact.FileName,
			}
		}
	}
	return nil
}
