package series

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

type shard struct {
	mu sync.RWMutex
	m  map[string]time.Time
}

// Store tracks series observed during the TTL window. Keys must already be canonical.
type Store struct {
	shards      []shard
	ttl         time.Duration
	active      atomic.Int64
	stop        chan struct{}
	cleanupDone chan struct{}
}

func NewStore(ttl time.Duration, shardCount int) *Store {
	if shardCount < 1 {
		shardCount = 1
	}
	s := &Store{shards: make([]shard, shardCount), ttl: ttl, stop: make(chan struct{}), cleanupDone: make(chan struct{})}
	for i := range s.shards {
		s.shards[i].m = make(map[string]time.Time)
	}
	go s.cleanupLoop()
	return s
}

func (s *Store) Close() {
	close(s.stop)
	<-s.cleanupDone
}

func (s *Store) shard(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &s.shards[int(h.Sum32())%len(s.shards)]
}

func (s *Store) Touch(key string, now time.Time) {
	sh := s.shard(key)
	expires := now.Add(s.ttl)
	sh.mu.Lock()
	if _, exists := sh.m[key]; !exists {
		s.active.Add(1)
	}
	sh.m[key] = expires
	sh.mu.Unlock()
}

func (s *Store) Exists(key string, now time.Time) bool {
	sh := s.shard(key)
	sh.mu.RLock()
	expires, ok := sh.m[key]
	sh.mu.RUnlock()
	return ok && expires.After(now)
}

func (s *Store) Active() int64 { return s.active.Load() }

func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer close(s.cleanupDone)
	for {
		select {
		case now := <-ticker.C:
			s.expire(now)
		case <-s.stop:
			return
		}
	}
}

func (s *Store) expire(now time.Time) {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for key, expires := range sh.m {
			if !expires.After(now) {
				delete(sh.m, key)
				s.active.Add(-1)
			}
		}
		sh.mu.Unlock()
	}
}
