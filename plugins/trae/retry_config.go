// retry_config.go owns the per-request retry-on-4xx budget for trae (T19,
// same semantic pattern as T17 plugins/workbuddy/retry_config.go).
//
// When a non-streaming execute hits an account-level 4xx (401/403/404/405)
// or 429 soft rate limit, the executor retries the same request on the next
// INTERNAL POOL credential (pool.PickExcluding) up to budget times before
// giving up. The budget is parsed from plugin config `retry_on_4xx:` on
// every Register/Reconfigure, default 10. Setting it to 0 disables
// same-request rotation (kill switch). An ABSENT key keeps the current
// budget so an unrelated reconfigure never silently lifts a
// `retry_on_4xx: 0` kill switch (缺省键保持, same as T17).
//
// Overlay rule (T19): rotation NEVER changes the default path —
// scheduler_pick stays defer-to-builtin unless scheduler_mode=session, and
// the single-shot accounting (applyCooldownOn) runs unchanged whenever no
// alternative credential exists. 400/402/413/5xx/transport errors never
// rotate: 400/413/4001-model-mismatch are request-level (every account
// rejects them identically), 402/5xx keep the cross-request cooldown path.
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// retryOn4xxDefault is the default per-request retry budget when
// `retry_on_4xx:` is absent from plugin config. 10 means up to 11 total
// attempts (initial + 10 rotations) per request, across distinct pool
// credentials.
const retryOn4xxDefault = 10

// retryOn4xxMin and retryOn4xxMax clamp user config to a sane range.
const (
	retryOn4xxMin = 0
	retryOn4xxMax = 10
)

var (
	retryOn4xx   = retryOn4xxDefault
	retryOn4xxMu sync.RWMutex
)

// setRetryOn4xx applies a parsed budget under the lock. Test helper; the
// configure path calls it only when the key is present.
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
// Negatives fall back to the default; oversize values cap at the max.
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
	if i := strings.Index(v, "#"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return n, true
}

// isAccountLevel4xx reports whether a status reflects an account-level
// problem (THIS credential is rejected) rather than a request-level one.
// 401/403/404/405 mean the upstream refused access for this account;
// 429 is usually per-account soft rate limiting. Retrying with a different
// pool credential has a real chance of succeeding. 400 is intentionally
// excluded — it almost always means our request body was malformed and
// would fail identically on every other credential.
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
// burning retry_on_4xx budget to try the same request on the next pool
// credential. Only account-level 4xx/429 rotate; everything else keeps the
// historical single-shot path.
func shouldRotateOnUpstreamErr(status int, _ string) bool {
	return isAccountLevel4xx(status)
}

// configureRetryRobustness parses the T19 robustness keys from the plugin
// lifecycle payload (Register/Reconfigure): `retry_on_4xx:` (absent key
// keeps the current budget; unparseable resets to the default) and
// `scheduler_mode:` (off/session; absent key keeps the current mode, so an
// unrelated reconfigure never silently drops an opted-in session mode).
// Called from handleMethod next to configureVariant; never touches the
// login-region surface (variant.go / intl flows own that).
func configureRetryRobustness(raw []byte) {
	if len(raw) == 0 {
		return
	}
	var req struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	if len(req.ConfigYAML) == 0 {
		return
	}
	// Line scan first (block style), then the YAML map for flow-style
	// one-liners — same dual view as configureVariant.
	seenBudget := false
	nextBudget := retryOn4xxDefault
	seenMode := false
	nextMode := schedulerModeOff
	applyLine := func(key, val string) {
		switch key {
		case "retry_on_4xx:":
			seenBudget = true
			if n, ok := parseRetryOn4xxLine("retry_on_4xx: " + val); ok {
				nextBudget = clampRetryOn4xx(n)
			} else {
				nextBudget = retryOn4xxDefault
			}
		case "scheduler_mode:":
			seenMode = true
			if v := strings.Trim(val, "\"'"); v == schedulerModeSession {
				nextMode = schedulerModeSession
			} else {
				nextMode = schedulerModeOff
			}
		}
	}
	for _, line := range strings.Split(string(req.ConfigYAML), "\n") {
		line = strings.TrimSpace(line)
		for _, key := range []string{"retry_on_4xx:", "scheduler_mode:"} {
			if strings.HasPrefix(line, key) {
				applyLine(key, strings.TrimSpace(strings.TrimPrefix(line, key)))
			}
		}
	}
	if m, ok := decodePluginConfigYAML(req.ConfigYAML); ok {
		if v, present := m["retry_on_4xx"]; present {
			seenBudget = true
			if n, ok := parseRetryOn4xxLine("retry_on_4xx: " + configScalarString(v)); ok {
				nextBudget = clampRetryOn4xx(n)
			} else {
				nextBudget = retryOn4xxDefault
			}
		}
		if v, present := m["scheduler_mode"]; present {
			seenMode = true
			if configScalarString(v) == schedulerModeSession {
				nextMode = schedulerModeSession
			} else {
				nextMode = schedulerModeOff
			}
		}
	}
	if seenBudget {
		setRetryOn4xx(nextBudget)
	}
	if seenMode {
		setSchedulerMode(nextMode)
	}
}
