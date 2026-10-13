package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// lockTempFeed points the shared feed at a temp file for one test.
func lockTempFeed(t *testing.T, path string) {
	t.Helper()
	usageFeedMu.Lock()
	oldEnabled := usageFeedEnabled
	oldPath := usageFeedPath
	usageFeedEnabled = true
	usageFeedPath = path
	usageFeedMu.Unlock()
	t.Cleanup(func() {
		usageFeedMu.Lock()
		usageFeedEnabled = oldEnabled
		usageFeedPath = oldPath
		usageFeedMu.Unlock()
	})
}

func feedLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read feed: %v", err)
	}
	var out []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

func TestHandleUsageAppendsOneFeedLine(t *testing.T) {
	dir := t.TempDir()
	feedPath := filepath.Join(dir, "feed.ndjson")
	lockTempFeed(t, feedPath)

	started := time.Now().Add(-3 * time.Second).Truncate(time.Millisecond)
	rec := pluginapi.UsageRecord{
		Provider:        providerName,
		ExecutorType:    "mimo",
		Model:           "mimo-pro",
		Alias:           "alias-m",
		AuthID:          "u-1",
		AuthIndex:       "idx-1",
		RequestedAt:     started,
		Latency:         3 * time.Second,
		TTFT:            850 * time.Millisecond,
		ReasoningEffort: "high",
		Failed:          false,
		Detail: pluginapi.UsageDetail{
			InputTokens:     100,
			OutputTokens:    200,
			ReasoningTokens: 50,
		},
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	out, err := handleUsage(raw)
	if err != nil {
		t.Fatalf("handleUsage: %v", err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope ok = false: %s", out)
	}
	var fwd struct {
		Forwarded bool `json:"forwarded"`
	}
	if err := json.Unmarshal(env.Result, &fwd); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !fwd.Forwarded {
		t.Fatalf("forwarded = false, want true")
	}

	lines := feedLines(t, feedPath)
	if len(lines) != 1 {
		t.Fatalf("got %d feed lines, want exactly 1", len(lines))
	}
	var got struct {
		Timestamp       string `json:"timestamp"`
		Source          string `json:"source"`
		AuthIndex       string `json:"auth_index"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
		Alias           string `json:"alias"`
		Failed          bool   `json:"failed"`
		StatusCode      int    `json:"status_code"`
		SessionKey      string `json:"session_key"`
		ReasoningEffort string `json:"reasoning_effort"`
		TTFTNS          uint64 `json:"ttft_ns"`
		Tokens          struct {
			Input     int64 `json:"input_tokens"`
			Output    int64 `json:"output_tokens"`
			Reasoning int64 `json:"reasoning_tokens"`
			Total     int64 `json:"total_tokens"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("feed line not json-parseable: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.Timestamp); err != nil {
		t.Fatalf("timestamp %q not RFC3339Nano: %v", got.Timestamp, err)
	}
	if got.Provider != providerName {
		t.Fatalf("provider = %q, want %q", got.Provider, providerName)
	}
	if got.Model != "mimo-pro" || got.Alias != "alias-m" {
		t.Fatalf("model/alias = %q/%q", got.Model, got.Alias)
	}
	if got.AuthIndex != "u-1" {
		t.Fatalf("auth_index = %q, want u-1", got.AuthIndex)
	}
	if got.Tokens.Input != 100 || got.Tokens.Output != 200 || got.Tokens.Reasoning != 50 {
		t.Fatalf("tokens = %+v", got.Tokens)
	}
	if got.Tokens.Total != 350 {
		t.Fatalf("total_tokens = %d, want 350 (recomputed)", got.Tokens.Total)
	}
	if got.TTFTNS != uint64(850*time.Millisecond) {
		t.Fatalf("ttft_ns = %d, want %d", got.TTFTNS, uint64(850*time.Millisecond))
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", got.ReasoningEffort)
	}
	// Known gap: scheduler session key is not carried in UsageRecord.
	if got.SessionKey != "" {
		t.Fatalf("session_key = %q, want empty (known gap)", got.SessionKey)
	}
	if got.Failed {
		t.Fatal("failed should be false")
	}
}

func TestHandleUsageMalformedFixture(t *testing.T) {
	dir := t.TempDir()
	feedPath := filepath.Join(dir, "feed.ndjson")
	lockTempFeed(t, feedPath)

	// Malformed JSON must not panic: handleUsage records the error and
	// continues (returns it to the host), writing no bad line.
	out, err := handleUsage([]byte("{not valid json"))
	if err == nil {
		t.Fatalf("expected error, got out=%s", out)
	}
	if _, statErr := os.Stat(feedPath); !os.IsNotExist(statErr) {
		if lines := feedLines(t, feedPath); len(lines) != 0 {
			t.Fatalf("malformed fixture wrote %d bad lines", len(lines))
		}
	}

	// Foreign-provider records route to their own plugin: no forward, no line.
	foreign, _ := json.Marshal(pluginapi.UsageRecord{Provider: "some-other-plugin", Model: "m"})
	out, err = handleUsage(foreign)
	if err != nil {
		t.Fatalf("foreign provider handleUsage: %v", err)
	}
	var env2 struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &env2); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var fwd2 struct {
		Forwarded bool `json:"forwarded"`
	}
	if err := json.Unmarshal(env2.Result, &fwd2); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if fwd2.Forwarded {
		t.Fatalf("forwarded = true, want false")
	}
	if _, statErr := os.Stat(feedPath); !os.IsNotExist(statErr) {
		if lines := feedLines(t, feedPath); len(lines) != 0 {
			t.Fatalf("foreign record wrote %d lines, want 0", len(lines))
		}
	}
}

func TestConfigureUsageFeedParsesConfigYAML(t *testing.T) {
	usageFeedMu.Lock()
	oldEnabled := usageFeedEnabled
	oldPath := usageFeedPath
	usageFeedMu.Unlock()
	t.Cleanup(func() {
		usageFeedMu.Lock()
		usageFeedEnabled = oldEnabled
		usageFeedPath = oldPath
		usageFeedMu.Unlock()
	})

	dir := t.TempDir()
	// The host serializes config_yaml as base64 (ConfigYAML []byte over the
	// RPC wire); pass a []byte value so json.Marshal mimics the real transport.
	raw, _ := json.Marshal(map[string]any{
		"config_yaml": []byte("usage_feed_enabled: true\nusage_feed_path: \"" + filepath.Join(dir, "feed.ndjson") + "\"\n"),
	})
	configureUsageFeed(raw)
	usageFeedMu.RLock()
	enabled := usageFeedEnabled
	path := usageFeedPath
	usageFeedMu.RUnlock()
	if !enabled {
		t.Fatal("feed should be enabled")
	}
	if path != filepath.Join(dir, "feed.ndjson") {
		t.Fatalf("feed path = %q", path)
	}

	raw, _ = json.Marshal(map[string]any{
		"config_yaml": []byte("usage_feed_enabled: false\n"),
	})
	configureUsageFeed(raw)
	usageFeedMu.RLock()
	enabled = usageFeedEnabled
	usageFeedMu.RUnlock()
	if enabled {
		t.Fatal("feed should be disabled")
	}
}

func TestAppendUsageFeedLineRotation(t *testing.T) {
	dir := t.TempDir()
	feedPath := filepath.Join(dir, "feed.ndjson")

	oldCap := maxUsageFeedBytes
	maxUsageFeedBytes = 64 // tiny cap for the test
	defer func() { maxUsageFeedBytes = oldCap }()

	// Exceed the cap: a 100-byte line triggers truncation on the next append
	// (R1 semantics: consumer treats shrink as rotation signal, re-reads
	// from 0, nothing double-imported).
	bigLine := string(make([]byte, 100)) // 100 NUL bytes; only the size matters
	if err := os.WriteFile(feedPath, []byte(bigLine), 0o644); err != nil {
		t.Fatal(err)
	}
	appendUsageFeedLine(feedPath, []byte("{\"a\":1}\n"))
	raw, err := os.ReadFile(feedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\"a\":1}\n" {
		t.Fatalf("feed content after rotation = %q", raw)
	}
}

// TestUsageHandleRegistration is the T14 adaptation assertion: mimo
// previously shipped UsagePlugin=false with no MethodUsageHandle dispatch.
// The registration must advertise the capability AND handleMethod must route
// the host callback to the feed writer.
func TestUsageHandleRegistration(t *testing.T) {
	reg := mimoRegistration()
	if !reg.Capabilities.UsagePlugin {
		t.Fatal("mimoRegistration must advertise UsagePlugin=true (plan T14)")
	}

	dir := t.TempDir()
	feedPath := filepath.Join(dir, "feed.ndjson")
	lockTempFeed(t, feedPath)

	rec, _ := json.Marshal(pluginapi.UsageRecord{
		Provider:    providerName,
		Model:       "mimo-pro",
		Alias:       "alias-m",
		AuthID:      "u-9",
		RequestedAt: time.Now().Add(-time.Second).Truncate(time.Millisecond),
		Detail:      pluginapi.UsageDetail{InputTokens: 10, OutputTokens: 20},
	})
	out, err := handleMethod(string(pluginabi.MethodUsageHandle), rec)
	if err != nil {
		t.Fatalf("handleMethod(MethodUsageHandle): %v", err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope not ok: %s / %v", out, err)
	}
	var fwd struct {
		Forwarded bool `json:"forwarded"`
	}
	if err := json.Unmarshal(env.Result, &fwd); err != nil || !fwd.Forwarded {
		t.Fatalf("forwarded = false via handleMethod dispatch: %s", env.Result)
	}
	if lines := feedLines(t, feedPath); len(lines) != 1 {
		t.Fatalf("dispatch wrote %d feed lines, want exactly 1", len(lines))
	}
}
