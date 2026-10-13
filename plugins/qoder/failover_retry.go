// failover_retry.go selects an alternate account for in-flight failover
// (T18 port of R1 qoderwork/failover_retry.go:pickNextAuth, adapted to the
// R2 account surface, mirroring the T17 workbuddy port semantics exactly).
//
// When the executor hits an account-level 4xx (401/403/404/405) or 429, the
// request itself is fine — only this account is broken. Rather than failing
// the user, the executor tries the same request on a different qoderwork
// account. pickNextAuth returns the next usable (authID, storedAuth) pair in
// host-reported order, skipping the current failing account and any that are
// disabled, model-cooling on this request's model, or unloadable.
//
// Unlike R1 qoderwork (which also skipped fixed-cooldown and 「测试」
// quarantined accounts from its own accountFailover.go tables), this port
// has no per-account cooldown table to consult: R2 never had one, and the
// per-(credential, model) table (cooldown.go) is read-only from here via
// modelIsCooling. A retry reuses the same QoderEncoding-encoded body with
// fresh per-account COSY headers (applyCosyHeaders) against
// endpointChatFor of the new account.
//
// Overlay rule (T18): this file owns ACCOUNT rotation. Model-level
// throttling stays with the R2 per-(credential, model) cooldown table
// (cooldown.go): a pair cooling on this model is skipped here, and the
// table itself is never written from this file.
package main

import (
	"strings"
)

// pickNextAuthFn is the indirection the executor retry loops call. Tests
// stub it with in-memory accounts (the real pickNextAuth needs host RPC);
// production always uses pickNextAuth.
var pickNextAuthFn = pickNextAuth

// pickNextAuth returns the next qoderwork account to retry on after
// currentAuthID failed on model. It is best-effort: any error reading the
// host's auth list, loading a candidate's bundle, or evaluating a
// credential's state results in ok=false so the caller falls back to
// surfacing the original error to the user.
//
// ok=false means no alternate account was usable; the caller should stop
// retrying and propagate the last upstream error.
func pickNextAuth(currentAuthID, model string) (nextAuthID string, nextSA *storedAuth, ok bool) {
	currentAuthID = strings.TrimSpace(currentAuthID)
	files, err := hostAuthList()
	if err != nil || len(files) == 0 {
		return "", nil, false
	}

	// First pass: find the first file-entry whose host-reported ID is
	// NOT currentAuthID and passes the cheap filters (disabled,
	// model-cooling on this request's model). Order is host-provided so
	// successive retries walk the same predictable path — unlike
	// scheduler.pick, in-flight failover does NOT reorder by credits,
	// because a mid-request switch must stay deterministic across the
	// retry chain.
	for _, f := range files {
		id := strings.TrimSpace(f.ID)
		if id == "" {
			continue
		}
		if id == currentAuthID {
			continue
		}
		if f.Disabled {
			continue
		}
		if modelIsCooling(id, model) {
			continue
		}
		// Skip candidates with no usable auth index — we can't load
		// their stored body, so retrying on them would fail
		// identically and burn a budget slot.
		if strings.TrimSpace(f.AuthIndex) == "" {
			continue
		}

		sa, _, loadErr := hostAuthGetBundle(f.AuthIndex)
		if loadErr != nil || sa == nil {
			// Treat as not-usable; keep walking. Don't surface the
			// load error — there may be a viable next candidate.
			continue
		}

		// Defensive: ensure the stored auth actually carries a token
		// worth retrying on. An empty Access/Refresh would 401
		// identically (COSY would also fail to build a session).
		if strings.TrimSpace(sa.Auth.AccessToken) == "" && strings.TrimSpace(sa.Auth.RefreshToken) == "" {
			continue
		}

		return id, sa, true
	}
	return "", nil, false
}

// qoderChatBaseOverride, when non-empty, replaces the region-derived chat
// endpoint for every account. Test-only seam (httptest mock upstream): it
// lets hermetic tests drive handleExecExecute/handleExecStream against a
// local server that distinguishes accounts by the cosy-user header.
var qoderChatBaseOverride = ""

// setQoderChatBase temporarily overrides the chat endpoint for tests;
// returns a restore func.
func setQoderChatBase(s string) func() {
	old := qoderChatBaseOverride
	qoderChatBaseOverride = s
	return func() { qoderChatBaseOverride = old }
}
