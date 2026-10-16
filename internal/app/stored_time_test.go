package app

import (
	"sort"
	"testing"
	"time"
)

// Stored timestamps are TEXT and ordered lexically. Instants whose fraction
// ends in zeros ("…03.3336") must still sort before later instants that extend
// the same digits ("…03.33363"), and across the whole-second boundary.
func TestStoredTimestampsSortLikeTime(t *testing.T) {
	base := time.Date(2026, 10, 16, 19, 10, 23, 0, time.UTC)
	offsets := []time.Duration{
		0, 1, 999_999_999, 1_000_000_000, 333_600_000, 333_630_000, 333_600_001,
		500_000_000, 500_010_000, 120_000_000, 123_000_000, 123_400_000, 123_400_100,
	}
	var times []time.Time
	for _, o := range offsets {
		times = append(times, base.Add(o))
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	stamps := make([]string, len(times))
	for i, ts := range times {
		stamps[i] = stampTime(ts)
		parsed, err := time.Parse(time.RFC3339Nano, stamps[i])
		if err != nil || !parsed.Equal(ts) {
			t.Fatalf("stamp %q does not round-trip %v: %v", stamps[i], ts, err)
		}
	}
	if !sort.StringsAreSorted(stamps) {
		t.Fatalf("lexical order differs from time order: %v", stamps)
	}
}
