package javaruntime

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnknownDistribution is returned for an OpenJDK distribution we do not have.
var ErrUnknownDistribution = errors.New("unknown java distribution")

// Distribution ids. These double as the directory-name prefix of anything
// installed, so changing one orphans every runtime already on disk.
const (
	// DistZulu is Azul Zulu, the default: it builds for musl as well as glibc,
	// covers three feature releases Adoptium does not (13, 14, 15), and serves
	// its archives off a commercial CDN rather than GitHub's release storage,
	// which from a mainland Chinese host is the difference that matters.
	DistZulu = "zulu"
	// DistTemurin is Eclipse Temurin, kept rather than replaced: every mirror
	// that makes a download work from inside China carries the Adoptium tree
	// and none of them carries Zulu, so when Azul's CDN is the slow one this
	// is the way out.
	DistTemurin = "temurin"
)

// DefaultDistribution is what an install that names none gets.
const DefaultDistribution = DistZulu

// Distribution is an OpenJDK build the panel can install.
type Distribution struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Default marks the one an install that names none gets.
	Default bool `json:"default,omitempty"`
}

// provider is one distribution's upstream: what it ships, and which build of
// it fits this machine. Everything else about a distribution — where the bytes
// can come from, what the install is called on disk — hangs off the Release it
// returns rather than off this interface.
type provider interface {
	Majors(ctx context.Context) ([]Major, error)
	LatestRelease(ctx context.Context, major int, imageType string, platform Platform) (Release, error)
}

// distributions are offered to the operator in this order, default first.
var distributions = []Distribution{
	{
		ID:      DistZulu,
		Name:    "Azul Zulu",
		Note:    "官方 CDN 直连，国内一般比 GitHub 好走；musl（Alpine）也能装",
		Default: true,
	},
	{
		ID:   DistTemurin,
		Name: "Eclipse Temurin",
		Note: "有清华、南大、华为等国内镜像；没有 musl 构建",
	},
}

// Distributions lists what an operator can pick, default first.
func Distributions() []Distribution {
	out := make([]Distribution, len(distributions))
	copy(out, distributions)
	return out
}

// DistributionName is the human name of a distribution id, for a log line or
// an error message.
func DistributionName(id string) string {
	for _, dist := range distributions {
		if dist.ID == id {
			return dist.Name
		}
	}
	return id
}

// ResolveDistribution normalises a requested distribution id. An empty one is
// the default; anything unrecognised is refused rather than quietly turned
// into one, because silently installing a different vendor's Java than what
// was asked for is the surprise this axis exists to remove.
func ResolveDistribution(id string) (string, error) {
	if id == "" {
		return DefaultDistribution, nil
	}
	for _, dist := range distributions {
		if dist.ID == id {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownDistribution, id)
}
