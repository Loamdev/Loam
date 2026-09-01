// Package seed generates realistic, foreign-key-correct synthetic data and bulk
// loads it with Postgres COPY.
//
// Two rules govern everything here. Every foreign key value is drawn from a
// parent row that was actually generated, never invented (build spec section
// 12.5). And rows go in via pgx.CopyFrom, never row-by-row INSERT (section
// 12.3) - the speed is the product's demo moment.
package seed

import (
	"math/rand"
	"sort"
	"strings"

	"github.com/shivansh-source/stateful-playwright/internal/config"
)

// DefaultStatus is the fallback mix for a status-like column whose permitted
// values are unknown. It is deliberately lopsided: uniformly spread values hide
// the edge cases that realistic data is supposed to surface (build spec section 5).
var DefaultStatus = config.Distribution{
	"active":    0.70,
	"trial":     0.15,
	"churned":   0.10,
	"suspended": 0.05,
}

// statusWeights gives common status vocabulary a plausible frequency, so that
// when a column's real allowed values are known the mix still looks like
// production rather than a flat spread. Values outside this lexicon fall back to
// a decaying weight by declaration order.
var statusWeights = map[string]float64{
	// Healthy, common states.
	"active": 70, "enabled": 60, "paid": 45, "completed": 40, "complete": 40,
	"delivered": 30, "shipped": 30, "approved": 28, "published": 28, "success": 35,
	"succeeded": 35, "open": 25, "confirmed": 25, "verified": 30, "done": 35,

	// Ordinary but less common.
	"pending": 16, "processing": 12, "in_progress": 12, "trial": 15, "new": 14,
	"draft": 10, "queued": 10, "closed": 14, "inactive": 9, "unverified": 10,
	"scheduled": 9, "on_hold": 6, "paused": 6, "review": 8, "submitted": 12,

	// Rare states - the ones that actually catch bugs.
	"churned": 8, "cancelled": 6, "canceled": 6, "suspended": 5, "refunded": 3,
	"failed": 4, "rejected": 4, "expired": 4, "archived": 4, "banned": 2,
	"deleted": 2, "error": 3, "disputed": 2, "chargeback": 1, "blocked": 2,
	"abandoned": 3, "unpaid": 5, "overdue": 4, "locked": 2, "disabled": 5,
}

// unknownValueBaseWeight is the weight given to the first value not found in
// statusWeights; later ones decay from it.
const unknownValueBaseWeight = 12.0

// Profile turns a column's permitted values into a weighted distribution.
//
// Known status vocabulary keeps its plausible frequency; anything unrecognized
// decays by declaration order, which mirrors how schemas are usually written
// (the common case first). The result is never uniform, because uniform is the
// one distribution real data never has.
func Profile(values []string) config.Distribution {
	d := make(config.Distribution, len(values))

	unknownRank := 0
	for _, v := range values {
		if w, ok := statusWeights[strings.ToLower(strings.TrimSpace(v))]; ok {
			d[v] = w
			continue
		}
		// Decay: 12, 6, 4, 3, ... never reaching zero.
		d[v] = unknownValueBaseWeight / float64(unknownRank+1)
		unknownRank++
	}
	return d
}

// Uniform spreads weight evenly across values. It exists for callers that
// genuinely want a flat draw; Profile is the right default for status columns.
func Uniform(values []string) config.Distribution {
	d := make(config.Distribution, len(values))
	for _, v := range values {
		d[v] = 1
	}
	return d
}

// Pick draws one value from a weighted distribution.
//
// Keys are sorted before drawing. Go randomizes map iteration order, so without
// that a fixed rng seed would still produce different data on every run, and
// reproducing a bug from a seed would be impossible.
func Pick(r *rand.Rand, d config.Distribution) string {
	if len(d) == 0 {
		return ""
	}

	keys := make([]string, 0, len(d))
	total := 0.0
	for k, w := range d {
		if w <= 0 {
			continue // a zero-weight value is one the user excluded
		}
		keys = append(keys, k)
		total += w
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)

	target := r.Float64() * total
	for _, k := range keys {
		target -= d[k]
		if target <= 0 {
			return k
		}
	}
	return keys[len(keys)-1] // float rounding
}
