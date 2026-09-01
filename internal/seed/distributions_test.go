package seed

import (
	"math/rand"
	"testing"

	"github.com/shivansh-source/stateful-playwright/internal/config"
)

func TestPickIsDeterministicForASeed(t *testing.T) {
	// Go randomizes map iteration order, so a distribution drawn without
	// sorting its keys first would differ between runs even with a fixed seed -
	// and reproducing a bug from a seed would be impossible.
	d := config.Distribution{"active": 0.7, "trial": 0.15, "churned": 0.1, "suspended": 0.05}

	var first []string
	for run := 0; run < 20; run++ {
		r := rand.New(rand.NewSource(42)) //nolint:gosec // test determinism, not security
		got := make([]string, 0, 200)
		for i := 0; i < 200; i++ {
			got = append(got, Pick(r, d))
		}
		if run == 0 {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("draw %d differed between runs: %q then %q", i, first[i], got[i])
			}
		}
	}
}

func TestPickRespectsWeights(t *testing.T) {
	r := rand.New(rand.NewSource(1)) //nolint:gosec // test
	d := config.Distribution{"common": 0.9, "rare": 0.1}

	counts := map[string]int{}
	const n = 20000
	for i := 0; i < n; i++ {
		counts[Pick(r, d)]++
	}

	commonPct := float64(counts["common"]) / n
	if commonPct < 0.85 || commonPct > 0.95 {
		t.Errorf("common drawn %.1f%% of the time, want about 90%%", commonPct*100)
	}
	if counts["rare"] == 0 {
		t.Error("rare was never drawn; a weighted value must still appear")
	}
}

func TestPickSkipsZeroWeightValues(t *testing.T) {
	// Weight 0 is how a user excludes a value in playbrige.yml.
	r := rand.New(rand.NewSource(7)) //nolint:gosec // test
	d := config.Distribution{"keep": 1, "drop": 0}

	for i := 0; i < 500; i++ {
		if got := Pick(r, d); got != "keep" {
			t.Fatalf("Pick returned %q, want only %q: zero weight means excluded", got, "keep")
		}
	}
}

func TestPickWithNothingToDraw(t *testing.T) {
	r := rand.New(rand.NewSource(1)) //nolint:gosec // test
	if got := Pick(r, nil); got != "" {
		t.Errorf("Pick(nil) = %q, want empty", got)
	}
	if got := Pick(r, config.Distribution{"a": 0}); got != "" {
		t.Errorf("Pick(all-zero) = %q, want empty", got)
	}
}

func TestProfileIsNeverUniform(t *testing.T) {
	// The whole point of a distribution profile: a few values must be rare.
	// Uniform data is the one shape real data never has (build spec section 5).
	values := []string{"pending", "paid", "shipped", "cancelled", "refunded"}
	d := Profile(values)

	if len(d) != len(values) {
		t.Fatalf("Profile covered %d of %d values", len(d), len(values))
	}

	var lowest, highest float64 = -1, 0
	for _, w := range d {
		if w > highest {
			highest = w
		}
		if lowest < 0 || w < lowest {
			lowest = w
		}
	}
	if highest <= lowest {
		t.Fatalf("Profile is flat: %v", d)
	}
	// The common case should dominate the rare one by a real margin.
	if highest/lowest < 3 {
		t.Errorf("Profile spread is only %.1fx (%v); rare values should be genuinely rare", highest/lowest, d)
	}
	if d["paid"] <= d["refunded"] {
		t.Errorf("paid (%.0f) should outweigh refunded (%.0f)", d["paid"], d["refunded"])
	}
}

func TestProfileHandlesUnknownVocabulary(t *testing.T) {
	// Values outside the lexicon still get a decaying, non-flat spread.
	d := Profile([]string{"zeta", "yankee", "xray"})

	if len(d) != 3 {
		t.Fatalf("Profile = %v, want 3 entries", d)
	}
	if !(d["zeta"] > d["yankee"] && d["yankee"] > d["xray"]) {
		t.Errorf("declaration order should decay: %v", d)
	}
	for v, w := range d {
		if w <= 0 {
			t.Errorf("%s has weight %v; no value should be impossible", v, w)
		}
	}
}

func TestProfileIsStable(t *testing.T) {
	values := []string{"active", "churned", "mystery"}
	first := Profile(values)
	for i := 0; i < 10; i++ {
		got := Profile(values)
		for k, v := range first {
			if got[k] != v {
				t.Fatalf("Profile is not stable: %s was %v then %v", k, v, got[k])
			}
		}
	}
}

func TestUniformIsFlat(t *testing.T) {
	d := Uniform([]string{"a", "b", "c"})
	for k, w := range d {
		if w != 1 {
			t.Errorf("Uniform gave %s weight %v, want 1", k, w)
		}
	}
}

func TestDefaultStatusMatchesTheSpec(t *testing.T) {
	// Build spec section 5 names these proportions explicitly.
	want := map[string]float64{"active": 0.70, "trial": 0.15, "churned": 0.10, "suspended": 0.05}
	for k, v := range want {
		if DefaultStatus[k] != v {
			t.Errorf("DefaultStatus[%q] = %v, want %v", k, DefaultStatus[k], v)
		}
	}
	if len(DefaultStatus) != len(want) {
		t.Errorf("DefaultStatus = %v, want exactly %v", DefaultStatus, want)
	}
}
