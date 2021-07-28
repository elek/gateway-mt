// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package httpauth

import (
	"net/http"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/zeebo/errs"
	"golang.org/x/time/rate"

	"storj.io/gateway-mt/pkg/server"
)

// FailureRateLimiterConfig configures a failure rate limiter.
type FailureRateLimiterConfig struct {
	MaxReqsSecond int `help:"maximum number of allowed operations per second starting when first failure operation happens" default:"2" testDefault:"1"`
	Burst         int `help:"maximum number of allowed operations to overpass the maximum operations per second" default:"2" testDefault:"1"`
	NumLimits     int `help:"maximum number of keys/rate-limit pairs stored in the LRU cache" default:"1000" testDefault:"10"`
}

// failureRateLimiter imposes a request rate limit per tracked key when the
// operation is marked as failed
type failureRateLimiter struct {
	limiters *lru.Cache
	limit    rate.Limit
	burst    int
}

// newFailureRateLimiter creates an FailureRateLimiter returning an error if the
// c.MaxReqSecond, c.Burst or c.NumLimits are 0 or negative.
func newFailureRateLimiter(c FailureRateLimiterConfig) (*failureRateLimiter, error) {
	if c.MaxReqsSecond <= 0 {
		return nil, errs.New("MaxReqsSecond cannot be zero or negative")
	}

	if c.Burst <= 0 {
		return nil, errs.New("Burst cannot be zero or negative")
	}

	limiters, err := lru.New(c.NumLimits)
	if err != nil {
		return nil, err
	}

	return &failureRateLimiter{
		limiters: limiters,
		limit:    1 / rate.Limit(c.MaxReqsSecond), // minium interval between requests
		burst:    c.Burst,
	}, nil
}

// Allow returns true and non-nil succeeded and failed, and a zero delay if key
// is allowed to perform an operation, otherwise false, succeeded and failed are
// nil, and delay is greater than 0.
//
// key is allowed to make the request if it isn't tracked or it's tracked but it
// hasn't reached the limit.
//
// When key isn't tracked, it gets tracked when failed is executed and
// subsequent Allow calls with key will be rate-limited. succeeded untrack the
// key when the rate-limit doesn't apply anymore. For these reason the caller
// MUST always call succeeded or failed when true is returned.
func (irl *failureRateLimiter) Allow(key string) (allowed bool, succeeded func(), failed func(), delay time.Duration) {
	v, ok := irl.limiters.Get(key)
	if ok {
		rl := v.(*rateLimiter)
		allowed, delay, rollback := rl.Allow()
		if !allowed {
			return false, nil, nil, delay
		}

		// When the key is already tracked, failed func doesn't have to do anything.
		return true, func() {
			// The operations has succeeded, hence rollback the consumed rate-limit
			// allowance.
			rollback()

			if rl.IsOnInitState() {
				irl.limiters.Remove(key)
			}
		}, func() {}, 0
	}

	return true, func() {}, func() {
		// The operation is failed, hence we start to rate-limit the key.
		rl := newRateLimiter(irl.limit, irl.burst)
		irl.limiters.Add(key, rl)
		// Consume one operation, which is this failed one.
		rl.Allow()
	}, 0
}

// AllowReq gets uses the client IP from r as key to call the Allow method.
//
// It gets the IP of the client from the 'Forwarded', 'X-Forwarded-For', or
// 'X-Real-Ip' headers, returning it from the first header which are checked in
// that specific order; if any of those header exists then it gets the IP from
// r.RemoteAddr.
// It panics if r is nil.
func (irl *failureRateLimiter) AllowReq(r *http.Request) (allowed bool, succeeded func(), failed func(), delay time.Duration) {
	ip, ok := server.GetIPFromHeaders(r)
	if !ok {
		ip = strings.SplitN(r.RemoteAddr, ":", 2)[0]
	}

	return irl.Allow(ip)
}

// rateLimiter is a wrapper around rate.Limiter to suit the failureRateLimiter
// requirements.
type rateLimiter struct {
	limiter    *rate.Limiter
	delayUntil time.Time
}

func newRateLimiter(limit rate.Limit, burst int) *rateLimiter {
	return &rateLimiter{
		limiter: rate.NewLimiter(limit, burst),
	}
}

// IsOnInitState returns true if the rate-limiter is back to its full allowance
// such is when it is created.
func (rl *rateLimiter) IsOnInitState() bool {
	now := time.Now()
	rsvt := rl.limiter.ReserveN(now, rl.limiter.Burst())
	// Cancel immediately the reservation because we are only interested in the
	// finding out the delay of executing as many operations as burst.
	// 	Using the same time when the reservation was created allows to cancel
	// the reservation despite it's already consumed at this moment.
	rsvt.CancelAt(now)

	return rsvt.Delay() == 0
}

// Allow returns true when the operations is allowed to be performed, and also
// returns a rollback function for rolling it back the consumed token for not
/// counting to the rate-limiting of future calls. Otherwise it returns false
// and the time duration that the caller must wait until being allowed to
// perform the operation and rollback is nil because there isn't an allowed
// operations to roll it back.
func (rl *rateLimiter) Allow() (_ bool, _ time.Duration, rollback func()) {
	now := time.Now()

	// Delay is zero when previous call was allowed.
	if rl.delayUntil.IsZero() {
		rsvt := rl.limiter.ReserveN(now, 1)
		if d := rsvt.Delay(); d > 0 {
			// If there is an imposed delay, it means that the reserved token cannot
			// be consumed right now, so isn't allowed. We keep the delay time for not
			// consuming more tokens in subsequent calls.
			rl.delayUntil = now.Add(d)
			return false, d, nil
		}

		// The reserved token can be consumed right now, so it's allowed.
		return true, 0, func() {
			// 	Using the same time when the reservation was created allows to cancel
			// the reservation despite it's already consumed at this moment.
			rsvt.CancelAt(now)
		}
	}

	//  Not allowed because the reserved token is still not allowed to be consumed.
	if rl.delayUntil.After(now) {
		return false, rl.delayUntil.Sub(now), nil
	}

	// The reserved token can be consumed now because the delay is over, hence
	// it's allowed.
	rl.delayUntil = time.Time{}
	return true, 0, func() {}
}
