# Task 14 evidence — mimo usage feed (T14) + post-T14 feed-label fix

## Fix record — feed mislabel correction (2026-10-13)

T14 copied `plugins/mimo/usage_feed.go` byte-identical from the canonical
`plugins/workbuddy/usage_feed.go`, so mimo feed rows carried
`executor_type:"workbuddy"`, the header claimed CANONICAL status, and the
warn prefix read `[workbuddy]`. Corrected to mimo identity; R1-port
provenance comment kept.

### Before (`grep -n "workbuddy\|executor_type\|usage feed:" plugins/mimo/usage_feed.go`)

```text
5:// Why a file feed instead of merging the tracker into workbuddy or sharing
8://     pure UsagePlugin consumer cannot observe workbuddy requests.
10://     long-lived processes (workbuddy recording + tracker dashboard) cannot
14:// (<CPA root>/data/token-usage-feed.ndjson by default). workbuddy is the only
38://	"latency_ms", "source", "endpoint", "auth_type", "executor_type",
48://   - KNOWN-GAP source: the workbuddy-internal account label
60:// Ported from the R1 workbuddy plugin (usage_feed.go): rotation/threshold
188:// accountLabel is the workbuddy-internal account identifier (sa.Account.Nickname
232:		"executor_type":    "workbuddy",
291:	fmt.Fprintf(os.Stderr, "[workbuddy] usage feed: "+format+"\n", args...)
```

Header before: `CANONICAL implementation (plan T10): all other plugins copy
this file; divergences must land here first, then sync out.`

### After (same grep)

```text
3:// (plan T10, plugins/workbuddy/usage_feed.go); divergences must land in the
5:// (workbuddy -> mimo).
40://	"latency_ms", "source", "endpoint", "auth_type", "executor_type",
62:// Ported from the R1 workbuddy plugin (usage_feed.go): rotation/threshold
234:		"executor_type":    "mimo",
293:	fmt.Fprintf(os.Stderr, "[mimo] usage feed: "+format+"\n", args...)
```

Remaining `workbuddy` mentions are intentional: canonical-path pointer
(L3), adaptation note (L5), R1-port provenance (L62, kept per scope).

### Other copies — header verification (read before editing)

- `plugins/qoder/usage_feed.go`: header already `COPY of the canonical
  implementation (plan T10, plugins/workbuddy/usage_feed.go)` — no change.
  `executor_type` stays `"qoderwork"`, prefix `[qoder]` (owner-set, out of scope).
- `plugins/zcode/usage_feed.go`: header already COPY attribution — no change.
  `executor_type` stays `"zcode"`, prefix `[zcode]` (owner-set, out of scope).
- `plugins/trae/usage_feed.go`: header read `CANONICAL copy from
  plugins/workbuddy (plan T10/T12)` — false CANONICAL self-claim, corrected
  to `COPY of the canonical implementation (plan T10/T12,
  plugins/workbuddy/usage_feed.go)`. `executor_type` stays `"trae"`,
  prefix `[trae]` (owner-set, out of scope).

### Test

- `plugins/mimo/usage_feed_test.go`: `TestHandleUsageAppendsOneFeedLine`
  extended with `executor_type` field decode + `== "mimo"` assertion.
- Results: mimo full suite green (`go test ./...` in `plugins/mimo`);
  qoder/trae/zcode feed-test files green
  (`TestHandleUsage*`, `TestConfigureUsageFeed*`, `TestAppendUsageFeedLine*`,
  `TestUsageHandle*`).

### Docs

- `docs/porting-notes.md` T14 section: "byte-identical copy" sentence
  replaced (identity-adapted copy); one line added on the
  `executor_type: workbuddy → mimo` correction.
