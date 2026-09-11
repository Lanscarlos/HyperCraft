package javaruntime

import (
	"errors"
	"testing"
)

func TestDefaultDistributionIsZulu(t *testing.T) {
	resolved, err := ResolveDistribution("")
	if err != nil {
		t.Fatalf("ResolveDistribution(\"\"): %v", err)
	}
	if resolved != DistZulu {
		t.Errorf("an unset distribution resolved to %q, want %q", resolved, DistZulu)
	}

	list := Distributions()
	if len(list) != 2 {
		t.Fatalf("expected two distributions, got %+v", list)
	}
	if list[0].ID != DistZulu || !list[0].Default {
		t.Errorf("Zulu should be first and marked default, got %+v", list[0])
	}
}

// An unrecognised distribution is refused rather than quietly turned into the
// default: silently installing a different vendor's Java than what was asked
// for is exactly the surprise this axis exists to remove.
func TestResolveDistributionRefusesTheUnknown(t *testing.T) {
	if _, err := ResolveDistribution("graalvm"); !errors.Is(err, ErrUnknownDistribution) {
		t.Errorf("expected ErrUnknownDistribution, got %v", err)
	}
}
