// retry_config.go owns the per-request retry-on-4xx budget (T18 port of R1
// qoderwork/retry_config.go + the rotation decision from R1
// qoderwork/accountFailover.go:shouldRotateOnUpstreamErr/isAccountLevel4xx,
// mirroring the T17 workbuddy port semantics exactly).
//
// When a request hits an account-level 4xx (401/403/404/405) or 429 soft
// rate limit, the executor may try the same request on the next available
// account up to budget times before giving up. The budget is parsed from
// plugin config `retry_on_4xx:` on every configure()/reconfigure call, with
// 10 as the default. Setting it to 0 disables on-request account failover
// (the old behavior) — useful as a kill-switch when a global outage makes
// retries destructive.
//
// Overlay rule (T18): account-level 4xx rotates the ACCOUNT within one
// request; model-level throttling keeps the R2 per-(credential, model)
// cooldown table (cooldown.go: recordUpstreamFailure) untouched. 5xx/0/402
// are intentionally NOT rotated here — they keep the cross-request cooldown
// path. R1 qoderwork additionally owned a fixed per-account cooldown
// (accountFailover.go) which R2 never had; it is NOT ported — the
// per-(credential, model) table is the only cooldown surface here.
package main

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// retryOn4xxDefault is the default per-request retry budget when
// `retry_on_4xx:` is absent from plugin config. 10 means up to 11 total
// attempts (initial + 10 retries) per request, across distinct accounts.
const retryOn4xxDefault = 10

// retryOn4xxMin and retryOn4xxMax clamp user config to a sane range.
// Upper bound 10 lets large account pools be walked within one request
// while still capping a misconfiguration that could otherwise spin the
// request forever when the upstream is universally 4xx-failing.
const (
	retryOn4xxMin = 0
	retryOn4xxMax = 10
)

var (
	retryOn4xx   = retryOn4xxDefault
	retryOn4xxMu sync.RWMutex
)

// setRetryOn4xx applies a parsed budget under the lock. Caller is
// expected to have already clamped the value.
func setRetryOn4xx(n int) {
	retryOn4xxMu.Lock()
	retryOn4xx = n
	retryOn4xxMu.Unlock()
}

// loadedRetryOn4xx returns the currently configured budget (default 10
// when nothing has been parsed yet).
func loadedRetryOn4xx() int {
	retryOn4xxMu.RLock()
	defer retryOn4xxMu.RUnlock()
	return retryOn4xx
}

// clampRetryOn4xx returns n clamped to [retryOn4xxMin, retryOn4xxMax].
// Out-of-range values fall back: negatives to the default, oversize to
// the max; the call site decides whether to log the substitution.
func clampRetryOn4xx(n int) int {
	if n < retryOn4xxMin {
		return retryOn4xxDefault
	}
	if n > retryOn4xxMax {
		return retryOn4xxMax
	}
	return n
}

// parseRetryOn4xxLine extracts the integer from a `retry_on_4xx: N`
// config_yaml line. Returns (value, ok). Whitespace and inline comments
// after the number are tolerated.
func parseRetryOn4xxLine(line string) (int, bool) {
	v := strings.TrimSpace(strings.TrimPrefix(line, "retry_on_4xx:"))
	v = strings.Trim(v, "\"'")
	if v == "" {
		return 0, false
	}
	// Strip a trailing YAML comment ("# something").
	if i := strings.Index(v, "#"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return n, true
}

// isAccountLevel4xx reports whether a 4xx status reflects an account-level
// problem (the credential/endpoint on this account is wrong) rather than a
// request-level problem. 401/403/404/405 mean the upstream rejected access
// for THIS account: token expired, no permission, route missing, or method
// not allowed. Retrying with a different account has a real chance of
// succeeding. 400 is intentionally excluded — it almost always means the
// request body was malformed by us and would fail identically on every
// other account.
//
// 429 (Too Many Requests / soft rate limit) is INCLUDED: the upstream soft
// rate limit is usually per-account, so rotating to the next candidate is
// the cheapest way to recover inside a single request. The model-level
// cooldown still applies in parallel via recordUpstreamFailure — i.e. a
// 429-triggered same-request rotation also parks the failed (credential,
// model) pair so subsequent requests route away from it.
func isAccountLevel4xx(status int) bool {
	switch status {
	case http.StatusUnauthorized, // 401
		http.StatusForbidden,        // 403
		http.StatusNotFound,         // 404
		http.StatusMethodNotAllowed, // 405
		http.StatusTooManyRequests:  // 429 — same-request rotation
		return true
	}
	return false
}

// shouldRotateOnUpstreamErr reports whether one upstream failure is worth
// burning retry_on_4xx budget to try the same request on the next account.
// Account-level 4xx (401/403/404/405) and 429 rotate; an in-stream error
// frame on a 200 body (status == 200, upstream refused in-band) rotates
// only when the body carries account-level quota/rate-limit markers.
// 5xx/0/402 keep the cross-request cooldown path and never rotate here.
func shouldRotateOnUpstreamErr(status int, errBody string) bool {
	if status == http.StatusOK {
		return isHardCreditError(status, errBody) || isSoftRateLimit(status, errBody)
	}
	return isAccountLevel4xx(status)
}
