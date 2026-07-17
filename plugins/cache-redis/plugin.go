// Package cacheredis registers a "redis" driver for the togo cache service, so
// CaBrain's L1 working-memory cache (SPEC §2.1, D4) becomes a SHARED Redis tier
// across app replicas. Selected by CACHE_DRIVER=redis; connection from REDIS_URL
// (default redis://redis:6379 — the stack_stacknet service name). Self-registers
// on blank-import; the brain's cache-aside code is unchanged.
package cacheredis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/togo-framework/cache"
	"github.com/togo-framework/togo"
)

const Name = "cache-redis"

func init() {
	cache.RegisterDriver("redis", func(k *togo.Kernel) (togo.Cache, error) {
		url := os.Getenv("REDIS_URL")
		if url == "" {
			url = "redis://redis:6379"
		}
		opt, err := redis.ParseURL(url)
		if err != nil {
			return nil, fmt.Errorf("cache-redis: bad REDIS_URL %q: %w", url, err)
		}
		rdb := redis.NewClient(opt)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			return nil, fmt.Errorf("cache-redis: ping %s: %w", url, err)
		}
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name, "url", url)
		}
		return &redisCache{rdb: rdb}, nil
	})
}

// redisCache implements togo.Cache over Redis. Values are stored as strings: the
// brain caches JSON strings (recall result sets) and short scalar epochs, so a
// string round-trips cleanly; other types are JSON-encoded defensively.
type redisCache struct{ rdb *redis.Client }

func (c *redisCache) Get(key string) (any, bool) {
	v, err := c.rdb.Get(context.Background(), key).Result()
	if err != nil {
		return nil, false // miss (redis.Nil) or transient error → treat as miss
	}
	return v, true
}

func (c *redisCache) Set(key string, value any, ttl time.Duration) {
	var s string
	switch t := value.(type) {
	case string:
		s = t
	case []byte:
		s = string(t)
	default:
		b, err := json.Marshal(value)
		if err != nil {
			return
		}
		s = string(b)
	}
	// ttl <= 0 means no expiry in go-redis; the brain always passes a positive TTL.
	c.rdb.Set(context.Background(), key, s, ttl)
}

func (c *redisCache) Delete(key string) {
	c.rdb.Del(context.Background(), key)
}
