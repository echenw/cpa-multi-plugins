package main

// T20 acceptance tests: retry_on_4xx same-request account rotation for
// plugins/zcode (port of R1 zcode/executor.go:48's fixed `for attempt < 3`,
// default 3) + the account ladder cooldown (R1 zcode/failover.go:22/:45/:82
// semantics in cooldown.go).
//
// The executor loops (handleExecExecute, handleExecStreamSync) are driven
// hermetically: openAIBaseZai points the coding-plan route at an httptest
// server that answers per bearer token, and pickNextAuthFn is stubbed with
// in-memory accounts (the real pickNextAuth needs host RPC). Upstream bodies
// are deliberately credit-neutral ("unauthorized", "bad request") so the
// lifecycle reconcile never fires and no host calls escape the test.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// t20CompletionOK is a minimal successful coding-plan completion body.
const t20CompletionOK = `{"id":"c1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`

// t20StreamOK is a minimal successful coding-plan SSE body for the sync
// stream path (one meaningful content delta + terminator).
const t20StreamOK = "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// t20Upstream is a mock coding-plan gateway. mode "rotate" fails token-a
// with 401 and serves token-b with 200; mode "all401" fails everything;
// mode "bad400" answers business 400 to everything.
type t20Upstream struct {
	mu    sync.Mutex
	mode  string
	serve string
	seen  []string
	calls int
}

func (u *t20Upstream) handler(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	u.mu.Lock()
	u.seen = append(u.seen, auth)
	u.calls++
	u.mu.Unlock()
	switch u.mode {
	case "bad400":
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request shape"}}`))
		return
	case "all401":
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":11101,"msg":"unauthorized, token expired"}`))
		return
	default: // rotate
		if auth == "token-b" {
			_, _ = w.Write([]byte(u.serve))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":11101,"msg":"unauthorized, token expired"}`))
	}
}

func (u *t20Upstream) snapshot() (seen []string, calls int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.seen...), u.calls
}

func t20Stored(token, uid string) *storedAuth {
	return &storedAuth{
		Auth:    zcodeTokens{AccessToken: token, Provider: providerZai, Plan: planCoding, DeviceMid: "device-mid-1"},
		Account: zcodeAccount{UID: uid},
	}
}

func t20Storage(t *testing.T, sa *storedAuth) []byte {
	t.Helper()
	raw, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal storedAuth: %v", err)
	}
	return raw
}

func t20ExecRaw(t *testing.T, authID string, sa *storedAuth) []byte {
	t.Helper()
	payloadRaw, _ := json.Marshal(map[string]any{
		"model":    "zcode/glm-4",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	raw, err := json.Marshal(map[string]any{
		"AuthID":      authID,
		"Model":       "zcode/glm-4",
		"Payload":     base64.StdEncoding.EncodeToString(payloadRaw),
		"StorageJSON": t20Storage(t, sa),
	})
	if err != nil {
		t.Fatalf("marshal exec request: %v", err)
	}
	return raw
}

func t20StreamRaw(t *testing.T, authID string, sa *storedAuth) []byte {
	t.Helper()
	// Same wire shape as the non-stream request; absent StreamID selects
	// the synchronous collect path inside handleExecStream.
	return t20ExecRaw(t, authID, sa)
}

// t20Setup points the coding-plan route at a mock server, resets the retry
// budget to the default, clears ladder state for the given ids, and stubs
// account failover. Returns the two accounts (token-a/token-b).
func t20Setup(t *testing.T, mode, serve string, ids ...string) (*t20Upstream, *storedAuth, *storedAuth) {
	t.Helper()
	up := &t20Upstream{mode: mode, serve: serve}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	origBase := openAIBaseZai
	openAIBaseZai = srv.URL
	t.Cleanup(func() { openAIBaseZai = origBase })
	oldBudget := loadedRetryOn4xx()
	setRetryOn4xx(retryOn4xxDefault)
	t.Cleanup(func() { setRetryOn4xx(oldBudget) })
	oldPick := pickNextAuthFn
	t.Cleanup(func() { pickNextAuthFn = oldPick })
	for _, id := range ids {
		resetAccountFailover(id)
	}
	t.Cleanup(func() {
		for _, id := range ids {
			resetAccountFailover(id)
		}
	})
	saA := t20Stored("token-a", "user-a")
	saB := t20Stored("token-b", "user-b")
	return up, saA, saB
}

func t20DecodeCompletion(t *testing.T, out []byte) string {
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

func t20DecodeChunks(t *testing.T, out []byte) string {
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

func t20EqualStrings(a, b []string) bool {
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

func t20ConfigEnvelope(yaml string) []byte {
	raw, err := json.Marshal(struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{ConfigYAML: []byte(yaml)})
	if err != nil {
		panic(err)
	}
	return raw
}

// mock 401 on the first account → same-request rotation succeeds on the
// second; the failed account is ladder-cooling afterwards.
func TestT20_RetryOn4xx_RotatesOn401_Execute(t *testing.T) {
	up, saA, saB := t20Setup(t, "rotate", t20CompletionOK, "t20-e-a", "t20-e-b")
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "t20-e-a" {
			return "t20-e-b", saB, true
		}
		return "", nil, false
	}

	out, err := handleExecExecute(t20ExecRaw(t, "t20-e-a", saA))
	if err != nil {
		t.Fatalf("execute after rotation: %v", err)
	}
	if got := t20DecodeCompletion(t, out); !strings.Contains(got, `"ok"`) {
		t.Fatalf("rotated completion missing content: %s", got)
	}
	if seen, _ := up.snapshot(); !t20EqualStrings(seen, []string{"token-a", "token-b"}) {
		t.Fatalf("execute path must use exactly 2 accounts in order, got %q", seen)
	}
	if !isAccountCoolingDown("t20-e-a") {
		t.Fatal("failed account must ladder-cool after a rotated 401")
	}
	if isAccountCoolingDown("t20-e-b") {
		t.Fatal("serving account must not cool after success")
	}
}

// same rotation contract on the synchronous stream path.
func TestT20_RetryOn4xx_RotatesOn401_Stream(t *testing.T) {
	up, saA, saB := t20Setup(t, "rotate", t20StreamOK, "t20-s-a", "t20-s-b")
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "t20-s-a" {
			return "t20-s-b", saB, true
		}
		return "", nil, false
	}

	out, err := handleExecStream(t20StreamRaw(t, "t20-s-a", saA))
	if err != nil {
		t.Fatalf("sync stream after rotation: %v", err)
	}
	if got := t20DecodeChunks(t, out); !strings.Contains(got, "hi") {
		t.Fatalf("rotated stream chunks missing content: %q", got)
	}
	if seen, _ := up.snapshot(); !t20EqualStrings(seen, []string{"token-a", "token-b"}) {
		t.Fatalf("stream path must use exactly 2 accounts in order, got %q", seen)
	}
}

// budget=0 kill switch: 401 surfaces after exactly 1 call, no rotation.
func TestT20_RetryOn4xx_BudgetZero_NoRetry(t *testing.T) {
	up, saA, saB := t20Setup(t, "rotate", t20CompletionOK, "t20-z-a", "t20-z-b")
	_ = saB
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		return "t20-z-b", saB, true
	}
	setRetryOn4xx(0)

	_, err := handleExecExecute(t20ExecRaw(t, "t20-z-a", saA))
	if err == nil {
		t.Fatal("with budget 0, 401 must surface an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected the original 401 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 1 || !t20EqualStrings(seen, []string{"token-a"}) {
		t.Fatalf("kill switch must allow exactly 1 call: calls=%d seen=%q", calls, seen)
	}
}

// business 400 does NOT rotate: exactly 1 upstream call, error surfaces.
func TestT20_RetryOn4xx_Business400_NoRotate(t *testing.T) {
	up, saA, _ := t20Setup(t, "bad400", t20CompletionOK, "t20-b-a", "t20-b-b")

	_, err := handleExecExecute(t20ExecRaw(t, "t20-b-a", saA))
	if err == nil {
		t.Fatal("business 400 must surface an error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected the original 400 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 1 || !t20EqualStrings(seen, []string{"token-a"}) {
		t.Fatalf("business 400 must not rotate: calls=%d seen=%q", calls, seen)
	}
}

// default budget exhausts after initial + 3 attempts, then gives up with
// the original error; every tried account ladder-cools.
func TestT20_RetryOn4xx_DefaultBudget_ExhaustsAndCools(t *testing.T) {
	up, saA, saB := t20Setup(t, "all401", t20CompletionOK, "t20-d-a", "t20-d-b")
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "t20-d-a" {
			return "t20-d-b", saB, true
		}
		if cur == "t20-d-b" {
			return "t20-d-a", saA, true
		}
		return "", nil, false
	}

	_, err := handleExecExecute(t20ExecRaw(t, "t20-d-a", saA))
	if err == nil {
		t.Fatal("all-401 must surface an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("must return the original 401 error, got: %v", err)
	}
	if seen, calls := up.snapshot(); calls != 4 {
		t.Fatalf("default budget 3 must bound attempts to 4, got %d (seen=%q)", calls, seen)
	}
	if !isAccountCoolingDown("t20-d-a") || !isAccountCoolingDown("t20-d-b") {
		t.Fatal("exhausted accounts must ladder-cool after give-up")
	}
}

// config-effective: explicit values apply clamped; missing key preserves;
// garbage resets to the default 3.
func TestT20_RetryOn4xx_ConfigEffective(t *testing.T) {
	old := loadedRetryOn4xx()
	defer setRetryOn4xx(old)
	setRetryOn4xx(retryOn4xxDefault)

	configure(t20ConfigEnvelope("retry_on_4xx: 0\n"))
	if got := loadedRetryOn4xx(); got != 0 {
		t.Fatalf("explicit 0 must apply, got %d", got)
	}
	configure(t20ConfigEnvelope("offpeak: false\n"))
	if got := loadedRetryOn4xx(); got != 0 {
		t.Fatalf("missing key must preserve current budget 0, got %d", got)
	}
	configure(t20ConfigEnvelope("retry_on_4xx: 99\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxMax {
		t.Fatalf("99 must clamp to %d, got %d", retryOn4xxMax, got)
	}
	configure(t20ConfigEnvelope("retry_on_4xx: abc\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxDefault {
		t.Fatalf("garbage must reset to default %d, got %d", retryOn4xxDefault, got)
	}
	configure(t20ConfigEnvelope("retry_on_4xx: 2 # comment\n"))
	if got := loadedRetryOn4xx(); got != 2 {
		t.Fatalf("inline comment must parse, got %d", got)
	}
	if retryOn4xxDefault != 3 {
		t.Fatalf("zcode default must stay 3 (R1 fixed 3), got %d", retryOn4xxDefault)
	}
}

// Failure case: oversized budget + persistent 401 → still bounded by the
// clamp cap (10 → 11 total attempts), never spinning forever.
func TestT20_RetryOn4xx_OversizedBudget_BoundedByCap(t *testing.T) {
	up, saA, saB := t20Setup(t, "all401", t20CompletionOK, "t20-f-a", "t20-f-b")
	configure(t20ConfigEnvelope("retry_on_4xx: 9999\n"))
	if got := loadedRetryOn4xx(); got != retryOn4xxMax {
		t.Fatalf("9999 must clamp to %d, got %d", retryOn4xxMax, got)
	}
	pickNextAuthFn = func(cur, model string) (string, *storedAuth, bool) {
		if cur == "t20-f-a" {
			return "t20-f-b", saB, true
		}
		if cur == "t20-f-b" {
			return "t20-f-a", saA, true
		}
		return "", nil, false
	}

	_, err := handleExecExecute(t20ExecRaw(t, "t20-f-a", saA))
	if err == nil {
		t.Fatal("all-401 must surface an error")
	}
	if seen, calls := up.snapshot(); calls != retryOn4xxMax+1 {
		t.Fatalf("oversized budget must still bound attempts to %d, got %d (seen=%q)", retryOn4xxMax+1, calls, seen)
	}
}
