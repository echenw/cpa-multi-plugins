// session_auth.go implements per-conversation account routing for trae
// (T19; semantic pattern from T17 plugins/workbuddy/session_auth.go,
// adapted to trae's defer-by-default scheduler).
//
// scheduler_mode=session spreads conversations across the available trae
// accounts: the SAME conversation is pinned to the SAME account for a bounded
// window (sessionStickinessTTL, default 1h), while DIFFERENT conversations are
// assigned to different accounts (unbound-account first, then round-robin).
// Default stays off — scheduler_pick defers everything to the host built-in
// scheduler (Handled: false), byte-identical to the pre-T19 behavior.
//
// Session identity comes from the scheduler pick request Options, which the
// host fills before every pick: Options.Metadata carries the
// host-derived/execution session identity, Options.Headers carries explicit
// client session headers. When no session signal is present, routing falls
// back to the first usable candidate.
//
// Candidate IDs are host auth-record IDs (the only thing scheduler_pick may
// return as AuthID). Pool health (cooling/disabled from executor rotation
// via Pool.Cooldown, quota freeze) filters usability through the UID derived
// per candidate (Attributes["uid"] / Metadata["uid"], else the candidate ID
// itself); unknown UIDs are treated as usable (fail-open — session mode must
// never route worse than the defer default).
package main

import (
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Scheduler modes for trae. Unlike workbuddy there is no "credits" mode:
// trae deliberately does not take over routing (Route/pool default behavior
// unchanged — T19 Must NOT). Only opt-in session stickiness is offered.
const (
	schedulerModeOff     = "off"
	schedulerModeSession = "session"
)

var (
	schedulerMode   = schedulerModeOff
	schedulerModeMu sync.RWMutex
)

// setSchedulerMode applies a mode and returns a restore func (test helper;
// the configure path calls it only when the key is present).
func setSchedulerMode(mode string) func() {
	schedulerModeMu.Lock()
	old := schedulerMode
	schedulerMode = mode
	schedulerModeMu.Unlock()
	return func() {
		schedulerModeMu.Lock()
		schedulerMode = old
		schedulerModeMu.Unlock()
	}
}

// loadedSchedulerMode returns the currently configured scheduler mode
// (default off).
func loadedSchedulerMode() string {
	schedulerModeMu.RLock()
	defer schedulerModeMu.RUnlock()
	return schedulerMode
}

// Session metadata keys written by the host.
// Kept as local constants instead of importing the SDK executor package so the
// plugin never depends on a specific CLIProxyAPI version for routing keys.
const (
	executionSessionIDMetadataKey = "execution_session_id"
	derivedSessionIDMetadataKey   = "derived_session_id"
)

// sessionStickinessTTL bounds how long one conversation keeps its account.
// After expiry the binding is released and the next pick re-assigns (variable
// so tests can shrink it; default 1h per product requirement).
var sessionStickinessTTL = time.Hour

// sessionPruneInterval bounds how often expired bindings are swept.
const sessionPruneInterval = 5 * time.Minute

// sessionAuthBinding pins one conversation to one account until ExpiresAt.
type sessionAuthBinding struct {
	AuthID    string
	ExpiresAt time.Time
}

var (
	// sessionAuthMu guards sessionAuthBindings and sessionRR.
	sessionAuthMu sync.RWMutex
	// sessionAuthBindings maps a session key (see extractSessionKey) to its
	// pinned account. Entries are created on first pick of a conversation and
	// removed by the pruner after expiry.
	sessionAuthBindings = make(map[string]sessionAuthBinding)
	// sessionRR is a monotonically increasing counter used to spread new
	// conversations across accounts when every usable account already has
	// bindings.
	sessionRR uint64
)

func init() {
	go func() {
		ticker := time.NewTicker(sessionPruneInterval)
		defer ticker.Stop()
		for range ticker.C {
			pruneSessionBindings()
		}
	}()
}

// normalizeSessionID validates and trims a raw session identifier, mirroring
// the host's session.NormalizeExplicitID contract: opaque printable values up
// to 256 bytes; control-bearing or oversized values are rejected.
func normalizeSessionID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 256 {
		return ""
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return raw
}

// sessionHeaderValue returns the first normalized value of a case-insensitive
// header. Only printable non-empty values are accepted.
func sessionHeaderValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, value := range values {
			if id := normalizeSessionID(value); id != "" {
				return id
			}
		}
	}
	return ""
}

// extractSessionKeyFromSources derives a stable conversation key from raw
// headers + metadata. Both scheduler.SchedulerOptions and
// executor.ExecutorRequest expose these as flat (headers, metadata) fields,
// so the same priority chain works on both sides of the pick → execute
// handshake. Returns "" when no session signal is present; callers then fall
// back to the first usable candidate.
//
// Priority (strongest signal first):
//  1. explicit execution session metadata (e.g. Codex websocket call id)
//  2. explicit client session headers (Claude Code / Codex / Responses / OpenCode)
//  3. host-derived session identity (stable hash of the conversation root)
func extractSessionKeyFromSources(headers map[string][]string, metadata map[string]any) string {
	if v, ok := metadata[executionSessionIDMetadataKey].(string); ok {
		if id := normalizeSessionID(v); id != "" {
			return "execution:" + id
		}
	}
	for _, header := range []string{
		"X-Claude-Code-Session-Id", "Session-Id", "Session_id",
		"X-Session-ID", "X-Session-Affinity", "X-Client-Request-Id",
	} {
		if sid := sessionHeaderValue(headers, header); sid != "" {
			return headerSessionPrefix(header) + sid
		}
	}
	if v, ok := metadata[derivedSessionIDMetadataKey].(string); ok {
		if id := normalizeSessionID(v); id != "" {
			return "derived:" + id
		}
	}
	return ""
}

// extractSessionKey derives a stable conversation key from a scheduler pick
// request, using only information the host exposes to scheduler plugins.
// Thin adapter over extractSessionKeyFromSources — see that function for the
// priority chain and rationale.
func extractSessionKey(req pluginapi.SchedulerPickRequest) string {
	return extractSessionKeyFromSources(req.Options.Headers, req.Options.Metadata)
}

// headerSessionPrefix namespaces explicit session headers so different client
// types never collide on the same opaque id.
func headerSessionPrefix(header string) string {
	switch header {
	case "X-Claude-Code-Session-Id":
		return "claude:"
	case "Session-Id", "Session_id":
		return "codex:"
	case "X-Session-ID":
		return "header:"
	case "X-Session-Affinity":
		return "affinity:"
	case "X-Client-Request-Id":
		return "clientreq:"
	default:
		return "header:"
	}
}

// traeSessionCandidate is the thin view pickSessionAuth routes over: ID is
// the host auth-record ID (returned as AuthID), UID is the pool credential
// UID used for health checks ("" when unknown → usable, fail-open).
type traeSessionCandidate struct {
	ID        string
	UID       string
	Disabled  bool
	Exhausted bool
}

// sessionCandidateUID derives the pool UID for one host candidate:
// Attributes["uid"] / Metadata["uid"] win; otherwise the candidate ID
// itself (covers pools keyed by the same identifier, and stays fail-open
// when the pool knows nothing about it).
func sessionCandidateUID(c pluginapi.SchedulerAuthCandidate) string {
	if c.Attributes != nil {
		if uid := strings.TrimSpace(c.Attributes["uid"]); uid != "" {
			return uid
		}
	}
	if c.Metadata != nil {
		if s, ok := c.Metadata["uid"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return strings.TrimSpace(c.ID)
}

// sessionPoolUnusable reports whether the pool currently parks uid
// (cooling / disabled / quota-frozen). Unknown UIDs and a nil pool report
// usable (fail-open — session mode must never route worse than defer).
func sessionPoolUnusable(uid string) bool {
	if accountPool == nil || strings.TrimSpace(uid) == "" {
		return false
	}
	st, ok := accountPool.Status(uid)
	if !ok {
		return false
	}
	return st.Disabled || st.Cooling
}

// sessionCandidateDisabled reports host-disabled auth from Status/metadata
// (same contract as workbuddy candidateDisabled).
func sessionCandidateDisabled(c pluginapi.SchedulerAuthCandidate) bool {
	st := strings.ToLower(strings.TrimSpace(c.Status))
	if st == "disabled" {
		return true
	}
	if c.Metadata != nil {
		if v, ok := c.Metadata["disabled"]; ok {
			switch t := v.(type) {
			case bool:
				return t
			case string:
				return strings.EqualFold(strings.TrimSpace(t), "true")
			}
		}
	}
	return false
}

// pickSessionAuth selects the account for one conversation.
//
// sessionKey != "" → sticky session routing:
//   - a fresh, still-usable binding is reused unchanged (1h stickiness);
//   - a stale binding (expired) or a binding whose account became
//     disabled/exhausted is re-assigned;
//   - new assignments prefer accounts with no live bindings, then round-robin
//     across all usable accounts;
//   - when every account is unavailable, "" is returned so the scheduler
//     defers to the host's built-in scheduler (cross-provider failover); the
//     binding is kept for sticky recovery.
//
// sessionKey == "" → fall back to the first usable candidate.
func pickSessionAuth(sessionKey string, candidates []traeSessionCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	live := make(map[string]struct{}, len(candidates))
	usable := make([]string, 0, len(candidates))
	usableSet := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		live[c.ID] = struct{}{}
		if !c.Disabled && !c.Exhausted {
			usable = append(usable, c.ID)
			usableSet[c.ID] = struct{}{}
		}
	}
	if sessionKey == "" {
		if len(usable) > 0 {
			return usable[0]
		}
		return ""
	}

	now := time.Now()
	sessionAuthMu.Lock()
	defer sessionAuthMu.Unlock()

	// Sticky hit: binding fresh AND its account still usable.
	if b, ok := sessionAuthBindings[sessionKey]; ok {
		if _, isLive := live[b.AuthID]; isLive {
			if _, isUsable := usableSet[b.AuthID]; isUsable && now.Before(b.ExpiresAt) {
				return b.AuthID
			}
		}
	}

	if len(usable) == 0 {
		// No healthy trae account exists. Return "" so handleSchedulerPick
		// defers (Handled: false) to the host's built-in scheduler, which
		// can fail over to other providers' accounts instead of pinning
		// this conversation to a dead account. The existing binding is kept:
		// once an account recovers, the sticky re-hit restores it.
		return ""
	}

	// Fresh assignment: prefer accounts with no live bindings (spreads
	// conversations across accounts), then round-robin.
	boundCounts := make(map[string]int, len(usable))
	for _, b := range sessionAuthBindings {
		if now.Before(b.ExpiresAt) {
			boundCounts[b.AuthID]++
		}
	}
	next := ""
	for _, id := range usable {
		if boundCounts[id] == 0 {
			next = id
			break
		}
	}
	if next == "" {
		next = usable[int(sessionRR%uint64(len(usable)))]
		sessionRR++
	}

	sessionAuthBindings[sessionKey] = sessionAuthBinding{AuthID: next, ExpiresAt: now.Add(sessionStickinessTTL)}
	return next
}

// pruneSessionBindings removes expired bindings. Called by the background
// pruner; also safe to call from tests directly.
func pruneSessionBindings() {
	now := time.Now()
	sessionAuthMu.Lock()
	defer sessionAuthMu.Unlock()
	for key, b := range sessionAuthBindings {
		if !now.Before(b.ExpiresAt) {
			delete(sessionAuthBindings, key)
		}
	}
}

// evictSessionBindingsForAuth removes every binding pinned to the given
// account (regardless of expiry), so conversations stuck on a failing account
// are re-assigned on the next pick. Returns the number of bindings removed.
// Called on every same-request account rotation.
func evictSessionBindingsForAuth(authID string) int {
	if authID == "" {
		return 0
	}
	sessionAuthMu.Lock()
	defer sessionAuthMu.Unlock()
	evicted := 0
	for key, b := range sessionAuthBindings {
		if b.AuthID == authID {
			delete(sessionAuthBindings, key)
			evicted++
		}
	}
	return evicted
}

// clearSessionBindings wipes all session bindings. Test helper; never called
// in production paths.
func clearSessionBindings() {
	sessionAuthMu.Lock()
	sessionAuthBindings = make(map[string]sessionAuthBinding)
	sessionRR = 0
	sessionAuthMu.Unlock()
}
