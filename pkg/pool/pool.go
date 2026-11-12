package pool

import (
	"strings"
	"sync/atomic"
)

// KeyPool manages round-robin load distribution and fallback retries across multiple API keys.
type KeyPool struct {
	keys []string
	idx  uint64
}

// New parses comma-separated keys and initializes a thread-safe KeyPool.
func New(rawKeys string) *KeyPool {
	var clean []string
	for _, k := range strings.Split(rawKeys, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			clean = append(clean, k)
		}
	}
	return &KeyPool{keys: clean}
}

// IsEmpty returns true if no valid keys are present in the pool.
func (p *KeyPool) IsEmpty() bool {
	return p == nil || len(p.keys) == 0
}

// Size returns the total count of keys in the pool.
func (p *KeyPool) Size() int {
	if p == nil {
		return 0
	}
	return len(p.keys)
}

// OrderedKeys returns all keys in round-robin order, starting with the next round-robin key,
// followed by the remaining keys so that callers can attempt fallback retries on rate limits.
func (p *KeyPool) OrderedKeys() []string {
	if p == nil || len(p.keys) == 0 {
		return nil
	}
	n := len(p.keys)
	start := int(atomic.AddUint64(&p.idx, 1)-1) % n

	ordered := make([]string, n)
	for i := 0; i < n; i++ {
		ordered[i] = p.keys[(start+i)%n]
	}
	return ordered
}
