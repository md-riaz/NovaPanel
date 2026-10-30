package web

import (
	"crypto/sha256"
	"math"
	"sync"
	"time"
)

const maxTransportBuckets = 4096

type transportBucket struct {
	tokens  float64
	updated time.Time
}

type transportLimiter struct {
	mu         sync.Mutex
	buckets    map[[32]byte]transportBucket
	rps, burst float64
}

func (l *transportLimiter) refill(b transportBucket, now time.Time) transportBucket {
	if now.After(b.updated) {
		b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.updated).Seconds()*l.rps)
		b.updated = now
	}
	return b
}

func (l *transportLimiter) allow(token string, now time.Time) bool {
	key := sha256.Sum256([]byte(token))
	l.mu.Lock()
	defer l.mu.Unlock()
	b, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= maxTransportBuckets {
			// Only fully refilled buckets can be forgotten without restoring spent allowance.
			for k, old := range l.buckets {
				if l.refill(old, now).tokens >= l.burst {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= maxTransportBuckets {
				return false
			}
		}
		b = transportBucket{tokens: l.burst, updated: now}
	}
	b = l.refill(b, now)
	allowed := b.tokens >= 1
	if allowed {
		b.tokens--
	}
	l.buckets[key] = b
	return allowed
}
