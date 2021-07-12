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

// IPRateLimiterConfig configures an IP rate limiter.
type IPRateLimiterConfig struct {
	MaxReqsSecond int `help:"maximum number of allowed request per second" default:"2" testDefault:"1"`
	Burst         int `help:"maximum number of allowed request to overpass the maximum request per second" default:"5" testDefault:"1"`
	NumLimits     int `help:"number of IPs whose rate limits we store" default:"1000" testDefault:"10"`
}

// ipRateLimiter imposes a request rate limit per IP.
type ipRateLimiter struct {
	ips   *lru.Cache
	rate  rate.Limit
	burst int
}

// newIPRateLimiter creates an ipRateLimiter returning an error if the
// c.MaxReqSecond, c.Burst or c.NumLimits are 0 or negative.
func newIPRateLimiter(c IPRateLimiterConfig) (*ipRateLimiter, error) {
	if c.MaxReqsSecond <= 0 {
		return nil, errs.New("MaxReqsSecond cannot be zero or negative")
	}

	if c.Burst <= 0 {
		return nil, errs.New("Burst cannot be zero or negative")
	}

	ips, err := lru.New(c.NumLimits)
	if err != nil {
		return nil, err
	}

	return &ipRateLimiter{
		ips:   ips,
		rate:  1 / rate.Limit(c.MaxReqsSecond), // minium interval between requests
		burst: c.Burst,
	}, nil
}

// Allow returns true if ip is allowed to make the request, otherwise false.
func (irl *ipRateLimiter) Allow(ip string) bool {
	v, ok := irl.ips.Get(ip)
	if ok {
		return v.(*rate.Limiter).Allow()
	}

	rl := rate.NewLimiter(irl.rate, irl.burst)
	irl.ips.Add(ip, rl)

	return rl.Allow()
}

// AllowReq gets the client IP from r and returns true if it's allowed to make
// the request otherwise false.
//
// It gets the IP of the client from the 'Forwarded', 'X-Forwarded-For', or
// 'X-Real-Ip' headers, returning it from the first header which are checked in
// that specific order; if any of those header exists then it gets the IP from
// r.RemoteAddr.
// It panics if r is nil.
func (irl *ipRateLimiter) AllowReq(r *http.Request) bool {
	ip, ok := server.GetIPFromHeaders(r)
	if !ok {
		ip = strings.SplitN(r.RemoteAddr, ":", 2)[0]
	}

	return irl.Allow(ip)
}

// RetryAfter returns the duration that the ip making the request must wait
// for being allowed again.
func (irl *ipRateLimiter) RetryAfter(ip string) (_ time.Duration, ok bool) {
	v, ok := irl.ips.Get(ip)
	if !ok {
		return 0, false
	}

	return v.(*rate.Limiter).Reserve().Delay(), true
}
