package main

// In-panel OAuth modal endpoints (POST <base>/login/start|poll|callback):
// hermetic coverage with a controllable upstream login server (no live
// network). The token endpoint flips between pending / success / http500 /
// nouid-account so every poll/callback status is driven deterministically.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type loginModalUpstream struct {
	mu        sync.Mutex
	tokenMode string // "pending" | "success" | "http500"
	emptyAcct bool
}

func (m *loginModalUpstream) setTokenMode(mode string) {
	m.mu.Lock()
	m.tokenMode = mode
	m.mu.Unlock()
}

func (m *loginModalUpstream) getTokenMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokenMode
}

func (m *loginModalUpstream) isEmptyAcct() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.emptyAcct
}

// withLoginModalServer redirects the three login endpoints to a hermetic
// httptest server whose token endpoint answers per tokenMode.
func withLoginModalServer(t *testing.T) *loginModalUpstream {
	t.Helper()
	up := &loginModalUpstream{tokenMode: "pending"}
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/plugin/auth/state", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"state":"st-modal","authUrl":"https://auth.example/login?x=1"}}`))
	})
	mux.HandleFunc("/v2/plugin/auth/token", func(w http.ResponseWriter, r *http.Request) {
		switch up.getTokenMode() {
		case "success":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"accessToken":"at-modal","refreshToken":"rt-modal","expiresIn":3600,"domain":"copilot.tencent.com"}}`))
		case "http500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
		default:
			_, _ = w.Write([]byte(`{"code":1,"msg":"login ing","data":{}}`))
		}
	})
	mux.HandleFunc("/v2/plugin/login/account", func(w http.ResponseWriter, r *http.Request) {
		if up.isEmptyAcct() {
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"uid":"u-modal","nickname":"modal-user"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := loginEndpointsOverride
	loginEndpointsOverride = srv.URL
	t.Cleanup(func() { loginEndpointsOverride = old })
	oldDelays := loginAcctRetryDelays
	loginAcctRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { loginAcctRetryDelays = oldDelays })
	return up
}

// callLoginModalEndpoint dispatches one management request against the
// in-panel login surface and decodes the {status/error/payload} envelope.
func callLoginModalEndpoint(t *testing.T, suffix, body string) (int, map[string]any) {
	t.Helper()
	resetMgmtRateLimit(t)
	base := loadedManagementBasePath() + "/plugins/" + providerName
	req, err := json.Marshal(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   base + suffix,
		Body:   []byte(body),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := handleManagement(req)
	if err != nil {
		t.Fatalf("handleManagement: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope err=%v ok=%v raw=%s", err, env.OK, out)
	}
	var resp pluginapi.ManagementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, resp.Body)
	}
	return resp.StatusCode, payload
}

// modalStart begins one hermetic login and returns its state (cleanup drains
// the session so no login state leaks between tests).
func modalStart(t *testing.T, body string) (string, map[string]any) {
	t.Helper()
	before := snapshotLoginStates()
	t.Cleanup(func() { drainNewLoginStates(before) })
	status, payload := callLoginModalEndpoint(t, "/login/start", body)
	if status != http.StatusOK {
		t.Fatalf("login/start status = %d, payload=%v", status, payload)
	}
	state, _ := payload["state"].(string)
	if state == "" {
		t.Fatalf("login/start returned no state in %v", payload)
	}
	return state, payload
}

func TestModalLoginStartReturnsStateAndExpiry(t *testing.T) {
	_ = withLoginModalServer(t)

	for _, body := range []string{`{}`, `{"region":"cn"}`, `{"region":"global"}`} {
		state, payload := modalStart(t, body)
		if state != "st-modal" {
			t.Fatalf("body %q: state = %q, want st-modal", body, state)
		}
		if url, _ := payload["auth_url"].(string); url == "" {
			t.Fatalf("body %q: empty auth_url in %v", body, payload)
		}
		exp, _ := payload["expires_at"].(string)
		if exp == "" {
			t.Fatalf("body %q: empty expires_at in %v", body, payload)
		}
		if _, err := time.Parse(time.RFC3339, exp); err != nil {
			t.Fatalf("body %q: expires_at %q is not RFC3339: %v", body, exp, err)
		}
	}
}

func TestModalLoginPollPending(t *testing.T) {
	_ = withLoginModalServer(t)

	state, _ := modalStart(t, `{}`)
	status, payload := callLoginModalEndpoint(t, "/login/poll", `{"state":"`+state+`"}`)
	if status != http.StatusOK {
		t.Fatalf("poll status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "pending" {
		t.Fatalf("poll = %v, want status pending", payload)
	}
}

func TestModalLoginPollUnknownState(t *testing.T) {
	_ = withLoginModalServer(t)

	status, payload := callLoginModalEndpoint(t, "/login/poll", `{"state":"no-such-state"}`)
	if status != http.StatusOK {
		t.Fatalf("poll status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "expired" {
		t.Fatalf("poll unknown state = %v, want status expired", payload)
	}
	msg, _ := payload["error"].(string)
	if !strings.Contains(msg, "restart") {
		t.Fatalf("poll unknown state error %q must carry a restart hint", msg)
	}
}

func TestModalLoginPollEmptyState(t *testing.T) {
	_ = withLoginModalServer(t)

	for _, body := range []string{`{}`, ``} {
		status, payload := callLoginModalEndpoint(t, "/login/poll", body)
		if status != http.StatusBadRequest {
			t.Fatalf("body %q: poll status = %d, want 400 (payload=%v)", body, status, payload)
		}
		if _, ok := payload["error"]; !ok {
			t.Fatalf("body %q: no error in %v", body, payload)
		}
	}
}

func TestModalLoginPollSuccessPersists(t *testing.T) {
	up := withLoginModalServer(t)
	up.setTokenMode("success")
	saves := withRecordedAuthPersist(t)

	state, _ := modalStart(t, `{}`)
	status, payload := callLoginModalEndpoint(t, "/login/poll", `{"state":"`+state+`"}`)
	if status != http.StatusOK {
		t.Fatalf("poll status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "success" {
		t.Fatalf("poll = %v, want status success", payload)
	}
	if payload["uid"] != "u-modal" {
		t.Fatalf("poll success uid = %v, want u-modal", payload["uid"])
	}
	if len(*saves) != 1 {
		t.Fatalf("plugin-side persist saves = %d, want 1", len(*saves))
	}
	saved, err := parseStored((*saves)[0].raw)
	if err != nil {
		t.Fatalf("parse persisted credential: %v", err)
	}
	if saved.Account.UID != "u-modal" {
		t.Fatalf("persisted uid = %q, want u-modal", saved.Account.UID)
	}
	if _, ok := loginStates.Load(state); ok {
		t.Fatalf("login state %q not deleted after success", state)
	}
}

func TestModalLoginPollUpstreamError(t *testing.T) {
	up := withLoginModalServer(t)
	up.setTokenMode("http500")

	state, _ := modalStart(t, `{}`)
	status, payload := callLoginModalEndpoint(t, "/login/poll", `{"state":"`+state+`"}`)
	if status != http.StatusOK {
		t.Fatalf("poll status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "error" {
		t.Fatalf("poll http500 = %v, want status error", payload)
	}
	if _, ok := payload["error"]; !ok {
		t.Fatalf("poll http500 has no error in %v", payload)
	}
}

func TestModalLoginPollExpiredState(t *testing.T) {
	_ = withLoginModalServer(t)

	state, _ := modalStart(t, `{}`)
	if v, ok := loginStates.Load(state); ok {
		lc := v.(*loginCtx)
		lc.expires = time.Now().Add(-time.Second)
	}
	status, payload := callLoginModalEndpoint(t, "/login/poll", `{"state":"`+state+`"}`)
	if status != http.StatusOK {
		t.Fatalf("poll status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "expired" {
		t.Fatalf("poll expired = %v, want status expired", payload)
	}
	if msg, _ := payload["error"].(string); !strings.Contains(msg, "restart") && !strings.Contains(msg, "expired") {
		t.Fatalf("poll expired error %q must hint restart/expiry", msg)
	}
}

func TestModalLoginCallbackPendingThenSuccess(t *testing.T) {
	up := withLoginModalServer(t)
	saves := withRecordedAuthPersist(t)

	state, _ := modalStart(t, `{}`)
	cb := `{"state":"` + state + `","callback_url":"https://auth.example/done?state=` + state + `"}`
	status, payload := callLoginModalEndpoint(t, "/login/callback", cb)
	if status != http.StatusOK {
		t.Fatalf("callback status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "pending" {
		t.Fatalf("callback pending = %v, want status pending", payload)
	}

	up.setTokenMode("success")
	status, payload = callLoginModalEndpoint(t, "/login/callback", cb)
	if status != http.StatusOK {
		t.Fatalf("callback status = %d, payload=%v", status, payload)
	}
	if payload["status"] != "success" {
		t.Fatalf("callback success = %v, want status success", payload)
	}
	if len(*saves) != 1 {
		t.Fatalf("callback success saves = %d, want 1", len(*saves))
	}
}

func TestModalLoginCallbackInvalid(t *testing.T) {
	_ = withLoginModalServer(t)

	state, _ := modalStart(t, `{}`)
	beforeSet := snapshotLoginStates()
	cases := []struct {
		name string
		body string
	}{
		{"unknown state", `{"state":"no-such-state","callback_url":"https://auth.example/done"}`},
		{"empty state", `{"state":"","callback_url":"https://auth.example/done"}`},
		{"empty callback", `{"state":"` + state + `","callback_url":""}`},
		{"missing callback", `{"state":"` + state + `"}`},
		{"state mismatch", `{"state":"` + state + `","callback_url":"https://auth.example/done?state=other-login"}`},
		{"invalid region", `{"state":"` + state + `","callback_url":"https://auth.example/done","region":"eu"}`},
	}
	for _, tc := range cases {
		status, payload := callLoginModalEndpoint(t, "/login/callback", tc.body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: callback status = %d, want 400 (payload=%v)", tc.name, status, payload)
		}
		if _, ok := payload["error"]; !ok {
			t.Fatalf("%s: no error in %v", tc.name, payload)
		}
	}
	// Invalid regions (and all callback rejections) must not create sessions:
	// only the one valid session started above may exist.
	drainNewLoginStates(beforeSet)
	if got := countLoginStates(); got != len(beforeSet) {
		t.Fatalf("callback rejections leaked %d login state(s)", got-len(beforeSet))
	}
	// The valid session must still be alive (rejections delete nothing).
	if _, ok := loginStates.Load(state); !ok {
		t.Fatalf("valid login state %q deleted by rejected callbacks", state)
	}
}

func TestModalLoginCallbackExpiredState(t *testing.T) {
	_ = withLoginModalServer(t)

	state, _ := modalStart(t, `{}`)
	if v, ok := loginStates.Load(state); ok {
		v.(*loginCtx).expires = time.Now().Add(-time.Second)
	}
	status, payload := callLoginModalEndpoint(t, "/login/callback", `{"state":"`+state+`","callback_url":"https://auth.example/done"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("callback expired status = %d, want 400 (payload=%v)", status, payload)
	}
	msg, _ := payload["error"].(string)
	if !strings.Contains(msg, "restart") && !strings.Contains(msg, "expired") {
		t.Fatalf("callback expired error %q must hint restart/expiry", msg)
	}
}
