# Porting notes (R2 → cpa-plugin-suite adaptations)

Per-plugin deviations from a verbatim R2 carry-over, recorded as they land.
Each entry names the plan task that introduced it.

## T14 — mimo usage feed (feed producer with usage callback enabled)

- `plugins/mimo/usage_feed.go` is a copy of the canonical
  `plugins/workbuddy/usage_feed.go` (plan T10) with module/path identity
  adapted (`workbuddy` → `mimo` in header, comments, warn prefix; both
  `package main` on SDK `v7.2.30`; the shared `providerName` const resolves
  to `mimo`). Canonical known gaps are inherited unchanged:
  empty `session_key` on the host-callback path, `record.Source`
  pass-through.
- Feed-label correction (post-T14 fix): the initial copy carried the
  canonical `"executor_type": "workbuddy"` verbatim; it now writes
  `"executor_type": "mimo"` (asserted by the append-one-line feed test).
- Adaptation (the reason this task is marked「含适配差异」): mimo shipped
  with `UsagePlugin=false` and no `MethodUsageHandle` dispatch, unlike
  qoder/zcode/trae. `plugins/mimo/main.go` now advertises
  `UsagePlugin=true` and routes `pluginabi.MethodUsageHandle` to a new
  `handleUsage` in `plugins/mimo/usage.go`; `configure()` calls
  `configureUsageFeed(raw)` so the register/reconfigure lifecycle arms the
  feed path (without it the path stays unset and the writer is a no-op).
- `handleUsage` is feed-only: mimo has no executor-side `publishUsage` and
  no CPAMP forward, so the host callback is the sole write entry (same
  shape as trae T12). workbuddy-only concerns (note ledger, CPAMP) were
  deliberately not ported.
- Dependency closure: importing `sdk/cliproxy/usage` (for `usage.Detail`)
  required adding the indirect `github.com/sirupsen/logrus v1.9.3` entry to
  `plugins/mimo/go.mod`/`go.sum` — the same version workbuddy already pins.
  No other dependency or behavior change.

## T20 — zcode retry_on_4xx rotation + ladder cooldown (no session)

- R1 `zcode/executor.go:48` fixed `for attempt<3` ported to
  `plugins/zcode/main.go` execute (`handleExecExecute`) and sync-stream
  (`handleExecStreamSync`) paths as a configurable `retry_on_4xx` budget
  (`plugins/zcode/retry_config.go`, default 3 aligned with R1's fixed 3,
  clamp 0–10 matching R1). Versus R2 this is new account-level rotation,
  not an equivalent port. Overlay rule: account-level 4xx/429 rotates the
  account within one request; model-level throttling keeps the existing
  per-(credential, model) table untouched; business 400, 5xx/0/402 and
  off-peak ticket-lane failures never rotate (off-peak/plane semantics
  unchanged). Next-account selection lives in
  `plugins/zcode/failover_retry.go` (host-auth order, skips cooling/
  disabled/unloadable candidates).
- R1 `zcode/failover.go:22/:45/:82` ladder (1/3/10 min tiers, fail-count
  escalation, success reset, 401/403/404/405/429 + body-marker rotate
  predicate) implemented in the work-copy equivalent structure
  (`plugins/zcode/cooldown.go` account ladder; R2 has no
  `plugins/zcode/failover.go` and existing `plugins/zcode/executor.go` is
  untouched). Async stream pump stays single-shot (a retry would be visible
  mid-stream); only the sync-collect path rotates.
- Gap (explicitly out of scope): zcode has no session affinity — R1 zcode
  has none either, so there is nothing to port. mimo has neither rotation
  nor session and no reference implementation exists — not built here.

## T18 — qoder 4xx account rotation + session affinity (T17-mode port)

- Ported files (T17 semantics mirrored exactly, qoder surface adapted):
  `plugins/qoder/retry_config.go` (default 10 / clamp 0–10 / missing-key
  preserved, `isAccountLevel4xx` = 401/403/404/405/429, 400 excluded),
  `plugins/qoder/failover_retry.go` (`pickNextAuth(currentAuthID, model)`,
  `pickNextAuthFn` stub seam, `qoderChatBaseOverride` hermetic seam),
  `plugins/qoder/session_auth.go` (1h sticky bindings, unbound-first then
  round-robin, evict-on-rotation). Attempt loops on both single-shot paths
  (`main.go` `handleExecExecute`, `stream.go` `collectUpstreamStreamQoder`
  sync branch; async pump untouched, as in T17). Scheduler authID — not
  token/UID — threads through both loops; `configure()` wires
  `retry_on_4xx` + `scheduler_mode=session` on BOTH parse paths (YAML-map
  decode and line-scan). Registration advertises the new `session` enum
  value plus the `retry_on_4xx` integer key.
- Overlay rule (account-level vs model-level cooldown priority): an
  account-level 4xx/429 rotates the ACCOUNT within the request
  (`failover_retry.go`); model-level throttling stays exclusively with the
  R2 per-(credential, model) table (`cooldown.go` `recordUpstreamFailure`),
  which the retry path reads (`modelIsCooling` skip) but never writes.
  R1 qoderwork's fixed per-account cooldown (`accountFailover.go`) and its
  「测试」 quarantine have no R2 counterpart and were deliberately NOT
  ported; R1's `accountRoutable` cooling check inside `pickSessionAuth`
  collapses to the scheduler-side cooling filter that already runs before
  session routing. 5xx/0/402 and business 400 keep the cross-request
  cooldown path and never burn rotation budget.
- Hermetic seams: `qoderChatBaseOverride` short-circuits `endpointChatFor`
  (chat-only blast radius); mock upstreams key accounts off the `cosy-user`
  identity header; in-stream 200 error frames rotate only on
  hard-credit/soft-rate-limit markers (T17 rule, not R1's broader
  `isAccountFailure`).

## Post-v0.1 — login region moved to the management panel (qoder/trae/workbuddy)

- Config-file region switching is deleted (no `login_region:`/`login_variant:`
  parsing, no sticky state, no `login/region` GET route, no
  `region_options` metadata stamping — the host frontend ignores that
  metadata, verified against CLIProxyAPI source 2026-10-14).
- Each plugin panel now has an `授权登录` button with a CN/Intl toggle
  defaulting to Intl, backed by new `POST <base>/login/start`
  (`{region}`, absent → intl, invalid → 400, nothing persisted).
  Login attempts without a carried region (incl. the host OAuth popup)
  resolve to Intl. Existing accounts keep the region recorded at login time.
