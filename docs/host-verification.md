# Host Load Verification (T9)

Source of record: `../.omo/evidence/task-9-cpa-plugin-suite.md`
(canonical evidence, 266 lines, dated 2026-10-13).
This doc is a faithful transcription. No new claims.

## Verdict

* Fallback host: **VERIFIED**, 5/5 plugins registered on CLIProxyAPI v7.3.8 host
  (evidence lines 8, 184, 245).
* Preferred host (cockpit sidecar): **BLOCKER**, no pluginhost support
  (evidence lines 9, 42-50, 75-77).

## Registration lines (5/5)

Trial (b.2) happy path, `grep -c "plugin registered"` = 5
(evidence lines 184-192). Verbatim:

```
[2026-10-13 20:45:28] [--------] [info ] [host.go:375] pluginhost: plugin registered plugin_id=mimo plugin_name=mimo version=0.2.17 path=/tmp/t9-cliproxyapi-b.PsGuzV/plugins/mimo.dylib
[2026-10-13 20:45:28] [--------] [info ] [host.go:375] pluginhost: plugin registered plugin_id=qoder plugin_name=qoder version=0.8.63 path=/tmp/t9-cliproxyapi-b.PsGuzV/plugins/qoder.dylib
[2026-10-13 20:45:29] [--------] [info ] [host.go:375] pluginhost: plugin registered plugin_id=trae plugin_name=trae version=0.12.77 path=/tmp/t9-cliproxyapi-b.PsGuzV/plugins/trae.dylib
[2026-10-13 20:45:29] [--------] [info ] [host.go:375] pluginhost: plugin registered plugin_id=workbuddy plugin_name=workbuddy version=0.9.58 path=/tmp/t9-cliproxyapi-b.PsGuzV/plugins/workbuddy.dylib
[2026-10-13 20:45:29] [--------] [info ] [host.go:375] pluginhost: plugin registered plugin_id=zcode plugin_name=zcode version=0.2.0 path=/tmp/t9-cliproxyapi-b.PsGuzV/plugins/zcode.dylib
```

Artifacts loaded: `dist/darwin-arm64/{mimo,qoder,trae,workbuddy,zcode}.dylib`,
Mach-O 64-bit arm64, each exports `cliproxy_plugin_init` count=1
(evidence lines 5-6).

## Static ABI table

Transcribed from evidence lines 238-245:

| Side | Module / source | ABIVersion (`sdk/pluginabi`) | SchemaVersion | Note |
|---|---|---|---|---|
| Plugins (all 5: mimo/qoder/trae/workbuddy/zcode go.mod) | `github.com/router-for-me/CLIProxyAPI/v7 v7.2.30` | 1 | 1 | module-cache `types.go:7,11`; SchemaVersion 1 = capability-gated, no breaking RPC |
| Fallback host (loading host) | `参考项目/00-agent/CLIProxyAPI` HEAD `c93978c4` = `git describe` **v7.3.8** | 1 | 6 | `sdk/pluginabi/types.go:7,20`; v6 adds raw JSON mgmt responses; compat: host rejects only `plugin schema > host schema` (`rpc_client.go:75-76`), older plugins get back-compat shims (`streamChunkOmitsRequestBodies/History` gates) |
| Preferred sidecar host | `cockpit-tools/sidecars/cockpit-cliproxy`: outer go.mod `v7.2.155` + `replace => ./third_party/CLIProxyAPI` (UPSTREAM.md: sync baseline v7.2.155 `7fac6b15`) | 1 (vendored) | 3 (vendored) | IRRELEVANT in practice: outer sidecar code has zero pluginhost usage (grep empty), binary cannot load plugins at all |

Loading verdict (evidence line 244-245): plugin SchemaVersion 1 <= host 6,
ABIVersion 1 = 1, compatible; trial (b.2) confirms 5/5 `plugin registered`.

## Method note

* Copy-build: fallback host was a COPY (`cp -R 参考项目/00-agent/CLIProxyAPI`
  to `cpa-plugin-suite/.local-test/cliproxyapi-host`), built in the copy
  (`go build -o /tmp/t9-server ./cmd/server`, `BUILD EXIT=0`),
  source dir never written (evidence lines 83-86).
* Per-plugin `plugins.configs.<id>.enabled: true` REQUIRED. Global
  `plugins.enabled: true` alone loads nothing: trial 1 (negative control)
  gave `grep -c pluginhost` = 0 because absent plugin IDs default to
  `Enabled:false` (`internal/pluginhost/config.go:72-79`) and `host.go`
  skips disabled items silently (evidence lines 88-121).
* `.local-test/` has been removed since (evidence line 263).
  Temp dirs `/tmp/t9-sidecar-a.shdjIp`, `/tmp/t9-cliproxyapi-b.PsGuzV`
  and temp binaries removed; spawned hosts SIGTERM'd
  (evidence lines 259-263).
* Reference repos untouched: BEFORE and AFTER
  `git -C 参考项目/00-agent/CLIProxyAPI status --short` and
  `git -C cockpit-tools status --short` both empty
  (evidence lines 11-12, 264-265).

## Sidecar BLOCKER detail

* Sidecar binary has no plugin-related flag (evidence lines 20-36).
* Static grep over outer sidecar code (excluding `third_party/`) for
  `pluginhost|plugin registered|plugins.dir|Plugins.Dir` is empty;
  `pluginhost` exists only under vendored `third_party/CLIProxyAPI/`,
  unused by sidecar main (evidence lines 42-49).
* Load trial (~12s, then SIGTERM) gave `grep -c "plugin registered"` = 0;
  raw `host.log` shows only startup stages plus one auth-dir error
  (evidence lines 65-77).

## Raw logs

See `../.omo/evidence/task-9-cpa-plugin-suite.md` for complete raw logs:
(a.1) help output, (a.3) sidecar `host.log`, (b.1) trial 1 `host.log`,
(b.2) trial 2 `host2.log`, (b.3) failure trial `host-fail.log`.
