// Package snapshot serves bounded, versioned display data while refreshing it
// independently of request cancellation. It must never be used for accounting.
package snapshot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"bosapi/backend/internal/platform/logging"
	"github.com/redis/go-redis/v9"
)

const maxBytes = 2 << 20

var ErrPending = errors.New("snapshot is generating")
var ErrExpired = errors.New("snapshot has expired")

type Metadata struct {
	ID          string    `json:"snapshot_id,omitempty"`
	GeneratedAt time.Time `json:"generated_at,omitempty"`
	ValidUntil  time.Time `json:"valid_until,omitempty"`
	Status      string    `json:"status"`
}
type Value struct {
	Data json.RawMessage `json:"data"`
	Metadata
}
type Load func(context.Context) (json.RawMessage, time.Time, error)
type entry struct {
	value                        Value
	load                         Load
	accessed, attempted, retryAt time.Time
	failed                       bool
	done                         chan struct{}
}
type Cache struct {
	mu              sync.Mutex
	entries         map[string]*entry
	client          redis.UniversalClient
	prefix          string
	refresh, maxAge time.Duration
	capacity        int
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	busy            bool
	now             func() time.Time
}

func New(client redis.UniversalClient, prefix string, refresh, maxAge time.Duration, capacity int) *Cache {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cache{entries: make(map[string]*entry), client: client, prefix: prefix, refresh: refresh, maxAge: maxAge, capacity: capacity, ctx: ctx, cancel: cancel, now: time.Now}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.tick()
			}
		}
	}()
	return c
}
func (c *Cache) Close()    { c.mu.Lock(); c.cancel(); c.mu.Unlock(); c.wg.Wait() }
func Key(value any) string { b, _ := json.Marshal(value); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func (c *Cache) tick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, e := range c.entries {
		if e.done == nil && now.Sub(e.accessed) > time.Hour {
			delete(c.entries, key)
			continue
		}
		if e.load != nil && (now.Sub(e.value.GeneratedAt) >= c.refresh || !now.Before(e.value.ValidUntil)) && !now.Before(e.retryAt) {
			if c.startLocked(key, e, false) {
				return
			}
		}
	}
}

// Get waits at most one second for a cold snapshot. A valid successful value
// stays visible during manual refresh and failures. Invalid values are withheld.
func (c *Cache) Get(ctx context.Context, key string, refresh bool, load Load) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return Value{}, context.Canceled
	}
	e := c.entries[key]
	if e == nil {
		if len(c.entries) >= c.capacity {
			oldest := ""
			var at time.Time
			for k, v := range c.entries {
				if v.done == nil && (at.IsZero() || v.accessed.Before(at)) {
					oldest, at = k, v.accessed
				}
			}
			if oldest == "" {
				c.mu.Unlock()
				return Value{Metadata: Metadata{Status: "generating"}}, ErrPending
			}
			delete(c.entries, oldest)
		}
		e = &entry{}
		c.entries[key] = e
	}
	now := c.now()
	e.load = load
	e.accessed = now
	due := e.value.ID == "" || !now.Before(e.value.ValidUntil) || now.Sub(e.value.GeneratedAt) >= c.refresh || refresh
	if due {
		c.startLocked(key, e, refresh)
	}
	value, err := c.resultLocked(e)
	done := e.done
	c.mu.Unlock()
	if err == nil || done == nil {
		return value, err
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Value{}, ctx.Err()
	case <-timer.C:
		return value, err
	case <-done:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resultLocked(e)
}
func (c *Cache) resultLocked(e *entry) (Value, error) {
	v := e.value
	if v.ID == "" {
		v.Status = "generating"
		if e.failed {
			v.Status = "unavailable"
		}
		return v, ErrPending
	}
	if !c.now().Before(v.ValidUntil) {
		v.Data = nil
		v.Status = "expired"
		return v, ErrExpired
	}
	v.Status = "ready"
	if e.failed {
		v.Status = "refresh_failed"
	}
	if e.done != nil {
		v.Status = "refreshing"
	}
	// Each caller owns its bytes; a response must not mutate the cache.
	v.Data = append(json.RawMessage(nil), v.Data...)
	return v, nil
}
func (c *Cache) startLocked(key string, e *entry, force bool) bool {
	now := c.now()
	if c.ctx.Err() != nil || c.busy || e.done != nil || now.Before(e.retryAt) || now.Sub(e.attempted) < 30*time.Second {
		return false
	}
	e.attempted = now
	e.done = make(chan struct{})
	c.busy = true
	load := e.load
	c.wg.Add(1)
	go c.refreshEntry(key, e, load, force)
	return true
}
func (c *Cache) refreshEntry(key string, e *entry, load Load, force bool) {
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
	defer cancel()
	value, err := c.load(ctx, key, load, force)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		e.value = value
		e.failed = false
		e.retryAt = time.Time{}
	} else {
		if e.value.ID == "" && value.ID != "" {
			e.value = value
		}
		e.failed = !errors.Is(err, ErrPending)
		e.retryAt = c.now().Add(30 * time.Second)
	}
	close(e.done)
	e.done = nil
	c.busy = false
	if err != nil && !errors.Is(err, ErrPending) && !errors.Is(err, context.Canceled) {
		logging.Printf(c.ctx, "WARN", "admin.snapshot.refresh_failed", "kind=%s filter_hash=%s error=%v", c.prefix, key, err)
	}
}
func (c *Cache) redisContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 150*time.Millisecond)
}
func (c *Cache) read(ctx context.Context, key string) (Value, bool) {
	if c.client == nil {
		return Value{}, false
	}
	rc, cancel := c.redisContext(ctx)
	defer cancel()
	raw, err := c.client.Get(rc, c.prefix+key).Bytes()
	var v Value
	if err != nil || len(raw) > maxBytes || json.Unmarshal(raw, &v) != nil || v.ID == "" || !json.Valid(v.Data) || v.GeneratedAt.After(c.now().Add(time.Minute)) {
		return Value{}, false
	}
	return v, true
}
func (c *Cache) load(ctx context.Context, key string, load Load, force bool) (Value, error) {
	fallback, hasFallback := c.read(ctx, key)
	if hasFallback && c.now().Before(fallback.ValidUntil) && c.now().Sub(fallback.GeneratedAt) < c.refresh && !force {
		return fallback, nil
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Value{}, err
	}
	token := hex.EncodeToString(tokenBytes)
	lock := c.prefix + key + ":lock"
	locked := false
	if c.client != nil {
		rc, cancel := c.redisContext(ctx)
		acquired, err := c.client.SetNX(rc, lock, token, 5*time.Second).Result()
		cancel()
		if err == nil && !acquired {
			if v, ok := c.read(ctx, key); ok && c.now().Before(v.ValidUntil) {
				return v, nil
			}
			return Value{}, ErrPending
		}
		locked = err == nil && acquired
		if locked {
			defer func() {
				rc, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
				defer cancel()
				_ = c.client.Eval(rc, `if redis.call('get',KEYS[1]) == ARGV[1] then return redis.call('del',KEYS[1]) end return 0`, []string{lock}, token).Err()
			}()
		}
	}
	started := c.now()
	data, validUntil, err := load(ctx)
	if err != nil {
		if hasFallback && c.now().Before(fallback.ValidUntil) {
			return fallback, err
		}
		return Value{}, err
	}
	if err = ctx.Err(); err != nil {
		return Value{}, err
	}
	if len(data) > maxBytes-1024 || !json.Valid(data) {
		return Value{}, errors.New("snapshot exceeds size limit or contains invalid JSON")
	}
	deadline := started.Add(c.maxAge)
	if validUntil.IsZero() || validUntil.After(deadline) {
		validUntil = deadline
	}
	value := Value{Data: data, Metadata: Metadata{ID: token, GeneratedAt: started, ValidUntil: validUntil, Status: "ready"}}
	if locked {
		raw, err := json.Marshal(value)
		if err != nil {
			return Value{}, err
		}
		rc, cancel := c.redisContext(ctx)
		result, err := c.client.Eval(rc, `if redis.call('get',KEYS[1]) ~= ARGV[1] then return 0 end
 redis.call('set',KEYS[2],ARGV[2],'EX',86400)
 redis.call('zadd',KEYS[3],ARGV[3],KEYS[2]); redis.call('expire',KEYS[3],86400)
 local extra=redis.call('zcard',KEYS[3])-tonumber(ARGV[4])
 if extra>0 then
 local old=redis.call('zrange',KEYS[3],0,extra-1)
 for _,key in ipairs(old) do redis.call('del',key);redis.call('zrem',KEYS[3],key) end
 end
 return 1`, []string{lock, c.prefix + key, c.prefix + "index"}, token, raw, started.UnixMilli(), c.capacity).Int()
		cancel()
		if err == nil && result == 0 {
			return Value{}, ErrPending
		}
	}
	return value, nil
}
