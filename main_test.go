package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"
)

func setupTestRedis(tb testing.TB) *redis.Client {
	tb.Helper()

	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		tb.Fatalf("Failed to connect to Redis: %v. Make sure Redis is running on localhost:6379", err)
	}

	tb.Cleanup(func() {
		if err := client.Close(); err != nil {
			tb.Errorf("Failed to close Redis client: %v", err)
		}
	})

	return client
}

func cleanupTestKeys(tb testing.TB, client *redis.Client, keys ...string) {
	tb.Helper()

	if len(keys) == 0 {
		return
	}

	tb.Cleanup(func() {
		ctx := context.Background()
		for _, key := range keys {
			if err := client.Del(ctx, fmt.Sprintf("rate_limit:%s", key)).Err(); err != nil {
				tb.Errorf("Failed to delete key %s: %v", key, err)
			}
		}
	})
}

func generateTestKey(tb testing.TB, prefix string) string {
	tb.Helper()
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func TestNewRateLimiter(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)

	tests := []struct {
		name        string
		maxRequests int
		window      time.Duration
	}{
		{
			name:        "standard configuration",
			maxRequests: 10,
			window:      5 * time.Second,
		},
		{
			name:        "high rate limit",
			maxRequests: 1000,
			window:      1 * time.Minute,
		},
		{
			name:        "low rate limit",
			maxRequests: 1,
			window:      1 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewRateLimiter(client, tt.maxRequests, tt.window)

			if limiter == nil {
				t.Fatal("Expected non-nil RateLimiter")
			}

			if limiter.client != client {
				t.Error("Expected client to be set correctly")
			}

			if limiter.maxRequests != tt.maxRequests {
				t.Errorf("Expected maxRequests=%d, got %d", tt.maxRequests, limiter.maxRequests)
			}

			if limiter.window != tt.window {
				t.Errorf("Expected window=%v, got %v", tt.window, limiter.window)
			}
		})
	}
}

func TestRateLimiter_Allow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxRequests int
		window      time.Duration
		requests    int
		wantAllowed int
		wantDenied  int
	}{
		{
			name:        "within limit",
			maxRequests: 5,
			window:      1 * time.Second,
			requests:    3,
			wantAllowed: 3,
			wantDenied:  0,
		},
		{
			name:        "exceeds limit",
			maxRequests: 3,
			window:      10 * time.Second,
			requests:    5,
			wantAllowed: 3,
			wantDenied:  2,
		},
		{
			name:        "exactly at limit",
			maxRequests: 5,
			window:      1 * time.Second,
			requests:    5,
			wantAllowed: 5,
			wantDenied:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := setupTestRedis(t)
			testKey := generateTestKey(t, "test_allow")
			cleanupTestKeys(t, client, testKey)

			limiter := NewRateLimiter(client, tt.maxRequests, tt.window)
			ctx := context.Background()

			allowedCount := 0
			deniedCount := 0

			for i := range tt.requests {
				allowed, err := limiter.Allow(ctx, testKey)
				if err != nil {
					t.Fatalf("Request %d: unexpected error: %v", i+1, err)
				}

				if allowed {
					allowedCount++
				} else {
					deniedCount++
				}
			}

			if allowedCount != tt.wantAllowed {
				t.Errorf("Expected %d requests allowed, got %d", tt.wantAllowed, allowedCount)
			}

			if deniedCount != tt.wantDenied {
				t.Errorf("Expected %d requests denied, got %d", tt.wantDenied, deniedCount)
			}
		})
	}
}

func TestRateLimiter_Allow_DifferentKeys(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	testKey1 := generateTestKey(t, "test_diff_keys_1")
	testKey2 := generateTestKey(t, "test_diff_keys_2")
	cleanupTestKeys(t, client, testKey1, testKey2)

	limiter := NewRateLimiter(client, 2, 1*time.Second)
	ctx := context.Background()

	t.Run("key1 reaches limit", func(t *testing.T) {
		for i := range 2 {
			allowed, err := limiter.Allow(ctx, testKey1)
			if err != nil {
				t.Fatalf("User1 request %d: unexpected error: %v", i+1, err)
			}
			if !allowed {
				t.Errorf("User1 request %d: expected to be allowed", i+1)
			}
		}

		allowed, err := limiter.Allow(ctx, testKey1)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if allowed {
			t.Error("Expected user1's 3rd request to be denied")
		}
	})

	t.Run("key2 independent from key1", func(t *testing.T) {
		allowed, err := limiter.Allow(ctx, testKey2)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !allowed {
			t.Error("Expected user2's first request to be allowed despite user1's limit")
		}
	})
}

func TestRateLimiter_Allow_WindowExpiration(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	testKey := generateTestKey(t, "test_window_exp")
	cleanupTestKeys(t, client, testKey)

	window := 2 * time.Second
	limiter := NewRateLimiter(client, 2, window)
	ctx := context.Background()

	t.Run("requests denied after reaching limit", func(t *testing.T) {
		for i := range 2 {
			allowed, err := limiter.Allow(ctx, testKey)
			if err != nil {
				t.Fatalf("Request %d: unexpected error: %v", i+1, err)
			}
			if !allowed {
				t.Errorf("Request %d: expected to be allowed", i+1)
			}
		}

		allowed, err := limiter.Allow(ctx, testKey)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if allowed {
			t.Error("Expected 3rd request to be denied")
		}
	})

	t.Run("requests allowed after window expires", func(t *testing.T) {
		time.Sleep(window + 100*time.Millisecond)

		allowed, err := limiter.Allow(ctx, testKey)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !allowed {
			t.Error("Expected request to be allowed after window expiration")
		}
	})
}

func TestRateLimiter_Allow_SlidingWindow(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	testKey := generateTestKey(t, "test_sliding_window")
	cleanupTestKeys(t, client, testKey)

	window := 2 * time.Second
	limiter := NewRateLimiter(client, 3, window)
	ctx := context.Background()

	t.Run("sliding window denies requests at limit", func(t *testing.T) {
		for i := range 3 {
			allowed, err := limiter.Allow(ctx, testKey)
			if err != nil {
				t.Fatalf("Request %d: unexpected error: %v", i+1, err)
			}
			if !allowed {
				t.Errorf("Request %d: expected to be allowed", i+1)
			}
		}

		allowed, err := limiter.Allow(ctx, testKey)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if allowed {
			t.Error("Expected 4th request to be denied")
		}
	})

	t.Run("sliding window allows requests after expiration", func(t *testing.T) {
		time.Sleep(window + 100*time.Millisecond)

		allowed, err := limiter.Allow(ctx, testKey)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !allowed {
			t.Error("Expected request to be allowed after old requests expired")
		}
	})
}

func TestRateLimiter_Allow_ContextCancellation(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	testKey := generateTestKey(t, "test_ctx_cancel")
	cleanupTestKeys(t, client, testKey)

	limiter := NewRateLimiter(client, 5, 1*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := limiter.Allow(ctx, testKey)
	if err == nil {
		t.Error("Expected error with cancelled context, got nil")
	}
}

func TestRateLimiter_Allow_EmptyKey(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	testKey := ""
	cleanupTestKeys(t, client, testKey)

	limiter := NewRateLimiter(client, 5, 1*time.Second)
	ctx := context.Background()

	allowed, err := limiter.Allow(ctx, testKey)
	if err != nil {
		t.Fatalf("Unexpected error with empty key: %v", err)
	}

	if !allowed {
		t.Error("Expected request with empty key to be allowed")
	}
}

func TestRateLimiter_Allow_ConcurrentRequests(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	testKey := generateTestKey(t, "test_concurrent")
	cleanupTestKeys(t, client, testKey)

	const (
		maxRequests   = 10
		totalRequests = 15
	)

	limiter := NewRateLimiter(client, maxRequests, 1*time.Second)
	ctx := context.Background()

	var (
		allowedCount int
		deniedCount  int
		mu           sync.Mutex
	)

	g, ctx := errgroup.WithContext(ctx)

	for range totalRequests {
		g.Go(func() error {
			allowed, err := limiter.Allow(ctx, testKey)
			if err != nil {
				return err
			}

			mu.Lock()
			if allowed {
				allowedCount++
			} else {
				deniedCount++
			}
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if allowedCount != maxRequests {
		t.Errorf("Expected %d requests allowed, got %d", maxRequests, allowedCount)
	}

	expectedDenied := totalRequests - maxRequests
	if deniedCount != expectedDenied {
		t.Errorf("Expected %d requests denied, got %d", expectedDenied, deniedCount)
	}
}

func TestRateLimiter_Allow_MultipleUsers(t *testing.T) {
	t.Parallel()

	client := setupTestRedis(t)
	baseKey := generateTestKey(t, "test_multi_users")

	users := []string{
		fmt.Sprintf("%s_alice", baseKey),
		fmt.Sprintf("%s_bob", baseKey),
		fmt.Sprintf("%s_charlie", baseKey),
	}
	cleanupTestKeys(t, client, users...)

	limiter := NewRateLimiter(client, 3, 5*time.Second)
	ctx := context.Background()

	for _, user := range users {
		t.Run(user, func(t *testing.T) {
			for i := range 3 {
				allowed, err := limiter.Allow(ctx, user)
				if err != nil {
					t.Fatalf("Request %d: unexpected error: %v", i+1, err)
				}
				if !allowed {
					t.Errorf("Request %d: expected to be allowed", i+1)
				}
			}

			allowed, err := limiter.Allow(ctx, user)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if allowed {
				t.Error("Expected 4th request to be denied")
			}
		})
	}
}
