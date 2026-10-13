// usage_feed.go implements the shared NDJSON usage feed for the standalone
// token-usage-tracker plugin. COPY of the canonical implementation
// (plan T10/T12, plugins/workbuddy/usage_feed.go): field spec, rotation semantics and threshold behavior are
// identical; only provider/executor identity strings and comments are
// trae-adapted. Divergences must land in the canonical file first, then sync
// out.
//
// Why a file feed instead of merging the tracker into trae or sharing
// a bbolt database:
//   - The host's UsagePlugin broadcast never fires for plugin executors, so a
//     pure UsagePlugin consumer cannot observe trae requests.
//   - bbolt holds an exclusive flock for the lifetime of a writer, so two
//     long-lived processes (trae recording + tracker dashboard) cannot
//     share one DB file.
//
// The feed is a simple append-only NDJSON file at a well-known path
// (<CPA root>/data/token-usage-feed.ndjson by default). trae is a producer
// (O_APPEND, one line per completed request, opened and closed per
// line so rotation is safe); token-usage-tracker is the only consumer (tracks
// its read offset, imports into its own bbolt store). No locks, no
// cross-process coordination, replayable after restart. The host callback
// handleUsageTrae is the ONLY write entry — trae has no executor-side
// publishUsage, so no scatter writes exist.
//
// NDJSON field spec (one JSON object per line):
//
//	ts (string, RFC3339Nano "timestamp"): request start time (host
//	  UsageRecord.RequestedAt; falls back to time.Now when zero). Always
//	  present.
//	plugin (string, "provider"): plugin id (providerName). Always present.
//	account (string, "auth_index"): serving credential UID (host
//	  UsageRecord.AuthID). Always present (may be empty when the host
//	  omits it).
//	model (string, "model" + "alias"): upstream model + user-facing alias.
//	  Always present (alias falls back to model).
//	tokens (object, "tokens"): input_tokens / output_tokens /
//	  reasoning_tokens / cached_tokens / cache_read_tokens /
//	  cache_creation_tokens / total_tokens (total recomputed from
//	  input+output+reasoning when the host reports 0). Always present.
//	stream (uint64, "ttft_ns"): time-to-first-token in nanoseconds from
//	  host UsageRecord.TTFT; 0 when not observable (non-SSE paths or
//	  transport failures). Always present.
//	"latency_ms", "source", "endpoint", "auth_type", "executor_type",
//	  "failed", "status_code", "session_key", "reasoning_effort": same
//	  shape as the canonical feed payload so the tracker plugin sees
//	  identical data across producers.
//
// Host-callback availability (handleUsageTrae path) vs known gaps:
//   - AVAILABLE: ts (RequestedAt), plugin, account (AuthID), model/alias,
//     tokens (Detail), stream/TTFT (record.TTFT), reasoning_effort
//     (record.ReasoningEffort), failed + status_code (record.Failed /
//     record.Failure).
//   - KNOWN-GAP source: a trae-internal account label would only be known
//     on an executor publishUsage path (which trae does not have);
//     handleUsageTrae receives no nickname, so it passes record.Source
//     through (usually empty). Recorded as gap, not fabricated.
//   - KNOWN-GAP session_key: the per-conversation stickiness key is not
//     carried in UsageRecord, so the handleUsageTrae path always writes "".
//     Do NOT "fix" this here — the field is always written (empty when
//     unknown) so the feed schema stays self-documenting across NDJSON
//     rotations.
//
// Ported from the canonical plugins/workbuddy/usage_feed.go (plan T10):
// rotation/threshold semantics copied faithfully; maxUsageFeedBytes stays a
// var (not const) so tests can shrink it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const (
	// defaultUsageFeedName is the file name inside the CLIProxyAPI "data" dir.
	defaultUsageFeedName = "token-usage-feed.ndjson"
)

// maxUsageFeedBytes bounds feed growth; on exceeding it the producer
// truncates (the consumer detects truncation by offset > file size and
// resets to 0, so nothing is double-imported). A var (not const) so tests can
// shrink it.
var maxUsageFeedBytes int64 = 128 << 20

var (
	usageFeedMu      sync.RWMutex
	usageFeedEnabled = true
	usageFeedPath    = ""
)

// configureUsageFeed parses the usage_feed_* fields from config_yaml. Failures
// are non-fatal: the plugin keeps serving chat, only the feed is disabled.
func configureUsageFeed(raw []byte) {
	enabled := true
	dataPath := ""
	if len(raw) > 0 {
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if err := json.Unmarshal(raw, &req); err == nil {
			for _, line := range strings.Split(string(req.ConfigYAML), "\n") {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "usage_feed_enabled:"):
					v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "usage_feed_enabled:")), "\"'")
					enabled = v == "true" || v == "1" || v == "yes" || v == "on"
				case strings.HasPrefix(line, "usage_feed_path:"):
					v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "usage_feed_path:")), "\"'")
					if v != "" {
						dataPath = v
					}
				}
			}
		}
	}
	if dataPath == "" {
		dataPath = defaultUsageFeedPath()
	}
	usageFeedMu.Lock()
	usageFeedEnabled = enabled
	usageFeedPath = dataPath
	usageFeedMu.Unlock()
}

// defaultUsageFeedPath resolves the feed location next to the discovered
// CLIProxyAPI root ("<root>/data/token-usage-feed.ndjson"), matching the
// token-usage-tracker plugin's default so both sides agree out of the box.
func defaultUsageFeedPath() string {
	if root, ok := cliProxyRootFromWorkingDir(); ok {
		return filepath.Join(root, "data", defaultUsageFeedName)
	}
	if root, ok := cliProxyRootFromExecutable(); ok {
		return filepath.Join(root, "data", defaultUsageFeedName)
	}
	return filepath.Join("data", defaultUsageFeedName)
}

// cliProxyRootFromWorkingDir returns the first ancestor of the working
// directory that contains a "plugins" directory (the CPA install root).
func cliProxyRootFromWorkingDir() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	absolute, err := filepath.Abs(wd)
	if err != nil {
		return "", false
	}
	return cliProxyRootFromDir(absolute)
}

// cliProxyRootFromExecutable checks the directory containing the CPA binary.
func cliProxyRootFromExecutable() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return cliProxyRootFromDir(filepath.Dir(exe))
}

func cliProxyRootFromDir(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for {
		info, err := os.Stat(filepath.Join(dir, "plugins"))
		if err == nil && info.IsDir() && filepath.Dir(dir) != dir {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// recordUsageFeed appends one NDJSON line for a completed request. Called from
// handleUsageTrae (the host-driven path, synchronous — the host already runs
// it on its own goroutine after the request completes), so a slow filesystem
// never stalls the executor. The record shape mirrors the canonical feed
// payload so the tracker plugin sees identical data across producers.
// reasoningEffort is the reasoning_effort from the host record; ttftNS is
// the time-to-first-token in nanoseconds (0 when not observable, e.g.
// non-SSE paths or transport failures). accountLabel passes the host
// record's source through (usually empty — see the KNOWN-GAP note above).
// sessionKey is always "" on this path — the per-conversation key is not
// carried in UsageRecord (canonical gap, kept empty, not fabricated) so the
// feed schema stays self-documenting across NDJSON rotations.
func recordUsageFeed(alias, model, authUID string, started time.Time, detail usage.Detail, failed bool, statusCode int, reasoningEffort string, ttftNS uint64, accountLabel, sessionKey string) {
	usageFeedMu.RLock()
	enabled := usageFeedEnabled
	path := usageFeedPath
	usageFeedMu.RUnlock()
	if !enabled || path == "" {
		return
	}
	ts := started
	if ts.IsZero() {
		ts = time.Now()
	}
	latencyMs := int64(0)
	if !started.IsZero() {
		if d := time.Since(started).Milliseconds(); d > 0 {
			latencyMs = d
		}
	}
	total := detail.TotalTokens
	if total == 0 {
		total = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens
	}
	record := map[string]any{
		"timestamp":        ts.UTC().Format(time.RFC3339Nano),
		"latency_ms":       latencyMs,
		"source":           strings.TrimSpace(accountLabel),
		"auth_index":       strings.TrimSpace(authUID),
		"provider":         providerName,
		"model":            model,
		"alias":            alias,
		"endpoint":         "POST /v1/chat/completions",
		"auth_type":        "oauth",
		"executor_type":    "trae",
		"failed":           failed,
		"status_code":      statusCode,
		"session_key":      sessionKey,
		"reasoning_effort": strings.TrimSpace(reasoningEffort),
		"ttft_ns":          ttftNS,
		"tokens": map[string]any{
			"input_tokens":          detail.InputTokens,
			"output_tokens":         detail.OutputTokens,
			"reasoning_tokens":      detail.ReasoningTokens,
			"cached_tokens":         detail.CachedTokens,
			"cache_read_tokens":     detail.CacheReadTokens,
			"cache_creation_tokens": detail.CacheCreationTokens,
			"total_tokens":          total,
		},
	}
	body, err := json.Marshal(record)
	if err != nil {
		usageFeedWarnf("marshal: %v", err)
		return
	}
	body = append(body, '\n')
	appendUsageFeedLine(path, body)
}

// appendUsageFeedLine opens the feed with O_APPEND and writes one line.
// Opens per line (not a held handle) so the consumer can rotate/truncate and
// so a rename-based rotation by future tooling stays safe. Rotation uses
// os.Truncate BEFORE the O_APPEND open: on Windows, truncating through an
// O_APPEND handle fails with access denied (FILE_APPEND_DATA != WRITE_DATA).
func appendUsageFeedLine(path string, line []byte) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			usageFeedWarnf("mkdir %s: %v", dir, err)
			return
		}
	}
	// Rotation guard: truncate a feed that outgrew the cap. The consumer
	// treats "file smaller than my offset" as a rotation signal and re-reads
	// from the start, so truncation never causes missed records.
	if info, err := os.Stat(path); err == nil && info.Size() > maxUsageFeedBytes {
		if err := os.Truncate(path, 0); err != nil {
			usageFeedWarnf("truncate %s: %v", path, err)
			return
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		usageFeedWarnf("open %s: %v", path, err)
		return
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		usageFeedWarnf("write %s: %v", path, err)
	}
}

func usageFeedWarnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[trae] usage feed: "+format+"\n", args...)
}
