package gitlite

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signature is one side of a commit's authorship.
//
// The config history uses the two halves for different things — author is who
// asked for the change, committer is always the panel — which is what lets a
// timeline say "lanscarlos" on a hand-made snapshot and "system:lifecycle" on
// the one taken automatically before a start. See the design's §4.
type Signature struct {
	Name  string
	Email string
	When  time.Time
}

func (s Signature) encode() string {
	when := s.When
	if when.IsZero() {
		when = time.Now()
	}
	_, offset := when.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s <%s> %d %s%02d%02d",
		sanitiseIdentity(s.Name), sanitiseIdentity(s.Email),
		when.Unix(), sign, offset/3600, (offset%3600)/60)
}

// sanitiseIdentity strips what would break the line-oriented commit format.
// Names reach here from operator input, so this is a parser guard rather than
// tidying: an actor called "a\nparent deadbeef…" must not be able to forge a
// commit header.
func sanitiseIdentity(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', '\n', '\r', 0:
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return s
}

// Commit is one recorded state of the configuration.
type Commit struct {
	Hash      string
	Tree      string
	Parents   []string
	Author    Signature
	Committer Signature
	Message   string
}

// CommitInput describes a commit to write.
type CommitInput struct {
	Tree      string
	Parents   []string
	Author    Signature
	Committer Signature
	Message   string
}

// WriteCommit stores a commit object and returns its id. It does not move the
// branch; SetHead does that, and keeping them apart is what makes a commit
// that fails halfway leave nothing but unreachable objects behind.
func (r *Repo) WriteCommit(in CommitInput) (string, error) {
	if !validHash(in.Tree) {
		return "", fmt.Errorf("commit tree %q is not an object id", in.Tree)
	}

	var b strings.Builder
	b.WriteString("tree " + in.Tree + "\n")
	for _, parent := range in.Parents {
		if !validHash(parent) {
			return "", fmt.Errorf("commit parent %q is not an object id", parent)
		}
		b.WriteString("parent " + parent + "\n")
	}
	b.WriteString("author " + in.Author.encode() + "\n")
	b.WriteString("committer " + in.Committer.encode() + "\n")
	b.WriteString("\n")

	message := strings.ReplaceAll(in.Message, "\r\n", "\n")
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	b.WriteString(message)

	return r.writeObject("commit", []byte(b.String()))
}

// ReadCommit parses one commit.
func (r *Repo) ReadCommit(hash string) (*Commit, error) {
	kind, data, err := r.readObject(hash)
	if err != nil {
		return nil, err
	}
	if kind != "commit" {
		return nil, fmt.Errorf("%s is a %s, not a commit", hash, kind)
	}

	commit := &Commit{Hash: hash}
	header, message, _ := strings.Cut(string(data), "\n\n")
	commit.Message = message
	for _, line := range strings.Split(header, "\n") {
		field, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch field {
		case "tree":
			commit.Tree = value
		case "parent":
			commit.Parents = append(commit.Parents, value)
		case "author":
			commit.Author = parseSignature(value)
		case "committer":
			commit.Committer = parseSignature(value)
		}
	}
	if !validHash(commit.Tree) {
		return nil, fmt.Errorf("commit %s names no tree", hash)
	}
	return commit, nil
}

func parseSignature(value string) Signature {
	var sig Signature

	open := strings.LastIndex(value, " <")
	shut := strings.LastIndex(value, "> ")
	if open < 0 || shut < open {
		sig.Name = strings.TrimSpace(value)
		return sig
	}
	sig.Name = value[:open]
	sig.Email = value[open+2 : shut]

	stamp := strings.Fields(value[shut+2:])
	if len(stamp) == 0 {
		return sig
	}
	seconds, err := strconv.ParseInt(stamp[0], 10, 64)
	if err != nil {
		return sig
	}
	zone := time.UTC
	if len(stamp) > 1 && len(stamp[1]) == 5 {
		hours, hErr := strconv.Atoi(stamp[1][1:3])
		minutes, mErr := strconv.Atoi(stamp[1][3:5])
		if hErr == nil && mErr == nil {
			offset := hours*3600 + minutes*60
			if stamp[1][0] == '-' {
				offset = -offset
			}
			zone = time.FixedZone("", offset)
		}
	}
	sig.When = time.Unix(seconds, 0).In(zone)
	return sig
}

// Log walks the first-parent chain from a commit, newest first. limit <= 0
// means the whole history.
//
// First-parent only is not a simplification that might one day need
// generalising: this history is linear by construction and has nowhere a merge
// could come from.
func (r *Repo) Log(from string, limit int) ([]*Commit, error) {
	var out []*Commit
	seen := map[string]bool{}
	for hash := from; hash != ""; {
		if seen[hash] {
			return out, nil
		}
		seen[hash] = true

		commit, err := r.ReadCommit(hash)
		if err != nil {
			return out, err
		}
		out = append(out, commit)
		if limit > 0 && len(out) >= limit {
			return out, nil
		}
		if len(commit.Parents) == 0 {
			return out, nil
		}
		hash = commit.Parents[0]
	}
	return out, nil
}
