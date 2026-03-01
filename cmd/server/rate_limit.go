package main

import (
	"sync"
	"time"
)

type rateBucket struct {
	tokens float64
	last   time.Time
}

type tokenBucketLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	state  map[string]rateBucket
	nowFn  func() time.Time
	ttl    time.Duration
}

func newTokenBucketLimiter(limitPerMinute int, burst int) *tokenBucketLimiter {
	if limitPerMinute <= 0 {
		limitPerMinute = 1
	}
	if burst <= 0 {
		burst = limitPerMinute
	}
	return &tokenBucketLimiter{
		rate:  float64(limitPerMinute) / 60.0,
		burst: float64(burst),
		state: map[string]rateBucket{},
		nowFn: func() time.Time { return time.Now().UTC() },
		ttl:   15 * time.Minute,
	}
}

func (l *tokenBucketLimiter) Allow(key string) bool {
	if key == "" {
		return false
	}
	now := l.nowFn()
	l.mu.Lock()
	defer l.mu.Unlock()

	for k, b := range l.state {
		if now.Sub(b.last) > l.ttl {
			delete(l.state, k)
		}
	}

	bucket, exists := l.state[key]
	if !exists {
		bucket = rateBucket{tokens: l.burst, last: now}
	}
	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed > 0 {
		bucket.tokens += elapsed * l.rate
		if bucket.tokens > l.burst {
			bucket.tokens = l.burst
		}
	}
	bucket.last = now

	if bucket.tokens < 1 {
		l.state[key] = bucket
		return false
	}
	bucket.tokens--
	l.state[key] = bucket
	return true
}
