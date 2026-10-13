// failover_retry.go selects an alternate credential for in-flight failover
// (T19; semantic pattern from T17 plugins/workbuddy/failover_retry.go,
// adapted to trae's INTERNAL pool surface).
//
// When the non-streaming executor hits an account-level 4xx (401/403/404/405)
// or 429, the request itself is fine — only this credential is broken.
// Rather than failing the user, the executor tries the same request on a
// different pool credential. pickTraeNextAuth returns the next usable
// *auth.Auth via pool.PickExcluding(tried), skipping every UID already tried
// on this request (pool health — cooling/disabled — is evaluated inside
// PickExcluding, so a mid-request switch stays deterministic across the
// retry chain: highest-credits healthy credential first).
//
// Failure accounting (plan T19): every rotated failure is recorded via
// pool.Pool.Cooldown (pool.go:234) so the broken credential parks for
// subsequent requests too. The FINAL failure (budget exhausted or pool
// drained) keeps the historical applyCooldownOn accounting untouched —
// including 401 → Disable("session dead").
//
// Unlike workbuddy (per-account host RPC + per-(credential, model) cooldown
// table), trae needs no host calls here: pool entries already carry full
// *auth.Auth credentials.
package main

import (
	"net/http"
	"time"

	"github.com/mmqz/cpa-multi-plugins/plugins/trae/auth"
	"github.com/mmqz/cpa-multi-plugins/plugins/trae/pool"
)

// pickTraeNextAuthFn is the indirection the executor retry loop calls.
// Tests swap accountPool with an in-memory pool (the retry loop never needs
// host RPC); production always uses pickTraeNextAuth.
var pickTraeNextAuthFn = pickTraeNextAuth

// pickTraeNextAuth returns the next pool credential to retry on, skipping
// every UID in tried. Returns nil when the pool is nil/empty or every
// credential is tried, cooling, or disabled — the caller then surfaces the
// original upstream error (clean failure, no hang).
func pickTraeNextAuth(tried map[string]bool) *auth.Auth {
	if accountPool == nil {
		return nil
	}
	return pickTraeNextAuthOn(accountPool, tried)
}

// pickTraeNextAuthOn is the testable core of pickTraeNextAuth (pool
// injectable).
func pickTraeNextAuthOn(p *pool.Pool, tried map[string]bool) *auth.Auth {
	if p == nil {
		return nil
	}
	return p.PickExcluding(tried)
}

// noteRotationFailure records one rotated (non-final) upstream failure via
// pool.Pool.Cooldown (pool.go:234). Durations mirror the historical
// applyCooldownOn table without its terminal Disable: 429/404/405 park 60s
// (CoolSoft), 401/403 park 10m (CoolErr) — the credential stays in the pool
// and rejoins when the window lapses. Status 0 (transport/refresh-level,
// only used by the retry-loop prologue) parks 10m as suspect.
func noteRotationFailure(uid string, status int, _ string) {
	noteRotationFailureOn(accountPool, uid, status)
}

// noteRotationFailureOn is the testable core of noteRotationFailure (pool
// injectable).
func noteRotationFailureOn(p *pool.Pool, uid string, status int) {
	if p == nil || uid == "" {
		return
	}
	switch status {
	case http.StatusTooManyRequests, // 429
		http.StatusNotFound,         // 404
		http.StatusMethodNotAllowed: // 405
		p.Cooldown(uid, pool.CoolSoft, 60*time.Second,
			"same-request rotation after upstream denial — retry window 60s")
	default: // 401/403 and anything else suspect
		p.Cooldown(uid, pool.CoolErr, 10*time.Minute,
			"same-request rotation after upstream denial — retry window 10m")
	}
}
