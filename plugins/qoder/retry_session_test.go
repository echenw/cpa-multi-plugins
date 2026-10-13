package main

// T18 acceptance tests: retry_on_4xx same-request account rotation + session
// affinity for plugins/qoder (T17 workbuddy port, qoder edition).
//
// The executor loops (handleExecExecute, collectUpstreamStreamQoder) are
// driven hermetically: setQoderChatBase points every account at an httptest
// server that answers per cosy-user identity header, and pickNextAuthFn is
// stubbed with in-memory accounts (the real pickNextAuth needs host RPC).
// Upstream bodies are deliberately credit-neutral ("unauthorized",
// "bad request") so the lifecycle reconcile never fires and no host calls
// escape the test. publishUsage is fire-and-forget to an unconfigured CPAMP
// endpoint (no key → immediate no-op).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// t18Upstream is a mock QoderWork gateway. mode "rotate" fails uid-a with
// 401 and serves uid-b with 200 SSE answer frames; mode "all401" fails
// everything; mode "bad400" answers business 400 to everything. Accounts
// are told apart by the cosy-user identity header applyCosyHeaders signs
// per account.
type t18Upstream struct {
	mu    sync.Mutex
	mode  string
	seen  []string
	calls int
}

func (u *t18Upstream) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		uid := r.Header.Get("Cosy-User")
		u.mu.Lock()
		u.seen = append(u.seen, uid)
		u.calls++
		u.mu.Unlock()
		switch u.mode {
		case "bad400":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":400,"msg":"bad request"}`))
			return
		case "all401":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":11101,"msg":"unauthorized, token expired"}`))
			return
		default: // rotate
			if uid == "uid-b" {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, qoderOpener(t)+qoderAnswer(t, "hi")+qoderDone())
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":11101,"msg":"unauthorized, token expired"}`))
		}
	}
}

func (u *t18Upstream) snapshot() (seen []string, calls int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.seen...), u.calls
}

func t18Stored(token, uid string) *storedAuth {
	return &storedAuth{
		Auth:    storedTokens{AccessToken: token, RefreshToken: "refresh-" + token},
		Account: storedAccount{UID: uid, Nickname: "nick-" + uid},
	}
}

func t18Storage(t *testing.T, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal storedAuth: %v", err)
	}
	return raw
}

func t18ExecRaw(t *testing.T, authID string, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"AuthID":      authID,
		"Model":       "t18model",
		"Payload":     []byte(`{"model":"t18model","messages":[{"role":"user","content":"hi"}]}`),
		"StorageJSON": t18Storage(t, sa),
	})
	if err != nil {
		t.Fatalf("marshal exec request: %v", err)
	}
	return raw
}

func t18StreamRaw(t *testing.T, authID string, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"AuthID":      authID,
		"Model":       "t18model",
		"Payload":     []byte(`{"model":"t18model","messages":[{"role":"user","content":"hi"}]}`),
		"StorageJSON": t18Storage(t, sa),
	})
	if err != nil {
		t.Fatalf("marshal stream request: %v", err)
	}
	return raw
}

// t18Setup points the chat endpoint at a mock server, resets the retry
// budget, and stubs account failover to a two-account pool (qd-a → qd-b).
// Callers may replace pickNextAuthFn afterwards for custom topologies.
func t18Setup(t *testing.T, up *t18Upstream) (*storedAuth, *storedAuth) {
	t.Helper()
	srv := httptest.NewServer(up.handler(t))
	t.Cleanup(srv.Close)
	t.Cleanup(setQoderChatBase(srv.URL))
	oldBudget := loadedRetryOn4xx()
	setRetryOn4xx(retryOn4xxDefault)
	t.Cleanup(func() { setRetryOn4xx(oldBudget) })
	oldPick := pickNextAuthFn
	t.Cleanup(func() { pickNextAuthFn = oldPick })
	clearModelCooldown("qd-a", "t18model")
	clearModelCooldown("qd-b", "t18model")
	t.Cleanup(func() {
		clearModelCooldown("qd-a", "t18model")
		clearModelCooldown("qd-b", "t18model")
	})
	saA := t18Stored("token-a", "uid-a")
	saB := t18Stored("token-b", "uid-b")
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "qd-a" {
			return "qd-b", saB, true
		}
		return "", nil, false
	}
	return saA, saB
}

func t18DecodeCompletion(t *testing.T, out []byte) string {
	t.Helper()
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("expected ok envelope, got err=%v out=%s", err, out)
	}
	var res struct {
		Payload []byte `json:"payload"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode executor response: %v", err)
	}
	return string(res.Payload)
}

func t18DecodeChunks(t *testing.T, out []byte) string {
	t.Helper()
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("expected ok envelope, got err=%v out=%s", err, out)
	}
	var res struct {
		Chunks []struct {
			Payload []byte `json:"payload"`
		} `json:"chunks"`
	}
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode stream response: %v", err)
	}
	var sb strings.Builder
	for _, c := range res.Chunks {
		sb.Write(c.Payload)
	}
	return sb.String()
}

func t18EqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func t18Wire(cfg string) []byte {
	b, _ := json.Marshal(struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{ConfigYAML: []byte(cfg)})
	return b
}

// (a) mock 401 → same-request rotation succeeds; exactly 2 accounts used.
// Covers both single-shot paths: handleExecExecute and the sync branch of
// handleExecStream (StreamID == "" → collectUpstreamStreamQoder loop).
func TestT18_RetryOn4xx_RotatesOn401(t *testing.T) {
	up := &t18Upstream{}
	saA, _ := t18Setup(t, up)

	out, err := handleExecExecute(t18ExecRaw(t, "qd-a", saA))
	if err != nil {
		t.Fatalf("execute after rotation: %v", err)
	}
	if got := t18DecodeCompletion(t, out); !strings.Contains(got, "hi") {
		t.Fatalf("rotated completion missing content: %s", got)
	}
	if seen, _ := up.snapshot(); !t18EqualStrings(seen, []string{"uid-a", "uid-b"}) {
		t.Fatalf("execute path must use exactly 2 accounts in order, got %q", seen)
	}

	out, err = handleExecStream(t18StreamRaw(t, "qd-a", saA))
	if err != nil {
		t.Fatalf("sync stream after rotation: %v", err)
	}
	if got := t18DecodeChunks(t, out); !strings.Contains(got, "hi") {
		t.Fatalf("rotated stream chunks missing content: %q", got)
	}
	if seen, calls := up.snapshot(); calls != 4 || !t18EqualStrings(seen, []string{"uid-a", "uid-b", "uid-a", "uid-b"}) {
		t.Fatalf("stream path must rotate the same way, calls=%d seen=%q", calls, seen)
	}
}

// (b) business 400 does NOT rotate: exactly 1 upstream call, error surfaces.
func TestT18_RetryOn4xx_Business400NoRotate(t *testing.T) {
	up := &t18Upstream{mode: "bad400"}
	saA, _ := t18Setup(t, up)

	_, err := handleExecExecute(t18ExecRaw(t, "qd-a", saA))
	if err == nil {
		t.Fatal("business 400 must surface an error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected the original 400 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 1 || !t18EqualStrings(seen, []string{"uid-a"}) {
		t.Fatalf("business 400 must not rotate: calls=%d seen=%q", calls, seen)
	}
}

// (c) retry_on_4xx=0 kill switch: 401 surfaces after exactly 1 call.
func TestT18_RetryOn4xx_KillSwitch(t *testing.T) {
	up := &t18Upstream{}
	saA, _ := t18Setup(t, up)
	setRetryOn4xx(0)

	_, err := handleExecExecute(t18ExecRaw(t, "qd-a", saA))
	if err == nil {
		t.Fatal("with budget 0, 401 must surface an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected the original 401 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 1 || !t18EqualStrings(seen, []string{"uid-a"}) {
		t.Fatalf("kill switch must allow exactly 1 call: calls=%d seen=%q", calls, seen)
	}
}

// (d) missing key preserved: an unrelated reconfigure must not lift the
// kill switch; explicit values apply clamped; garbage resets to default.
// Block style pins the line-scan path, flow style the YAML-map path —
// configure() must honor retry_on_4xx on both.
func TestT18_RetryOn4xx_MissingKeyPreserved(t *testing.T) {
	old := loadedRetryOn4xx()
	defer setRetryOn4xx(old)
	oldMode := loadedSchedulerMode()
	defer setSchedulerMode(oldMode)()
	setRetryOn4xx(retryOn4xxDefault)

	configure(t18Wire("retry_on_4xx: 0\n"))
	if got := loadedRetryOn4xx(); got != 0 {
		t.Fatalf("explicit 0 must apply, got %d", got)
	}
	configure(t18Wire("checkin_auto: true\n"))
	if got := loadedRetryOn4xx(); got != 0 {
		t.Fatalf("missing key must preserve current budget 0, got %d", got)
	}
	configure(t18Wire("retry_on_4xx: 99\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxMax {
		t.Fatalf("99 must clamp to %d, got %d", retryOn4xxMax, got)
	}
	configure(t18Wire("retry_on_4xx: abc\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxDefault {
		t.Fatalf("garbage must reset to default %d, got %d", retryOn4xxDefault, got)
	}
	configure(t18Wire("retry_on_4xx: 3 # comment\n"))
	if got := loadedRetryOn4xx(); got != 3 {
		t.Fatalf("inline comment must parse, got %d", got)
	}
	// Flow-style / JSON payloads only reach the YAML-map decode path.
	configure(t18Wire("{enabled: true, retry_on_4xx: 5, priority: 0}\n"))
	if got := loadedRetryOn4xx(); got != 5 {
		t.Fatalf("flow-style 5 must apply via the map path, got %d", got)
	}
	configure(t18Wire(`{"enabled":true,"scheduler_mode":"session"}`))
	if got := loadedSchedulerMode(); got != schedulerModeSession {
		t.Fatalf("flow-style session mode must apply via the map path, got %q", got)
	}
	if got := loadedRetryOn4xx(); got != 5 {
		t.Fatalf("unrelated flow-style reconfigure must preserve budget 5, got %d", got)
	}
}

// (e) session affinity under scheduler_mode=session: same key twice → same
// account; different keys spread; rotation evicts the binding; session is
// inert under credits/off modes.
func TestT18_SessionAffinity(t *testing.T) {
	clearSessionBindings()
	t.Cleanup(clearSessionBindings)
	oldMode := loadedSchedulerMode()
	t.Cleanup(func() { setSchedulerMode(oldMode) })
	setActiveAuthID("")
	t.Cleanup(func() { setActiveAuthID("") })
	setSchedulerMode(schedulerModeSession)
	clearModelCooldown("qd-a", "t18session")
	clearModelCooldown("qd-b", "t18session")
	t.Cleanup(func() {
		clearModelCooldown("qd-a", "t18session")
		clearModelCooldown("qd-b", "t18session")
	})

	pick := func(meta map[string]any) pluginapi.SchedulerPickResponse {
		t.Helper()
		raw, err := json.Marshal(pluginapi.SchedulerPickRequest{
			Provider: providerName,
			Model:    "t18session",
			Options:  pluginapi.SchedulerOptions{Metadata: meta},
			Candidates: []pluginapi.SchedulerAuthCandidate{
				{ID: "qd-a", Provider: providerName},
				{ID: "qd-b", Provider: providerName},
			},
		})
		if err != nil {
			t.Fatalf("marshal pick: %v", err)
		}
		resp, err := handleSchedulerPick(raw)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		var env struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(resp, &env); err != nil || !env.OK {
			t.Fatalf("pick envelope: err=%v resp=%s", err, resp)
		}
		var out pluginapi.SchedulerPickResponse
		if err := json.Unmarshal(env.Result, &out); err != nil {
			t.Fatalf("decode pick response: %v", err)
		}
		return out
	}
	meta := func(conv string) map[string]any {
		return map[string]any{"derived_session_id": conv}
	}

	first := pick(meta("conv-1"))
	if !first.Handled || first.AuthID == "" {
		t.Fatalf("session pick must handle, got %+v", first)
	}
	second := pick(meta("conv-1"))
	if second.AuthID != first.AuthID {
		t.Fatalf("same key must stick to %q, got %q", first.AuthID, second.AuthID)
	}
	other := pick(meta("conv-2"))
	if other.AuthID == first.AuthID {
		t.Fatalf("different conversation must spread, both got %q", first.AuthID)
	}

	// Rotation unbinds: evict the stuck account, the conversation re-picks.
	if n := evictSessionBindingsForAuth(first.AuthID); n != 1 {
		t.Fatalf("evict must remove exactly 1 binding, got %d", n)
	}
	repick := pick(meta("conv-1"))
	if !repick.Handled || repick.AuthID == "" {
		t.Fatalf("re-pick after evict must handle, got %+v", repick)
	}

	// No session signal → panel-selected fallback (same as credits).
	setActiveAuthID("qd-b")
	fallback := pick(nil)
	if !fallback.Handled || fallback.AuthID != "qd-b" {
		t.Fatalf("no session key must fall back to panel pick qd-b, got %+v", fallback)
	}

	// Session is inert outside session mode: credits ignores the key.
	setSchedulerMode(schedulerModeCredits)
	setActiveAuthID("qd-b")
	cred := pick(meta("conv-1"))
	if !cred.Handled || cred.AuthID != "qd-b" {
		t.Fatalf("credits mode must follow panel pick, got %+v", cred)
	}

	// Default off (unchanged) defers everything.
	setSchedulerMode(schedulerModeOff)
	off := pick(meta("conv-1"))
	if off.Handled {
		t.Fatalf("off mode must defer, got %+v", off)
	}
}

// Failure case: every account 401 → budget exhausted → original error
// returned, attempts bounded (no infinite loop).
func TestT18_RetryOn4xx_BudgetExhausted(t *testing.T) {
	up := &t18Upstream{mode: "all401"}
	saA, saB := t18Setup(t, up)
	setRetryOn4xx(2)
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "qd-a" {
			return "qd-b", saB, true
		}
		if cur == "qd-b" {
			return "qd-a", saA, true
		}
		return "", nil, false
	}

	_, err := handleExecExecute(t18ExecRaw(t, "qd-a", saA))
	if err == nil {
		t.Fatal("all-401 must surface an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("must return the original 401 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 3 {
		t.Fatalf("budget 2 must bound attempts to 3, got %d (seen=%q)", calls, seen)
	}
}
