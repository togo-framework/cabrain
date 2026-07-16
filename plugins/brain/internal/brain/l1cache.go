package brain

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// L1 working-memory cache (SPEC §2.1, D4). Cache-aside over the kernel's Cache
// contract, so it is driver-agnostic: with CACHE_DRIVER=memory it is an in-process
// L1; installing the cache-redis plugin + CACHE_DRIVER=redis makes it the shared
// Redis L1 across app replicas — the same code, selected by config. Postgres stays
// authoritative; the cache only accelerates repeated recalls and never holds the
// only copy of anything.
//
// Invalidation is per-namespace epoch: every retain stamps brain:epoch:<ns> with a
// fresh value, which is folded into the recall cache key, so a write instantly
// orphans that namespace's cached reads (they then TTL out). This needs no key
// scanning — important for the Redis driver, whose Cache contract exposes no SCAN.

const (
	epochKeyPrefix   = "brain:epoch:"
	recallKeyPrefix  = "brain:recall:"
	defaultRecallTTL = 30 * time.Second
)

// cache returns the kernel cache (nil if no cache provider booted).
func (s *Store) cache() interface {
	Get(key string) (any, bool)
	Set(key string, value any, ttl time.Duration)
	Delete(key string)
} {
	if s.k == nil {
		return nil
	}
	return s.k.Cache
}

// recallTTL is the L1 entry lifetime. BRAIN_RECALL_CACHE_TTL (seconds) overrides
// it; "0" disables recall caching entirely even when a cache provider is present.
func recallTTL() (time.Duration, bool) {
	if v := os.Getenv("BRAIN_RECALL_CACHE_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n <= 0 {
				return 0, false
			}
			return time.Duration(n) * time.Second, true
		}
	}
	return defaultRecallTTL, true
}

// nsEpoch reads the current invalidation epoch for a namespace ("0" if unset).
func (s *Store) nsEpoch(ns string) string {
	c := s.cache()
	if c == nil {
		return "0"
	}
	if v, ok := c.Get(epochKeyPrefix + ns); ok {
		return fmt.Sprint(v)
	}
	return "0"
}

// bumpEpoch invalidates a namespace's cached recalls after a write (best-effort).
func (s *Store) bumpEpoch(ns string) {
	c := s.cache()
	if c == nil {
		return
	}
	// UnixNano is monotonic enough for cache invalidation; a rare collision only
	// costs one stale-until-TTL read.
	c.Set(epochKeyPrefix+ns, strconv.FormatInt(time.Now().UnixNano(), 10), 24*time.Hour)
}

// recallCacheKey is stable across identical queries (normalized text) and changes
// with any parameter or the namespace epoch.
func recallCacheKey(q RecallQuery, epoch string) string {
	norm := strings.ToLower(strings.TrimSpace(q.Query))
	raw := fmt.Sprintf("%s|%s|%d|%g|%t|%s", q.Namespace, norm, q.Limit, q.MinImportance, q.ExpandEntity, epoch)
	sum := sha1.Sum([]byte(raw))
	return recallKeyPrefix + hex.EncodeToString(sum[:])
}

// getCachedRecall returns a cached result set for the key, if present and decodable.
// Values are stored as JSON so the result round-trips through any cache driver
// (the memory driver keeps the string as-is; the redis driver stores the bytes).
func (s *Store) getCachedRecall(key string) ([]Recalled, bool) {
	c := s.cache()
	if c == nil || key == "" {
		return nil, false
	}
	v, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	var b []byte
	switch t := v.(type) {
	case string:
		b = []byte(t)
	case []byte:
		b = t
	case []Recalled: // memory driver may hand back the typed value directly
		return t, true
	default:
		return nil, false
	}
	var out []Recalled
	if json.Unmarshal(b, &out) != nil {
		return nil, false
	}
	return out, true
}

// putCachedRecall stores a result set under the key with the configured TTL.
func (s *Store) putCachedRecall(key string, rs []Recalled) {
	c := s.cache()
	ttl, on := recallTTL()
	if c == nil || key == "" || !on {
		return
	}
	if b, err := json.Marshal(rs); err == nil {
		c.Set(key, string(b), ttl)
	}
}
