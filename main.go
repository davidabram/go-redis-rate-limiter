package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	client      *redis.Client
	maxRequests int
	window      time.Duration
}

func NewRateLimiter(client *redis.Client, maxRequests int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		client:      client,
		maxRequests: maxRequests,
		window:      window,
	}
}

func (rl *RateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := fmt.Sprintf("rate_limit:%s", key)
	now := time.Now().UnixNano()
	windowStart := now - rl.window.Nanoseconds()

	pipe := rl.client.Pipeline()

	pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%d", windowStart))

	countCmd := pipe.ZCard(ctx, redisKey)

	pipe.ZAdd(ctx, redisKey, redis.Z{
		Score:  float64(now),
		Member: now,
	})

	pipe.Expire(ctx, redisKey, rl.window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	count := countCmd.Val()
	return count < int64(rl.maxRequests), nil
}

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	maxRequests := 5

	timeframeSeconds := 10

	timeframe := time.Duration(timeframeSeconds) * time.Second

	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	fmt.Println("Connected to Redis successfully!")

	limiter := NewRateLimiter(rdb, maxRequests, timeframe)

	userID := "david"

	fmt.Printf("\nTesting rate limiter (%d requests per %d seconds):\n\n", maxRequests, timeframeSeconds)

	for i := 1; i <= 8; i++ {
		allowed, err := limiter.Allow(ctx, userID)
		if err != nil {
			log.Printf("Error checking rate limit: %v", err)
			continue
		}

		if allowed {
			fmt.Printf("Request %d: ALLOWED\n", i)
		} else {
			fmt.Printf("Request %d: DENIED (rate limit exceeded) \n", i)
		}

		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("\nWaiting %d seconds for rate limit to reset...\n", timeframeSeconds)
	time.Sleep(timeframe)

	allowed, err := limiter.Allow(ctx, userID)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	if allowed {
		fmt.Println("Request after reset: ALLOWED")
	} else {
		fmt.Println("Request after reset: DENIED")
	}
}
