package limiter

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBurstConsumption verifies that the bucket allows consuming
// up to its full capacity in a burst.
func TestBurstConsumption(t *testing.T) {
	const (
		capacity   = 5
		refillRate = 1.0 // 1 token per second
	)

	bucket := NewTokenBucket(capacity, refillRate)

	// Should be able to consume exactly capacity tokens immediately.
	for i := range capacity {
		if !bucket.Allow() {
			t.Fatalf("iteration %d: expected Allow() to return true within burst capacity", i)
		}
	}

	// Bucket is now empty; the next call should fail.
	if bucket.Allow() {
		t.Fatal("expected Allow() to return false after capacity exhausted")
	}
}

// TestRejectionWhenEmpty verifies that requests are rejected
// once the bucket is empty.
func TestRejectionWhenEmpty(t *testing.T) {
	const (
		capacity   = 3
		refillRate = 0.1 // very slow refill
	)

	bucket := NewTokenBucket(capacity, refillRate)

	// Drain the bucket.
	for i := range capacity {
		if !bucket.Allow() {
			t.Fatalf("iteration %d: expected Allow() to return true", i)
		}
	}

	// Immediately after draining, all further calls must be rejected.
	for i := range 10 {
		if bucket.Allow() {
			t.Fatalf("iteration %d: expected Allow() to return false on empty bucket", i)
		}
	}
}

// TestTokenRefill verifies that tokens are replenished over time
// according to the refill rate.
func TestTokenRefill(t *testing.T) {
	const (
		capacity   = 10
		refillRate = 5.0 // 5 tokens per second
	)

	bucket := NewTokenBucket(capacity, refillRate)

	// Drain the bucket completely.
	for range capacity {
		bucket.Allow()
	}

	if got := bucket.Tokens(); got != 0 {
		t.Fatalf("expected 0 tokens after draining, got %d", got)
	}

	// Sleep for 1 second: should refill ~5 tokens.
	time.Sleep(1 * time.Second)

	if got := bucket.Tokens(); got < 4 || got > 6 {
		t.Fatalf("expected ~5 tokens after 1s, got %d", got)
	}

	// Sleep another 1 second: total ~10 tokens (capped at capacity).
	time.Sleep(1 * time.Second)

	if got := bucket.Tokens(); got != capacity {
		t.Fatalf("expected bucket to refill to capacity %d, got %d", capacity, got)
	}
}

// TestRefillCapAtCapacity verifies that the bucket never exceeds
// its configured capacity.
func TestRefillCapAtCapacity(t *testing.T) {
	const (
		capacity   = 4
		refillRate = 100.0 // very fast refill
	)

	bucket := NewTokenBucket(capacity, refillRate)

	// Consume one token.
	bucket.Allow()

	// Wait long enough that the bucket should be well past capacity.
	time.Sleep(300 * time.Millisecond)

	if got := bucket.Tokens(); got != capacity {
		t.Fatalf("expected tokens capped at capacity %d, got %d", capacity, got)
	}
}

// TestAllowN verifies batch token consumption.
func TestAllowN(t *testing.T) {
	const (
		capacity   = 10
		refillRate = 1.0
	)

	bucket := NewTokenBucket(capacity, refillRate)

	// AllowN(5) should succeed.
	if !bucket.AllowN(5) {
		t.Fatal("expected AllowN(5) to succeed with full bucket")
	}

	// AllowN(6) should fail (only 5 remain).
	if bucket.AllowN(6) {
		t.Fatal("expected AllowN(6) to fail when only 5 tokens remain")
	}

	// AllowN(5) should now succeed (exactly 5 remain).
	if !bucket.AllowN(5) {
		t.Fatal("expected AllowN(5) to succeed with exactly 5 remaining")
	}

	// Bucket is empty; AllowN(1) should fail.
	if bucket.AllowN(1) {
		t.Fatal("expected AllowN(1) to fail on empty bucket")
	}
}

// TestAllowNNonPositive verifies that AllowN with n <= 0 always succeeds.
func TestAllowNNonPositive(t *testing.T) {
	bucket := NewTokenBucket(1, 1.0)

	if !bucket.AllowN(0) {
		t.Fatal("expected AllowN(0) to succeed")
	}
	if !bucket.AllowN(-1) {
		t.Fatal("expected AllowN(-1) to succeed")
	}
}

// TestReset verifies that Reset restores the bucket to full capacity.
func TestReset(t *testing.T) {
	const capacity = 8

	bucket := NewTokenBucket(capacity, 0.5)

	// Drain the bucket.
	for range capacity {
		bucket.Allow()
	}

	if got := bucket.Tokens(); got != 0 {
		t.Fatalf("expected 0 tokens after draining, got %d", got)
	}

	bucket.Reset()

	if got := bucket.Tokens(); got != capacity {
		t.Fatalf("expected %d tokens after Reset, got %d", capacity, got)
	}
}

// TestNewTokenBucketPanics verifies constructor validation.
func TestNewTokenBucketPanics(t *testing.T) {
	testCases := []struct {
		name       string
		capacity   int
		refillRate float64
	}{
		{"zero capacity", 0, 1.0},
		{"negative capacity", -5, 1.0},
		{"zero refill rate", 10, 0.0},
		{"negative refill rate", 10, -1.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for capacity=%d refillRate=%v", tc.capacity, tc.refillRate)
				}
			}()
			NewTokenBucket(tc.capacity, tc.refillRate)
		})
	}
}

// TestConcurrentAccess verifies thread safety under heavy concurrent load.
// Run with `go test -race` to detect data races.
func TestConcurrentAccess(t *testing.T) {
	const (
		capacity   = 100
		refillRate = 50.0
		goroutines = 32
		iterations = 1000
	)

	bucket := NewTokenBucket(capacity, refillRate)

	var (
		allowed  atomic.Int64
		rejected atomic.Int64
		wg       sync.WaitGroup
	)

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				if bucket.Allow() {
					allowed.Add(1)
				} else {
					rejected.Add(1)
				}
				// Also exercise Tokens() concurrently to stress the mutex.
				_ = bucket.Tokens()
			}
		}()
	}

	wg.Wait()

	total := allowed.Load() + rejected.Load()
	if total != goroutines*iterations {
		t.Fatalf("expected total attempts %d, got %d", goroutines*iterations, total)
	}

	t.Logf("concurrent test: allowed=%d rejected=%d", allowed.Load(), rejected.Load())
}

// TestConcurrentRefillAndConsume verifies correctness when some goroutines
// refill (via Reset) while others consume.
func TestConcurrentRefillAndConsume(_ *testing.T) {
	const (
		capacity   = 50
		refillRate = 20.0
		goroutines = 16
		iterations = 500
	)

	bucket := NewTokenBucket(capacity, refillRate)

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for range goroutines {
		// Consumers
		go func() {
			defer wg.Done()
			for range iterations {
				bucket.Allow()
			}
		}()

		// Resetters
		go func() {
			defer wg.Done()
			for range iterations {
				bucket.Reset()
			}
		}()
	}

	wg.Wait()
}

// BenchmarkAllow measures the throughput of Allow().
func BenchmarkAllow(b *testing.B) {
	bucket := NewTokenBucket(1000000, 1000000.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bucket.Allow()
	}
}

// BenchmarkConcurrentAllow measures throughput under concurrency.
func BenchmarkConcurrentAllow(b *testing.B) {
	bucket := NewTokenBucket(10000000, 10000000.0)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			bucket.Allow()
		}
	})
}

// ExampleAllow demonstrates typical usage.
func ExampleTokenBucket_Allow() {
	bucket := NewTokenBucket(3, 1.0) // capacity 3, refill 1 token/sec

	fmt.Println(bucket.Allow()) // true
	fmt.Println(bucket.Allow()) // true
	fmt.Println(bucket.Allow()) // true
	fmt.Println(bucket.Allow()) // false (empty)
	// Output:
	// true
	// true
	// true
	// false
}
