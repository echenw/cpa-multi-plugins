# cockpit-tools-derived segments inventory (CC BY-NC-SA 4.0) — v0.1 publish gate

Source of truth for "declared entries": R2 `README.md` §Per-plugin source files
(read from `/Users/aizou/Desktop/工作台/参考项目/2api/cpa-multi-plugins/README.md`,
lines 37–67). One row per declared entry below — no invented entries, no dropped entries.

Upstream: [cockpit-tools](https://github.com/jlcodes99/cockpit-tools) — CC BY-NC-SA 4.0.
Work copy: `/Users/aizou/Desktop/工作台/cpa-plugin-suite/` (baseline `208a4194e41cf4e6a3a92529fda8f3517df5ae44`).
Every `file:line` below was read in the work copy during T5 (see evidence file).

## 1. workbuddy — content-filter bypass

- Declared (R2 README:41): **Content filter bypass**: `OmniRoute/open-sse/executors/codebuddy-cn.ts`
  lines 149–202 (AGENT_PATTERN + length fallback + reasoning_summary mirroring +
  large tool-description compression).
- Attribution note: this entry's declared origin is **OmniRoute (not cockpit-tools)**.
  It is included because the T5 task list names "workbuddy 内容过滤绕过" among the
  six rows; it is NOT a cockpit-tools-derived segment. No cockpit-tools license
  attaches to it via this declaration.
- Work-copy location (all read):
  - `plugins/workbuddy/payload.go:233-254` (`rewriteSystemInPlace`: three defenses entry point)
  - `plugins/workbuddy/payload.go:256-284` (`rewriteSystemMessagesInPlace`: system-role gate;
    restores OmniRoute's `role !== "system" -> return` guard)
  - `plugins/workbuddy/payload.go:286-328` (`rewriteSystemContentField`: wholesale
    neutralPrompt replacement RETIRED since v0.9.28; only `sanitizeBlockedTemplates` survives)
  - `plugins/workbuddy/payload.go:330-350` (`mirrorReasoningEffort`: reasoning_summary="auto" mirror)
  - `plugins/workbuddy/payload.go:352-383` (`compactToolDescriptions`: 64KB tool-description strip)
  - `plugins/workbuddy/payload.go:729-748` (`sanitizeBlockedTemplates`: verbatim-phrase
    rewrites + Codex-CLI intro signature, issue #31)
- Remediation advice: n/a (OmniRoute-derived, not CC BY-NC-SA 4.0). Keep the OmniRoute
  attribution; no publish-gate action under the cockpit-tools gate.

## 2. workbuddy — check-in state machine (cockpit-tools)

- Declared (R2 README:42): **Check-in state machine**:
  `cockpit-tools/src-tauri/src/modules/workbuddy_auto_checkin.rs` lines 33–64, 406–754
  (WorkbuddyAutoCheckinConfig + exponential backoff scheduler).
- Work-copy location (all read):
  - `plugins/workbuddy/checkin.go:22-30` (`ensureScheduler`: single-flight scheduler start)
  - `plugins/workbuddy/checkin.go:37-57` (`nextCheckinTime`: next-slot computation)
  - `plugins/workbuddy/checkin.go:59-82` (`schedulerLoop`: timer-driven tick loop)
  - `plugins/workbuddy/checkin.go:84-116` (`runAutoCheckin`: 09:00/21:00 tick, sem=4 fan-out)
  - `plugins/workbuddy/checkin.go:118-120` (`processAutoCheckinAccount`: per-account tick)
  - `plugins/workbuddy/usage_config.go:19` (`checkinHours = []int{9, 21}`: fixed cadence)
  - `plugins/workbuddy/checkin.go:336-341` (comment: mirrors cockpit-tools'
    "签到活动未开启或不适用" handling for the inactive-account skip)
- Mapping honesty note: the work copy uses a **fixed 09:00/21:00 cadence**; no
  exponential-backoff scheduler literal to cockpit's `workbuddy_auto_checkin.rs:406-754`
  was located in this copy. The cockpit-derived behavior that IS present is the
  scheduled auto-check-in lifecycle + the inactive-plan skip semantics cited at
  `checkin.go:336-341`.
- Remediation advice: re-implement the scheduler/state machine from a clean-room
  spec (own cadence + retry policy, no cockpit control-flow port) or obtain a
  compatible license; until then the gate below applies.

## 3. workbuddy — check-in field parsing (cockpit-tools)

- Declared (R2 README:43): **Check-in field parsing**:
  `cockpit-tools/src-tauri/src/modules/codebuddy_cn_oauth.rs` lines 1208–1258,
  1285–1394, 1423–1587 (full CheckinStatusResponse fields + fallback paths).
  Adjacent declared reference (R2 README:52): `codebuddy_cn_oauth.rs:8` platform parameter
  (codebuddy-cn/login_platform merge note; same upstream file, not a separate segment).
- Work-copy location (all read):
  - `plugins/workbuddy/billing.go:338-386` (`parseCheckinStatusPayload`: full status-field
    decode incl. snake/camelCase dual keys, checkin_dates calendar, today cross-check)
  - `plugins/workbuddy/billing.go:388-426` (`checkinSummary.mergeOR`: dual-endpoint OR-merge)
  - `plugins/workbuddy/billing.go:428-459` (`fetchCheckinStatus`: ambiguous-payload
    two-endpoint probing policy, `/v2/billing/meter/checkin-activity-status` then
    `/v2/billing/meter/checkin-status`)
- Remediation advice: re-derive the field table from live API responses / first-party
  docs (own tolerant parser + tests), removing the cockpit field-list/fallback-path
  lineage; until then the gate below applies.

## 4. trae — check-in headers (cockpit-tools)

- Declared (R2 README:65): **Check-in headers**:
  `cockpit-tools/src-tauri/src/modules/trae_account_token_injection.rs:2761,2859`
  (x-app-type: trae, Origin: https://www.trae.cn, Referer: https://www.trae.cn/).
- Work-copy location (all read):
  - `plugins/trae/upstream/headers.go:59-68` — the cockpit-style header image is
    **explicitly removed**: "UgCheckinHeaders 已移除 (v0.12.38)" and "无
    Origin/Referer/x-app-type（此前是 WebView 画像，真实客户端不携带）".
  - `plugins/trae/upstream/headers.go:114-138` (`ugBaseHeaders`: current capture-based
    fingerprint set; per-caller Authorization)
  - `plugins/trae/upstream/client.go:768-787` (`ugCheckinRequest`: current check-in
    request builder — capture fingerprint + per-scheme Authorization + X-Device-Id)
  - Historical lineage comments still present in copy (read, not behavior):
    `plugins/trae/upstream/client.go:563` (old GET + did query, "现已弃用"),
    `plugins/trae/upstream/client.go:596-604` (v0.12.35 Bearer-port postmortem:
    cockpit token-category mismatch, biz_code=1001)
- Mapping honesty note: the declared cockpit header triple (x-app-type/Origin/Referer)
  is **not present as behavior** in this copy — it was deliberately replaced by the
  v0.12.45 capture fingerprint. The segment remains listed because the README declares
  it as the derivation source and lineage comments cite it; remediation must cover the
  residual lineage (comments + any lingering shape) and confirm no header values are
  copied verbatim.
- Remediation advice: keep the capture-derived header set; scrub cockpit header values
  and lineage comments, re-document each header from own captures; until then the gate
  below applies.

## 5. trae — v2 credits pack priority (cockpit-tools)

- Declared (R2 README:66): **v2 credits pack priority**:
  `cockpit-tools/src-tauri/src/modules/trae_account_token_injection.rs:1807-1866`
  (apply_usage_response).
- Work-copy location (all read):
  - `plugins/trae/upstream/client.go:1040-1062` (`UserEntUsage`: "对齐 cockpit-tools
    trae_account_token_injection.rs apply_usage_response", 5-step pipeline comment)
  - `plugins/trae/upstream/client.go:1402-1444` (`SelectActivePack`: "applies cockpit-tools
    apply_usage_response logic" — PROMO_CODE/hide/cancelled filters + CN/Intl priority
    orders `100→6→5/4→1/9→8→0` / `6→4→1→9→8→0` + first-active fallback)
  - `plugins/trae/scheduler/scheduler.go:206` (comment: "对齐 cockpit-tools
    apply_usage_response：按 pack 优先级选最高 pack")
  - `plugins/trae/upstream/client.go:1361` (call site: `selected := SelectActivePack(...)`)
- Remediation advice: re-specify pack selection from own product/ordering requirements
  (own priority table + tests, no cockpit ordering port) or obtain a compatible license;
  until then the gate below applies.

## 6. trae — pack product_type mapping (cockpit-tools)

- Declared (R2 README:67): **pack product_type mapping**:
  `cockpit-tools/src/types/trae.ts:174-189` (TRAE_PRODUCT_TYPE).
- Work-copy location (all read):
  - `plugins/trae/upstream/client.go:1446-1473` (`ProductTypeIdentity`: "对齐 cockpit-tools
    usage_identity_from_product_type" — 100/6/5/4/1/9/8/0 → CNExpress/Ultra/Pro+/Pro/Lite/Free/Unknown)
  - `plugins/trae/upstream/client.go:949` (`ProductType int json:"product_type"` struct field
    with the value-meaning comment `0=Free, 1=Pro, 4=Pro+, 5=Pro+CN, 6=Ultra, 8=Lite, 9=Trial, 100=CNExpress`)
  - Call site: `plugins/trae/management.go:1067-1071` (identityStr falls back to
    `ProductTypeIdentity` when display_desc is absent)
- Remediation advice: re-derive the product_type→identity table from own plan-catalog
  sources (own mapping + tests), removing the cockpit table lineage; until then the gate
  below applies.

## Adjacent cockpit-tools mentions (declared elsewhere in R2 README, NOT separate inventory rows)

- R2 README:27 (Protocol Sources table) summarizes the same cockpit contributions at
  project level — covered by rows 2–6 above; row 1's bypass belongs to OmniRoute.
- R2 README:63-64,71 (`trae-cn` client_id `ono9krqynydwx5` / function `inline_chat`,
  `trae-solo-cn` client_id `en1oxy7wnw8j9n`): marked "aligned with cockpit-tools
  `trae_account_platform_storage.rs`" — alignment notes, not declared derivation
  segments; no separate rows (per "no invented entries"). Remediation of rows 2–6
  should double-check these constants are sourced independently.

## Publish gate (load-bearing — do not soften)

v0.1 does NOT remediate any cockpit-tools-derived segment above. This inventory is
documentation only; no inventoried code was modified.

**Any public release or public distribution of product carrying unremediated
cockpit-tools-derived segments (rows 2–6) is FORBIDDEN until each such segment is
remediated (clean-room re-implementation with independent sourcing + tests) or
replaced, and this file + NOTICE are updated to record the remediation.**
