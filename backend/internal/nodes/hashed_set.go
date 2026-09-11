package users

import "fmt"

// djb2Dual implements 64-bit dual-hash matching exodus-node.
func djb2Dual(str string) (uint32, uint32) {
	var high uint32 = 5381
	var low uint32 = 5387
	for i := 0; i < len(str); i++ {
		c := uint32(str[i])
		high = (high<<5) + high + c
		low = (low<<6) + low + c*37
	}
	return high, low
}

// HashedSet maintains unique string items and their 64-bit composite hash.
type HashedSet struct {
	seen     map[string]struct{}
	hashHigh uint32
	hashLow  uint32
}

// NewHashedSet allocates a new HashedSet.
func NewHashedSet() *HashedSet {
	return &HashedSet{
		seen: make(map[string]struct{}),
	}
}

// Add adds an item to the set if not already present, updating the composite hash.
func (h *HashedSet) Add(str string) {
	if str == "" {
		return
	}
	if _, ok := h.seen[str]; !ok {
		h.seen[str] = struct{}{}
		high, low := djb2Dual(str)
		h.hashHigh ^= high
		h.hashLow ^= low
	}
}

// Hash64String returns the 16-character hex representation of the composite hash.
func (h *HashedSet) Hash64String() string {
	return fmt.Sprintf("%08x%08x", h.hashHigh, h.hashLow)
}

// Size returns the count of unique items in the set.
func (h *HashedSet) Size() int {
	return len(h.seen)
}
