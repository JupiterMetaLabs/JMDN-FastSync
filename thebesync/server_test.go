package thebesync

import (
	"math"
	"testing"
)

// TestClampedTo verifies the GetBlocks range length cap, including the
// overflow-safe path for a malicious upper bound.
func TestClampedTo(t *testing.T) {
	cases := []struct {
		name     string
		from, to uint64
		want     uint64
	}{
		{"single block", 5, 5, 5},
		{"within cap", 0, 9, 9},
		{"exactly cap", 100, 100 + MaxBlocksPerRequest - 1, 100 + MaxBlocksPerRequest - 1},
		{"over cap clamps", 100, 100 + MaxBlocksPerRequest, 100 + MaxBlocksPerRequest - 1},
		{"far over cap clamps", 0, 1_000_000, MaxBlocksPerRequest - 1},
		{"malicious max uint64 no overflow", 0, math.MaxUint64, MaxBlocksPerRequest - 1},
		{"high from + max to no overflow", 42, math.MaxUint64, 42 + MaxBlocksPerRequest - 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampedTo(c.from, c.to); got != c.want {
				t.Fatalf("clampedTo(%d,%d) = %d, want %d", c.from, c.to, got, c.want)
			}
			if got := clampedTo(c.from, c.to); got-c.from+1 > MaxBlocksPerRequest {
				t.Fatalf("clampedTo(%d,%d) span %d exceeds cap %d", c.from, c.to, got-c.from+1, MaxBlocksPerRequest)
			}
		})
	}
}
