package brain

import (
	"sync"
	"testing"
	"time"

	"github.com/togo-framework/togo"
)

// fakeCache is a minimal in-process togo.Cache for exercising the L1 logic without
// a real cache provider or Redis.
type fakeCache struct {
	mu sync.Mutex
	m  map[string]any
}

func newFakeCache() *fakeCache { return &fakeCache{m: map[string]any{}} }

func (c *fakeCache) Get(k string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}
func (c *fakeCache) Set(k string, v any, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
}
func (c *fakeCache) Delete(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}

func storeWithCache(c togo.Cache) *Store { return &Store{k: &togo.Kernel{Cache: c}} }

func TestRecallCacheKeyStableAndEpochSensitive(t *testing.T) {
	q := RecallQuery{Namespace: "demo", Query: "  Hello World ", Limit: 8}
	k1 := recallCacheKey(q, "0")
	// Same query normalizes identically → same key.
	if got := recallCacheKey(RecallQuery{Namespace: "demo", Query: "hello world", Limit: 8}, "0"); got != k1 {
		t.Fatalf("expected stable key across normalization: %s != %s", got, k1)
	}
	// Different epoch → different key (invalidation).
	if recallCacheKey(q, "1") == k1 {
		t.Fatal("epoch change must change the cache key")
	}
	// Different namespace → different key (scoping).
	if recallCacheKey(RecallQuery{Namespace: "other", Query: "hello world", Limit: 8}, "0") == k1 {
		t.Fatal("namespace change must change the cache key")
	}
}

func TestRecallCacheRoundTrip(t *testing.T) {
	s := storeWithCache(newFakeCache())
	q := RecallQuery{Namespace: "demo", Query: "cluster pods", Limit: 8}
	key := recallCacheKey(q, s.nsEpoch(q.Namespace))
	if _, ok := s.getCachedRecall(key); ok {
		t.Fatal("expected miss on empty cache")
	}
	want := []Recalled{{ID: "a", Content: "one", Score: 0.9}, {ID: "b", Content: "two", Score: 0.5}}
	s.putCachedRecall(key, want)
	got, ok := s.getCachedRecall(key)
	if !ok || len(got) != 2 || got[0].ID != "a" || got[1].Content != "two" {
		t.Fatalf("round-trip failed: ok=%v got=%+v", ok, got)
	}
}

func TestEpochInvalidation(t *testing.T) {
	s := storeWithCache(newFakeCache())
	q := RecallQuery{Namespace: "demo", Query: "q", Limit: 8}
	k1 := recallCacheKey(q, s.nsEpoch(q.Namespace))
	s.putCachedRecall(k1, []Recalled{{ID: "x"}})
	// A retain-style write bumps the epoch → the recall key changes → old entry is
	// orphaned (a fresh recall misses and recomputes).
	s.bumpEpoch(q.Namespace)
	k2 := recallCacheKey(q, s.nsEpoch(q.Namespace))
	if k1 == k2 {
		t.Fatal("bumpEpoch must change the recall key")
	}
	if _, ok := s.getCachedRecall(k2); ok {
		t.Fatal("post-bump key must miss")
	}
}

func TestNilCacheIsNoOp(t *testing.T) {
	s := &Store{k: &togo.Kernel{}} // no cache provider
	if s.cache() != nil {
		t.Fatal("expected nil cache when no provider is set")
	}
	// Must not panic and must always miss.
	s.putCachedRecall("k", []Recalled{{ID: "x"}})
	if _, ok := s.getCachedRecall("k"); ok {
		t.Fatal("nil cache must always miss")
	}
	if s.nsEpoch("demo") != "0" {
		t.Fatal("nil cache epoch must be 0")
	}
	s.bumpEpoch("demo") // no panic
}
