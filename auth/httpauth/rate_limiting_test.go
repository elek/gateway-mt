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

func TestIPRateLimiter(t *testing.T) {
	const ip = "172.28.254.80"
	req := &http.Request{
		RemoteAddr: "10.5.2.23",
		Header: map[string][]string{
			"X-Forwarded-For": {fmt.Sprintf("%s, 192.168.80.25", ip)},
			"Forwarded":       {fmt.Sprintf("for=%s, for=172.17.5.10", ip)},
			"X-Real-Ip":       {ip},
		},
	}

	irl, err := newIPRateLimiter(IPRateLimiterConfig{MaxReqsSecond: 2, Burst: 3, NumLimits: 1})
	require.NoError(t, err)

	for i := 1; i <= 3; i++ {
		assert.Truef(t, irl.AllowReq(req), "request %d", i)
	}

	assert.False(t, irl.AllowReq(req), "request 4")

	d, ok := irl.RetryAfter(ip)
	require.True(t, ok, "RetryAfter OK")
	assert.LessOrEqual(t, d, 2*time.Second, "RetryAfter duration")

	assert.False(t, irl.Allow(ip), "request 5 using directly IP value")

	assert.True(t, irl.Allow("192.168.1.50"), "request from another IP")
	assert.False(t, irl.ips.Contains(ip), "previous IP should have been removed")
}

func TestNewIPRateLimiter(t *testing.T) {
	testCases := []struct {
		desc   string
		config IPRateLimiterConfig
		retErr bool
	}{
		{
			desc:   "ok",
			config: IPRateLimiterConfig{MaxReqsSecond: 5, Burst: 1, NumLimits: 1},
			retErr: false,
		},
		{
			desc:   "error zero max reqs per second",
			config: IPRateLimiterConfig{MaxReqsSecond: 0, Burst: 2, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error negative max reqs per second",
			config: IPRateLimiterConfig{MaxReqsSecond: -1, Burst: 5, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error zero burst",
			config: IPRateLimiterConfig{MaxReqsSecond: 9, Burst: 0, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error negative burst",
			config: IPRateLimiterConfig{MaxReqsSecond: 15, Burst: -5, NumLimits: 1},
			retErr: true,
		},
		{
			desc:   "error zero num limits",
			config: IPRateLimiterConfig{MaxReqsSecond: 5, Burst: 3, NumLimits: 0},
			retErr: true,
		},
		{
			desc:   "error negative num limits",
			config: IPRateLimiterConfig{MaxReqsSecond: 5, Burst: 1, NumLimits: -3},
			retErr: true,
		},
		{
			desc:   "error negative max reqs per second and num limits",
			config: IPRateLimiterConfig{MaxReqsSecond: -2, Burst: 10, NumLimits: -1},
			retErr: true,
		},
		{
			desc:   "error zero burst and negative num limits",
			config: IPRateLimiterConfig{MaxReqsSecond: 3, Burst: -1, NumLimits: -1},
			retErr: true,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			irl, err := newIPRateLimiter(tC.config)
			if tC.retErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, irl)
		})
	}
}
