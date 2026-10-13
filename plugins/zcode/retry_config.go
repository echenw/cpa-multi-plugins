// retry_config.go owns the per-request retry-on-4xx budget (T20 port of R1
// zcode/executor.go:48's fixed `for attempt < 3` loop, made configurable).
//
// R1 zcode retries a failed request on the next account at most 3 times
// (fixed). Here the loop bound is read from plugin config `retry_on_4xx:`
// on every configure()/reconfigure call, defaulting to 3 to align with R1's
// fixed 3. Setting it to 0 disables same-request account failover (the old
// R2 behavior) — useful as a kill-switch when a global outage makes retries
// destructive.
//
// Versus R2 this is NEW account-level rotation (R2 had none on this path),
// not an equivalent port. Overlay rule (T20): account-level 4xx rotates the
// ACCOUNT within one request; model-level throttling keeps the existing
// per-(credential, model) cooldown table (cooldown.go) untouched. Business
// 400, 5xx/0/402 never rotate here — they keep the cross-request path.
// Off-peak ticketed attempts never rotate either (see main.go).
package main

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// retryOn4xxDefault is the default per-request retry budget when
// `retry_on_4xx:` is absent from plugin config. 3 means up to 4 total
// attempts (initial + 3 retries) per request, aligned with R1 zcode's fixed
// `for attempt < 3` (initial + up-to-3 account switches).
const retryOn4xxDefault = 3

// retryOn4xxMin and retryOn4xxMax clamp user config to a sane range,
// matching R1 (workbuddy/retry_config.go) 0–10. Upper bound 10 caps a
// misconfiguration that could otherwise spin the request when the upstream
// is universally 4xx-failing.
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

// loadedRetryOn4xx returns the currently configured budget (default 3
// when nothing has been parsed yet).
func loadedRetryOn4xx() int {
	retryOn4xxMu.RLock()
	defer retryOn4xxMu.RUnlock()
	return retryOn4xx
}

// clampRetryOn4xx returns n clamped to [retryOn4xxMin, retryOn4xxMax].
// Negatives fall back to the default, oversize to the max.
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
// problem (the credential on this account is rejected) rather than a
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
// cooldown still applies in parallel via recordUpstreamFailure.
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
// Port of R1 zcode/failover.go:82: account-level 4xx (401/403/404/405) and
// 429 rotate by status; anything else rotates only when the body carries
// account-level markers (unauthorized/forbidden/rate limit/quota/
// insufficient). 5xx/0/402 and business 400 never rotate here.
func shouldRotateOnUpstreamErr(statusCode int, errMsg string) bool {
	if statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden ||
		statusCode == http.StatusNotFound ||
		statusCode == http.StatusMethodNotAllowed ||
		statusCode == http.StatusTooManyRequests {
		return true
	}
	lower := strings.ToLower(errMsg)
	if strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "insufficient") {
		return true
	}
	return false
}
