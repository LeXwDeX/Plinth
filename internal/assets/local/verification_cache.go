package local

import (
	"os"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// verificationCache remembers delivery inspection verdicts. Every Open with a
// trusted digest hashes the current bytes before consulting this cache. File
// identity is an additional change detector, not proof of unchanged bytes.
type verificationCache struct {
	mu      sync.Mutex
	entries map[verificationKey]struct{}
	order   []verificationKey
	limit   int
	hits    uint64
	misses  uint64
}

type fileIdentity struct {
	size     int64
	modified int64
	changed  int64
	device   uint64
	inode    uint64
}

type verificationKey struct {
	storageKey   string
	identity     fileIdentity
	digest       string
	expectedSize int64
	profile      string
	role         string
	variant      string
	mediaType    string
}

func newVerificationCache(limit int) *verificationCache {
	if limit <= 0 {
		limit = 4096
	}
	return &verificationCache{entries: make(map[verificationKey]struct{}), limit: limit}
}

func verificationKeyOf(info os.FileInfo, asset generation.Asset) verificationKey {
	return verificationKey{
		storageKey:   asset.StorageKey,
		identity:     fileIdentityOf(info),
		digest:       asset.SHA256,
		expectedSize: asset.Size,
		profile:      asset.DeliveryProfile,
		role:         asset.Role,
		variant:      asset.Variant,
		mediaType:    asset.MediaType,
	}
}

// hit reports whether this exact file identity already passed inspection for
// these expectations. It deliberately does not refresh the entry: a file that
// is being rewritten must not extend its own verdict.
func (c *verificationCache) hit(info os.FileInfo, asset generation.Asset) bool {
	key := verificationKeyOf(info, asset)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		c.hits++
		return true
	}
	c.misses++
	return false
}

func (c *verificationCache) miss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses++
}

func (c *verificationCache) remember(info os.FileInfo, asset generation.Asset) {
	key := verificationKeyOf(info, asset)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		return
	}
	c.entries[key] = struct{}{}
	c.order = append(c.order, key)
	for len(c.order) > c.limit {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
}

func sameFileIdentity(a, b fileIdentity) bool { return a == b }

// VerificationStats reports inspection cache hits and misses and actual bytes
// read for SHA-256, including failed and canceled reads, since store opening.
func (s *Store) VerificationStats() (hits, misses, verifiedBytes uint64) {
	if s.verified == nil {
		return 0, 0, s.verifiedBytes.Load()
	}
	s.verified.mu.Lock()
	defer s.verified.mu.Unlock()
	return s.verified.hits, s.verified.misses, s.verifiedBytes.Load()
}
