// Package bloom is a compact double-hashed Bloom filter for tracking active
// Iceberg file paths during a scan. It is tuned for our two use cases:
//
//	(1) Orphan file discovery — populate with paths from manifests, then
//	    probe paths returned by the storage list. Membership in the filter
//	    means "definitely referenced"; absence means "candidate orphan"
//	    that the caller must verify against an authoritative set.
//	(2) Future Phase 3 dual-mode (in-memory vs. disk-backed via bbolt/pebble)
//	    based on capacity. The disk path is not yet implemented; the API is
//	    written so it can be slotted in without touching call sites.
//
// The implementation is intentionally allocation-light and concurrency-safe
// for read-after-build access patterns.
package bloom

import (
	"errors"
	"hash/fnv"
	"math"
	"sync/atomic"
)

// Filter is a fixed-size Bloom filter. Add() must not be called concurrently
// with Test(); Test() may be called from many goroutines once Add()s are done.
type Filter struct {
	bits  []uint64
	mBits uint64 // bit count (always a multiple of 64)
	k     uint32 // hash count
	count atomic.Uint64
}

// New builds a Filter sized for n expected insertions and target false
// positive rate p. Both must be positive.
func New(n int, p float64) (*Filter, error) {
	if n <= 0 {
		return nil, errors.New("bloom: n must be > 0")
	}
	if p <= 0 || p >= 1 {
		return nil, errors.New("bloom: p must be in (0,1)")
	}
	// Optimal bits: m = -n*ln(p) / (ln 2)^2
	mFloat := -float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)
	mBits := uint64(math.Ceil(mFloat))
	if mBits < 64 {
		mBits = 64
	}
	// Round up to a multiple of 64.
	if r := mBits % 64; r != 0 {
		mBits += 64 - r
	}
	// Optimal k: (m/n) * ln 2
	k := uint32(math.Round(float64(mBits) / float64(n) * math.Ln2))
	if k < 1 {
		k = 1
	}
	if k > 30 {
		k = 30
	}
	return &Filter{
		bits:  make([]uint64, mBits/64),
		mBits: mBits,
		k:     k,
	}, nil
}

// Add inserts key into the filter.
func (f *Filter) Add(key string) {
	h1, h2 := hashes([]byte(key))
	for i := uint32(0); i < f.k; i++ {
		idx := (h1 + uint64(i)*h2) % f.mBits
		f.bits[idx/64] |= 1 << (idx % 64)
	}
	f.count.Add(1)
}

// Test returns true if key may have been added (with the configured FPR), or
// false if it definitely has not.
func (f *Filter) Test(key string) bool {
	h1, h2 := hashes([]byte(key))
	for i := uint32(0); i < f.k; i++ {
		idx := (h1 + uint64(i)*h2) % f.mBits
		if f.bits[idx/64]&(1<<(idx%64)) == 0 {
			return false
		}
	}
	return true
}

// Len returns the number of Add() calls.
func (f *Filter) Len() uint64 { return f.count.Load() }

// MemoryBytes returns the in-memory size of the bit array.
func (f *Filter) MemoryBytes() int { return len(f.bits) * 8 }

// hashes implements Kirsch-Mitzenmacher double hashing using two FNV-1a 64
// digests over distinct salts. Single-pass, no allocations.
func hashes(b []byte) (uint64, uint64) {
	h1 := fnv.New64a()
	_, _ = h1.Write(b)
	h2 := fnv.New64a()
	_, _ = h2.Write([]byte{0x9e, 0x37, 0x79, 0xb9})
	_, _ = h2.Write(b)
	a, c := h1.Sum64(), h2.Sum64()
	if c == 0 {
		c = 1
	}
	return a, c
}
