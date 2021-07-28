// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package httpauth

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailureRateLimiter(t *testing.T) {
	const ip = "172.28.254.80"
	req := &http.Request{
		RemoteAddr: "10.5.2.23",
		Header: map[string][]string{
			"X-Forwarded-For": {fmt.Sprintf("%s, 192.168.80.25", ip)},
			"Forwarded":       {fmt.Sprintf("for=%s, for=172.17.5.10", ip)},
			"X-Real-Ip":       {ip},
		},
	}

	irl, err := newFailureRateLimiter(FailureRateLimiterConfig{MaxReqsSecond: 2, Burst: 3, NumLimits: 1})
	require.NoError(t, err)

	t.Run("succeesful requests doesn't count to rate limit the IP", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			allowed, succeeded, _, _ := irl.AllowReq(req)
			require.Truef(t, allowed, "AlloReq: request %d", i)
			succeeded()
		}

		for i := 1; i <= 10; i++ {
			allowed, succeeded, _, _ := irl.Allow(ip)
			require.Truef(t, allowed, "Allow: request %d", i)
			succeeded()
		}

		assert.False(t, irl.limiters.Contains(ip), "IP with successful requests doesn't have assigned a rate limiter")
	})

	t.Run("failed requests counts to rate limit the IP ", func(t *testing.T) {
		for i := 1; i <= 2; i++ {
			allowed, _, failed, _ := irl.AllowReq(req)
			require.Truef(t, allowed, "AllowReq: request %d", i)
			failed()
		}

		// Execute the last one allowed but using directly the key (i.e. IP).
		allowed, _, failed, _ := irl.Allow(ip)
		require.True(t, allowed, "Allow: request 3")
		failed()

		baseDelay := 2 * time.Second
		for i := 4; i <= 5; i++ {
			allowed, _, _, delay := irl.AllowReq(req)
			assert.Falsef(t, allowed, "AllowReq: request %d", i)
			assert.LessOrEqual(t, delay, baseDelay, "retry duration")

			baseDelay += time.Second / 2
		}

		// Execute another one not allowed but using directly the key (i.e. IP).
		allowed, _, _, _ = irl.Allow(ip)
		assert.False(t, allowed, "Allow: request 6")
	})

	t.Run("new key evicts the oldest one when the cache size is reached", func(t *testing.T) {
		key := "new-key-evicts-older-one"
		allowed, _, failed, _ := irl.Allow(key)
		require.True(t, allowed, "Allow")
		failed()
		assert.False(t, irl.limiters.Contains(ip), "previous key should have been removed")
	})

	t.Run("not allowed key is allowed again if it waits for the delay for the following request", func(t *testing.T) {
		key := "no-allowed-wait-allowed-again"
		for i := 1; i <= 3; i++ {
			allowed, _, failed, _ := irl.Allow(key)
			require.Truef(t, allowed, "Allow: call %d", i)
			failed()
		}

		allowed, _, _, delay := irl.Allow(key)
		assert.False(t, allowed, "Allow: call 4")
		assert.LessOrEqual(t, delay, 2*time.Second, "retry duration")

		time.Sleep(delay)
		allowed, succeeded, _, _ := irl.Allow(key)
		require.True(t, allowed, "Allow: call after wait")
		succeeded()
	})

	t.Run("succeeded removes an existing rate limit when it reaches the initial state", func(t *testing.T) {
		key := "will-be-at-init-state"
		assert.False(t, irl.limiters.Contains(key), "new key should be in the cache")

		allowed, _, failed, _ := irl.Allow(key)
		require.True(t, allowed, "Allow")
		// Failed operation counts for being rate-limited.
		failed()
		rateLimitStarted := time.Now() // this is because of the previous failed call.

		assert.True(t, irl.limiters.Contains(key), "failed key should be in the cache")

		allowed, succeeded, _, _ := irl.Allow(key)
		require.True(t, allowed, "Allow")
		assert.True(t, irl.limiters.Contains(key), "allow shouldn't remove the key from the cache")
		succeeded()

		// Wait the time until the rate-limiter associated with the key is back to
		// it's initial state. That's the time that can reserve an amount of
		// operations equal to the burst without any delay.
		time.Sleep(rateLimitStarted.Add(2 * time.Second).Sub(time.Now()))
		allowed, succeeded, _, _ = irl.Allow(key)
		require.True(t, allowed, "Allow")
		// Succeeded remove a tracked rate-limiter when it's to it's initial state.
		succeeded()
		// Verify that the rate-limiter has been untracked.
		assert.False(t, irl.limiters.Contains(key), "succeeded should remove the key from the cache")
	})

	t.Run("cheaters cannot use successful operations to by pas it", func(t *testing.T) {
		key := "cheater"

		for i := 1; i <= 2; i++ {
			allowed, _, failed, _ := irl.Allow(key)
			require.True(t, allowed, "Allow")
			// Failed operation counts for being rate-limited.
			failed()
		}

		// This operation is still allowed because of the bust allowance.
		allowed, succeeded, _, _ := irl.Allow(key)
		require.True(t, allowed, "Allow")
		// Succeeded operation doesn't count for being rate-limited
		succeeded()
		assert.True(t, irl.limiters.Contains(key),
			"one succeeded operation shouldn't remove the key from the cache when there is not delay",
		)

		// This operation is still allowed because of the bust allowance and because
		// the previous one succeeded, so it wasn't count by the rate-limited.
		allowed, _, failed, _ := irl.Allow(key)
		require.True(t, allowed, "Allow")
		failed()

		// This operation is rate limited because the rate limit has not been
		// cleared due to the last succeeded operations and it has surpassed the
		// burst allowance.
		allowed, _, _, _ = irl.Allow(key)
		assert.False(t, allowed, "Allow")
	})
}

func TestNewFailureRateLimiter(t *testing.T) {
	testCases := []struct {
		desc   string
		config FailureRateLimiterConfig
		retErr bool
	}{
		{
			desc:   "ok",
			config: FailureRateLimiterConfig{MaxReqsSecond: 5, Burst: 1, NumLimits: 1},
			retErr: false,
		},
		{
			desc:   "error zero max reqs per second",
			config: FailureRateLimiterConfig{MaxReqsSecond: 0, Burst: 2, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error negative max reqs per second",
			config: FailureRateLimiterConfig{MaxReqsSecond: -1, Burst: 5, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error zero burst",
			config: FailureRateLimiterConfig{MaxReqsSecond: 9, Burst: 0, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error negative burst",
			config: FailureRateLimiterConfig{MaxReqsSecond: 15, Burst: -5, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error zero num limits",
			config: FailureRateLimiterConfig{MaxReqsSecond: 5, Burst: 3, NumLimits: 0},
			retErr: true,
		},
		{
			desc:   "error negative num limits",
			config: FailureRateLimiterConfig{MaxReqsSecond: 5, Burst: 1, NumLimits: -3},
			retErr: true,
		},
		{
			desc:   "error negative max reqs per second and num limits",
			config: FailureRateLimiterConfig{MaxReqsSecond: -2, Burst: 10, NumLimits: -1},
			retErr: true,
		},
		{
			desc:   "error zero burst and negative num limits",
			config: FailureRateLimiterConfig{MaxReqsSecond: 3, Burst: -1, NumLimits: -1},
			retErr: true,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			irl, err := newFailureRateLimiter(tC.config)
			if tC.retErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, irl)
		})
	}
}
