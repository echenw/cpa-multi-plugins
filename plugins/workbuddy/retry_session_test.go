package main

// T17 acceptance tests: retry_on_4xx same-request account rotation + session
// affinity for plugins/workbuddy.
//
// The executor loops (handleExecExecute, collectUpstreamStream) are driven
// hermetically: setChatUpstreamBase points every account at an httptest
// server that answers per bearer token, and pickNextAuthFn is stubbed with
// in-memory accounts (the real pickNextAuth needs host RPC). Upstream bodies
// are deliberately credit-neutral ("unauthorized", "bad request") so the
// lifecycle reconcile never fires and no host calls escape the test.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// t17SSEExecute is a minimal successful chat-completion SSE body for the
// non-stream fold path (aggregateCompletion).
const t17SSEExecute = "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// t17SSEStream is a minimal successful SSE body for the sync stream path
// (aggregateSSEWithCollector needs one meaningful content delta).
const t17SSEStream = "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// t17Upstream is a mock CodeBuddy gateway. mode "rotate" fails token-a with
// 401 and serves token-b with 200 SSE; mode "all401" fails everything;
// mode "bad400" answers business 400 to everything.
type t17Upstream struct {
	mu    sync.Mutex
	mode  string
	seen  []string
	calls int
}

func (u *t17Upstream) handler(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.mu.Lock()
	u.seen = append(u.seen, auth)
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
		if auth == "token-b" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(t17SSEExecute))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":11101,"msg":"unauthorized, token expired"}`))
	}
}

// t17StreamUpstream is the same mock but serves the stream-shape SSE body.
type t17StreamUpstream struct {
	t17Upstream
}

func (u *t17StreamUpstream) handler(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.mu.Lock()
	u.seen = append(u.seen, auth)
	u.calls++
	u.mu.Unlock()
	if auth == "token-b" {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(t17SSEStream))
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":11101,"msg":"unauthorized, token expired"}`))
}

func (u *t17Upstream) snapshot() (seen []string, calls int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.seen...), u.calls
}

func t17Stored(token, uid string) *storedAuth {
	return &storedAuth{
		Auth:    storedTokens{AccessToken: token, RefreshToken: "refresh-" + token},
		Account: storedAccount{UID: uid, Nickname: "nick-" + uid},
	}
}

func t17Storage(t *testing.T, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal storedAuth: %v", err)
	}
	return raw
}

func t17ExecRaw(t *testing.T, authID string, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"AuthID":      authID,
		"Model":       "glm",
		"Payload":     []byte(`{"model":"glm","messages":[{"role":"user","content":"hi"}]}`),
		"StorageJSON": t17Storage(t, sa),
	})
	if err != nil {
		t.Fatalf("marshal exec request: %v", err)
	}
	return raw
}

func t17StreamRaw(t *testing.T, authID string, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"AuthID":      authID,
		"Model":       "glm",
		"Payload":     []byte(`{"model":"glm","messages":[{"role":"user","content":"hi"}]}`),
		"StorageJSON": t17Storage(t, sa),
	})
	if err != nil {
		t.Fatalf("marshal stream request: %v", err)
	}
	return raw
}

// t17Setup points the chat upstream at a mock server, resets the retry
// budget, and stubs account failover to a two-account pool (wb-a → wb-b).
// Callers may replace pickNextAuthFn afterwards for custom topologies.
func t17Setup(t *testing.T, up *t17Upstream, handler http.HandlerFunc) (*storedAuth, *storedAuth) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(setChatUpstreamBase(srv.URL))
	oldBudget := loadedRetryOn4xx()
	setRetryOn4xx(retryOn4xxDefault)
	t.Cleanup(func() { setRetryOn4xx(oldBudget) })
	oldPick := pickNextAuthFn
	t.Cleanup(func() { pickNextAuthFn = oldPick })
	clearModelCooldown("wb-a", "glm")
	clearModelCooldown("wb-b", "glm")
	saA := t17Stored("token-a", "uid-a")
	saB := t17Stored("token-b", "uid-b")
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "wb-a" {
			return "wb-b", saB, true
		}
		return "", nil, false
	}
	return saA, saB
}

func t17DecodeCompletion(t *testing.T, out []byte) string {
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

func t17DecodeChunks(t *testing.T, out []byte) string {
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

func equalStrings(a, b []string) bool {
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

// (a) mock 401 → same-request rotation succeeds; exactly 2 accounts used.
// Covers both single-shot paths: handleExecExecute and the sync branch of
// handleExecStream (StreamID == "" → collectUpstreamStream loop).
func TestT17_RetryOn4xx_RotatesOn401(t *testing.T) {
	up := &t17Upstream{}
	saA, _ := t17Setup(t, up, up.handler)

	out, err := handleExecExecute(t17ExecRaw(t, "wb-a", saA))
	if err != nil {
		t.Fatalf("execute after rotation: %v", err)
	}
	if got := t17DecodeCompletion(t, out); !strings.Contains(got, "Hello") {
		t.Fatalf("rotated completion missing content: %s", got)
	}
	if seen, _ := up.snapshot(); !equalStrings(seen, []string{"token-a", "token-b"}) {
		t.Fatalf("execute path must use exactly 2 accounts in order, got %q", seen)
	}

	su := &t17StreamUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(su.handler))
	defer srv.Close()
	chatUpstreamBaseOverride = srv.URL
	defer func() { chatUpstreamBaseOverride = "" }()

	out, err = handleExecStream(t17StreamRaw(t, "wb-a", saA))
	if err != nil {
		t.Fatalf("sync stream after rotation: %v", err)
	}
	if got := t17DecodeChunks(t, out); !strings.Contains(got, "hi") {
		t.Fatalf("rotated stream chunks missing content: %q", got)
	}
	su.mu.Lock()
	gotSeen := append([]string(nil), su.seen...)
	su.mu.Unlock()
	if !equalStrings(gotSeen, []string{"token-a", "token-b"}) {
		t.Fatalf("stream path must use exactly 2 accounts in order, got %q", gotSeen)
	}
}

// (b) business 400 does NOT rotate: exactly 1 upstream call, error surfaces.
func TestT17_RetryOn4xx_Business400NoRotate(t *testing.T) {
	up := &t17Upstream{mode: "bad400"}
	saA, _ := t17Setup(t, up, up.handler)

	_, err := handleExecExecute(t17ExecRaw(t, "wb-a", saA))
	if err == nil {
		t.Fatal("business 400 must surface an error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected the original 400 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 1 || !equalStrings(seen, []string{"token-a"}) {
		t.Fatalf("business 400 must not rotate: calls=%d seen=%q", calls, seen)
	}
}

// (c) retry_on_4xx=0 kill switch: 401 surfaces after exactly 1 call.
func TestT17_RetryOn4xx_KillSwitch(t *testing.T) {
	up := &t17Upstream{}
	saA, _ := t17Setup(t, up, up.handler)
	setRetryOn4xx(0)

	_, err := handleExecExecute(t17ExecRaw(t, "wb-a", saA))
	if err == nil {
		t.Fatal("with budget 0, 401 must surface an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected the original 401 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 1 || !equalStrings(seen, []string{"token-a"}) {
		t.Fatalf("kill switch must allow exactly 1 call: calls=%d seen=%q", calls, seen)
	}
}

// (d) missing key preserved: an unrelated reconfigure must not lift the
// kill switch; explicit values apply clamped; garbage resets to default.
func TestT17_RetryOn4xx_MissingKeyPreserved(t *testing.T) {
	old := loadedRetryOn4xx()
	defer setRetryOn4xx(old)
	oldMode := loadedSchedulerMode()
	defer setSchedulerMode(oldMode)()
	setRetryOn4xx(retryOn4xxDefault)

	configure(configYAMLEnvelope("retry_on_4xx: 0\n"))
	if got := loadedRetryOn4xx(); got != 0 {
		t.Fatalf("explicit 0 must apply, got %d", got)
	}
	configure(configYAMLEnvelope("checkin_auto: true\n"))
	if got := loadedRetryOn4xx(); got != 0 {
		t.Fatalf("missing key must preserve current budget 0, got %d", got)
	}
	configure(configYAMLEnvelope("retry_on_4xx: 99\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxMax {
		t.Fatalf("99 must clamp to %d, got %d", retryOn4xxMax, got)
	}
	configure(configYAMLEnvelope("retry_on_4xx: abc\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxDefault {
		t.Fatalf("garbage must reset to default %d, got %d", retryOn4xxDefault, got)
	}
	configure(configYAMLEnvelope("retry_on_4xx: 3 # comment\n"))
	if got := loadedRetryOn4xx(); got != 3 {
		t.Fatalf("inline comment must parse, got %d", got)
	}
}

// (e) session affinity under scheduler_mode=session: same key twice → same
// account; different keys spread; rotation evicts the binding; session is
// inert under credits/off modes.
func TestT17_SessionAffinity(t *testing.T) {
	clearSessionBindings()
	t.Cleanup(clearSessionBindings)
	oldMode := loadedSchedulerMode()
	t.Cleanup(func() { setSchedulerMode(oldMode) })
	setActiveAuthID("")
	t.Cleanup(func() { setActiveAuthID("") })
	setSchedulerMode(schedulerModeSession)
	clearModelCooldown("wb-a", "glm")
	clearModelCooldown("wb-b", "glm")

	pick := func(meta map[string]any) pluginapi.SchedulerPickResponse {
		t.Helper()
		raw, err := json.Marshal(pluginapi.SchedulerPickRequest{
			Provider: providerName,
			Model:    "glm",
			Options:  pluginapi.SchedulerOptions{Metadata: meta},
			Candidates: []pluginapi.SchedulerAuthCandidate{
				{ID: "wb-a", Provider: providerName},
				{ID: "wb-b", Provider: providerName},
			},
		})
		if err != nil {
			t.Fatalf("marshal pick: %v", err)
		}
		resp, err := handleSchedulerPick(raw)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		return parsePickResponse(t, resp)
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
	setActiveAuthID("wb-b")
	fallback := pick(nil)
	if !fallback.Handled || fallback.AuthID != "wb-b" {
		t.Fatalf("no session key must fall back to panel pick wb-b, got %+v", fallback)
	}

	// Session is inert outside session mode: credits ignores the key.
	setSchedulerMode(schedulerModeCredits)
	setActiveAuthID("wb-b")
	cred := pick(meta("conv-1"))
	if !cred.Handled || cred.AuthID != "wb-b" {
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
func TestT17_RetryOn4xx_BudgetExhausted(t *testing.T) {
	up := &t17Upstream{mode: "all401"}
	saA, saB := t17Setup(t, up, up.handler)
	setRetryOn4xx(2)
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "wb-a" {
			return "wb-b", saB, true
		}
		if cur == "wb-b" {
			return "wb-a", saA, true
		}
		return "", nil, false
	}

	_, err := handleExecExecute(t17ExecRaw(t, "wb-a", saA))
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
