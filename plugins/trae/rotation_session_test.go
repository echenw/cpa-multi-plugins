package main

// T19 acceptance tests: pooled same-request rotation + session affinity for
// plugins/trae.
//
// handleExecExecute is driven hermetically: upstreamClient points at an
// httptest server (AgentHost override — ChatStream honors the client's
// hosts) that answers per Cloud-IDE-JWT token, and accountPool is swapped
// for an in-memory pool. Credentials carry far-future expiry so no refresh
// network ever fires, and fresh pools are never quota-frozen so no usage
// probe fires either.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmqz/cpa-multi-plugins/plugins/trae/auth"
	"github.com/mmqz/cpa-multi-plugins/plugins/trae/pool"
	"github.com/mmqz/cpa-multi-plugins/plugins/trae/upstream"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// t19SuccessSSE is a minimal SOLO SSE body Aggregate folds into a
// chat.completion.
const t19SuccessSSE = "event: output\ndata: {\"response\":\"hello\"}\n\nevent: done\ndata: {\"finish_reason\":\"stop\"}\n\n"

// t19Upstream is a mock llm_utils_chat lane. mode "rotate" 401s token-a and
// serves token-b; mode "all401" 401s everything.
type t19Upstream struct {
	mu    sync.Mutex
	mode  string
	seen  []string
	calls int
}

func (u *t19Upstream) handler(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Cloud-IDE-JWT "))
	u.mu.Lock()
	u.seen = append(u.seen, token)
	u.calls++
	u.mu.Unlock()
	if u.mode == "all401" || token != "token-b" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":401,"msg":"Cloud-IDE-JWT expired"}`))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(t19SuccessSSE))
}

func (u *t19Upstream) snapshot() (seen []string, calls int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.seen...), u.calls
}

// t19Cred builds one pool credential with far-future expiry (no refresh).
func t19Cred(uid, token string, credits int64, p *pool.Pool) *auth.Auth {
	a := &auth.Auth{
		AccessToken:  token,
		RefreshToken: "refresh-" + token,
		ExpiresAt:    time.Now().Add(48 * time.Hour).Unix(),
		Variant:      "cn",
		UID:          uid,
		Nickname:     "nick-" + uid,
	}
	p.Add(a)
	p.SetCredits(uid, credits)
	return a
}

// t19Storage renders the flat StorageJSON handleExecExecute parses.
func t19Storage(t *testing.T, uid, token string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"accessToken":  token,
		"refreshToken": "refresh-" + token,
		"expiresAt":    time.Now().Add(48 * time.Hour).Unix(),
		"variant":      "cn",
		"uid":          uid,
	})
	if err != nil {
		t.Fatalf("marshal storage: %v", err)
	}
	return raw
}

// t19ExecRaw builds one ExecutorRequest for uid/token.
func t19ExecRaw(t *testing.T, uid, token string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"AuthID":      "host-" + uid,
		"Model":       "m",
		"Payload":     []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`),
		"StorageJSON": t19Storage(t, uid, token),
	})
	if err != nil {
		t.Fatalf("marshal exec request: %v", err)
	}
	return raw
}

// t19Setup swaps the executor globals for hermetic rotation tests: mock
// upstream lane, fresh in-memory pool, default budget. Restores everything.
func t19Setup(t *testing.T, mode string) (*t19Upstream, *pool.Pool) {
	t.Helper()
	up := &t19Upstream{mode: mode}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	oldClient := upstreamClient
	upstreamClient = &upstream.Client{
		HTTP:       srv.Client(),
		StreamHTTP: srv.Client(),
		AgentHost:  srv.URL,
		UgHost:     srv.URL,
		OAuthHost:  srv.URL,
		ClientID:   "test",
	}
	t.Cleanup(func() { upstreamClient = oldClient })
	oldPool := accountPool
	p := pool.New("")
	accountPool = p
	t.Cleanup(func() { accountPool = oldPool })
	oldBudget := loadedRetryOn4xx()
	setRetryOn4xx(retryOn4xxDefault)
	t.Cleanup(func() { setRetryOn4xx(oldBudget) })
	clearSessionBindings()
	t.Cleanup(clearSessionBindings)
	return up, p
}

// TestTraePooledRotationOn401 locks the T19 happy path: the routed
// credential 401s, the same request rotates inside the pool via
// PickExcluding, the failed credential is parked via Pool.Cooldown, and the
// request succeeds on the second credential.
func TestTraePooledRotationOn401(t *testing.T) {
	up, p := t19Setup(t, "rotate")
	t19Cred("uid-a", "token-a", 100, p)
	t19Cred("uid-b", "token-b", 50, p)

	out, err := handleExecExecute(t19ExecRaw(t, "uid-a", "token-a"))
	if err != nil {
		t.Fatalf("rotated execute: %v", err)
	}
	var env struct {
		Result pluginapi.ExecutorResponse `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !strings.Contains(string(env.Result.Payload), "hello") {
		t.Errorf("want rotated completion carrying upstream answer, got %q", env.Result.Payload)
	}
	seen, _ := up.snapshot()
	if len(seen) != 2 || seen[0] != "token-a" || seen[1] != "token-b" {
		t.Errorf("want same-request rotation token-a → token-b, upstream saw %q", seen)
	}
	st, ok := p.Status("uid-a")
	if !ok {
		t.Fatal("uid-a missing from pool")
	}
	if !st.Cooling {
		t.Error("rotated-out uid-a must be parked via Pool.Cooldown (plan pool.go:234)")
	}
	if st.Disabled {
		t.Error("rotation-path accounting must Cooldown, not Disable (Disable stays on the final-failure path)")
	}
}

// TestTraeNoAlternativeDefersSemantics locks the T19 defer preservation:
// session mode with no usable trae account returns Handled:false (the
// pre-T19 default-path semantics), never a pinned dead pick.
func TestTraeNoAlternativeDefersSemantics(t *testing.T) {
	_, p := t19Setup(t, "rotate")
	defer setSchedulerMode(schedulerModeSession)()
	t19Cred("uid-a", "token-a", 100, p)
	t19Cred("uid-b", "token-b", 50, p)
	// Park every pool credential so none is usable.
	p.Cooldown("uid-a", pool.CoolSoft, time.Hour, "t19")
	p.Cooldown("uid-b", pool.CoolSoft, time.Hour, "t19")

	raw, _ := json.Marshal(map[string]any{
		"plugin":   map[string]any{"id": "trae"},
		"provider": "trae",
		"model":    "m",
		"options": map[string]any{
			"headers":  map[string][]string{"Session-Id": {"s-1"}},
			"metadata": map[string]any{},
		},
		"candidates": []map[string]any{
			{"id": "uid-a", "provider": "trae"},
			{"id": "uid-b", "provider": "trae"},
		},
	})
	out, err := handleSchedulerPick(raw)
	if err != nil {
		t.Fatalf("handleSchedulerPick: %v", err)
	}
	var env struct {
		Result pluginapi.SchedulerPickResponse `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if env.Result.Handled {
		t.Errorf("no usable alternative must keep defer semantics (Handled=false), got AuthID=%q", env.Result.AuthID)
	}
	if env.Result.AuthID != "" {
		t.Errorf("no auth should be picked when everything is parked, got %q", env.Result.AuthID)
	}
}

// TestTraeSessionAffinityHit locks session wiring in scheduler_pick: the
// same session key sticks to one account across picks while a different key
// spreads to the other account.
func TestTraeSessionAffinityHit(t *testing.T) {
	_, p := t19Setup(t, "rotate")
	defer setSchedulerMode(schedulerModeSession)()
	t19Cred("uid-a", "token-a", 100, p)
	t19Cred("uid-b", "token-b", 50, p)

	pick := func(session string) pluginapi.SchedulerPickResponse {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"plugin":   map[string]any{"id": "trae"},
			"provider": "trae",
			"model":    "m",
			"options": map[string]any{
				"headers":  map[string][]string{"Session-Id": {session}},
				"metadata": map[string]any{},
			},
			"candidates": []map[string]any{
				{"id": "uid-a", "provider": "trae"},
				{"id": "uid-b", "provider": "trae"},
			},
		})
		out, err := handleSchedulerPick(raw)
		if err != nil {
			t.Fatalf("handleSchedulerPick: %v", err)
		}
		var env struct {
			Result pluginapi.SchedulerPickResponse `json:"result"`
		}
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("envelope: %v", err)
		}
		return env.Result
	}

	first := pick("conv-1")
	if !first.Handled || first.AuthID == "" {
		t.Fatalf("session pick must handle, got %+v", first)
	}
	second := pick("conv-1")
	if second.AuthID != first.AuthID {
		t.Errorf("same session must stick: first=%q second=%q", first.AuthID, second.AuthID)
	}
	other := pick("conv-2")
	if !other.Handled || other.AuthID == "" {
		t.Fatalf("second conversation must pick, got %+v", other)
	}
	if other.AuthID == first.AuthID {
		t.Errorf("different conversation should spread, both picked %q", first.AuthID)
	}
}

// TestTraeEmptyPool401FailsClean is the T19 failure case: nothing usable in
// the pool + 401 → the original error surfaces promptly (clean failure, no
// hang, no rotation loop).
func TestTraeEmptyPool401FailsClean(t *testing.T) {
	up, _ := t19Setup(t, "all401")
	// Pool stays empty: the routed credential is the only account and the
	// pool offers no alternative.

	done := make(chan error, 1)
	go func() {
		_, err := handleExecExecute(t19ExecRaw(t, "uid-solo", "token-solo"))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("empty pool + 401 must fail, got success")
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("want the original 401 surfaced, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("empty pool + 401 hung — rotation must terminate")
	}
	if _, calls := up.snapshot(); calls != 1 {
		t.Errorf("want exactly 1 upstream call (no rotation loop), got %d", calls)
	}
}
