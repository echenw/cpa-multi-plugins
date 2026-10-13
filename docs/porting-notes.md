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
