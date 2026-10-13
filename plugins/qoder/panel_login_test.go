package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func callPanelLoginRoute(t *testing.T, route, body string) (int, map[string]any) {
	t.Helper()
	base := loadedManagementBasePath() + "/plugins/" + providerName
	req, err := json.Marshal(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   base + route,
		Body:   []byte(body),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := handleManagement(req)
	if err != nil {
		t.Fatalf("handleManagement %s: %v", route, err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope err=%v ok=%v raw=%s", err, env.OK, out)
	}
	var resp pluginapi.ManagementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, resp.Body)
	}
	return resp.StatusCode, payload
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubLoginTransport(t *testing.T, fn roundTripFunc) {
	t.Helper()
	_ = sharedHTTPClient()
	old := sharedClient
	sharedClient = &http.Client{Transport: fn}
	t.Cleanup(func() { sharedClient = old })
}

func stubLoginSave(t *testing.T, calls *[]string) {
	t.Helper()
	orig := hostAuthSaveJSONFn
	*calls = nil
	hostAuthSaveJSONFn = func(name string, raw []byte) error {
		*calls = append(*calls, name+"="+string(raw))
		return nil
	}
	t.Cleanup(func() { hostAuthSaveJSONFn = orig })
}

func loginJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func startPanelLoginForTest(t *testing.T, body string) (string, string) {
	t.Helper()
	before := snapshotLoginStates(t)
	t.Cleanup(func() { deleteNewLoginStates(before) })
	status, payload := callPanelLoginRoute(t, "/login/start", body)
	if status != http.StatusOK {
		t.Fatalf("login/start status=%d payload=%v", status, payload)
	}
	state, _ := payload["state"].(string)
	exp, _ := payload["expires_at"].(string)
	if state == "" || exp == "" {
		t.Fatalf("login/start must return state+expires_at, got %v", payload)
	}
	if _, err := time.Parse(time.RFC3339, exp); err != nil {
		t.Fatalf("expires_at %q not RFC3339: %v", exp, err)
	}
	return state, exp
}

func TestPanelLoginStartReturnsState(t *testing.T) {
	state, _ := startPanelLoginForTest(t, `{}`)
	v, ok := loginStates.Load(state)
	if !ok {
		t.Fatal("login state not stored")
	}
	lc := v.(*loginCtx)
	if lc.region != regionIntl {
		t.Fatalf("region=%q want intl", lc.region)
	}
}

func TestPanelLoginPollPending(t *testing.T) {
	state, _ := startPanelLoginForTest(t, `{"region":"intl"}`)
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/deviceToken/poll") {
			return loginJSON(http.StatusNotFound, `{"error":"not authorized yet"}`), nil
		}
		return loginJSON(http.StatusNotFound, `{}`), nil
	})
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/login/poll", `{"state":"`+state+`"}`)
	if status != http.StatusOK || payload["status"] != "pending" {
		t.Fatalf("poll pending: status=%d payload=%v", status, payload)
	}
	if len(saves) != 0 {
		t.Fatalf("pending poll must not persist, saves=%d", len(saves))
	}
	if _, ok := loginStates.Load(state); !ok {
		t.Fatal("pending poll must keep state")
	}
}

func TestPanelLoginPollUnknownState(t *testing.T) {
	status, payload := callPanelLoginRoute(t, "/login/poll", `{"state":"qw-no-such-state"}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown state status=%d want 404 payload=%v", status, payload)
	}
	if !strings.Contains(strings.ToLower(payload["error"].(string)), "restart login") {
		t.Fatalf("unknown state error must hint restart, got %v", payload)
	}
}

func TestPanelLoginPollSuccessPersists(t *testing.T) {
	state, _ := startPanelLoginForTest(t, `{"region":"cn"}`)
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/deviceToken/poll") {
			return loginJSON(http.StatusOK, `{"token":"dt-test-123","refresh_token":"drt-test-456","user_id":"uid-persist-1","expires_in":2592000000}`), nil
		}
		return loginJSON(http.StatusNotFound, `{}`), nil
	})
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/login/poll", `{"state":"`+state+`"}`)
	if status != http.StatusOK || payload["status"] != "success" {
		t.Fatalf("poll success: status=%d payload=%v", status, payload)
	}
	if len(saves) != 1 {
		t.Fatalf("success must persist exactly once, saves=%d", len(saves))
	}
	if !strings.Contains(saves[0], "dt-test-123") {
		t.Fatalf("persisted doc must carry device token, got %.200s", saves[0])
	}
	if _, ok := loginStates.Load(state); ok {
		t.Fatal("success must delete state")
	}
}

func TestPanelLoginCallbackPATLegacy(t *testing.T) {
	state, _ := startPanelLoginForTest(t, `{"region":"intl"}`)
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/jobToken/exchange"):
			return loginJSON(http.StatusOK, `{"token":"jt-leg-1","refresh_token":"jrt-leg-1","expires_in":86400000,"refresh_token_expires_in":172800000}`), nil
		case strings.Contains(r.URL.Path, "/userinfo"):
			return loginJSON(http.StatusOK, `{"id":"uid-pat-9","name":"pat-user"}`), nil
		default:
			return loginJSON(http.StatusNotFound, `{}`), nil
		}
	})
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/login/callback", `{"state":"`+state+`","callback_url":"pt-test-legacy-token"}`)
	if status != http.StatusOK || payload["status"] != "success" {
		t.Fatalf("callback PAT: status=%d payload=%v", status, payload)
	}
	if len(saves) != 1 || !strings.Contains(saves[0], "jt-leg-1") {
		t.Fatalf("PAT legacy must persist jobToken, saves=%.300v", saves)
	}
	if _, ok := loginStates.Load(state); ok {
		t.Fatal("callback success must delete state")
	}
}

func TestPanelLoginCallbackInvalid(t *testing.T) {
	status, payload := callPanelLoginRoute(t, "/login/callback", `{"state":"qw-no-such","callback_url":"https://qoder.com/x"}`)
	if status != http.StatusNotFound {
		t.Fatalf("unknown state status=%d want 404 payload=%v", status, payload)
	}
	if !strings.Contains(strings.ToLower(payload["error"].(string)), "restart login") {
		t.Fatalf("callback unknown error must hint restart, got %v", payload)
	}
	state, _ := startPanelLoginForTest(t, `{}`)
	status, payload = callPanelLoginRoute(t, "/login/callback", `{"state":"`+state+`","callback_url":""}`)
	if status != http.StatusBadRequest {
		t.Fatalf("empty callback status=%d want 400 payload=%v", status, payload)
	}
}

func TestPanelLoginInvalidRegionNothingStored(t *testing.T) {
	nBefore := countLoginStates()
	status, payload := callPanelLoginRoute(t, "/login/start", `{"region":"eu"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid region start status=%d want 400 payload=%v", status, payload)
	}
	if got := countLoginStates(); got != nBefore {
		deleteNewLoginStates(nil)
		t.Fatalf("invalid region must store nothing (%d->%d)", nBefore, got)
	}
	_ = payload
	loginStates.Store("qw-invalid-region-test", &loginCtx{verifier: "v", nonce: "n", region: "eu", expires: time.Now().Add(loginTTL)})
	t.Cleanup(func() { loginStates.Delete("qw-invalid-region-test") })
	var saves []string
	stubLoginSave(t, &saves)
	status, _ = callPanelLoginRoute(t, "/login/poll", `{"state":"qw-invalid-region-test"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid stored region poll status=%d want 400", status)
	}
	if len(saves) != 0 {
		t.Fatal("invalid region must store nothing on poll")
	}
	status, _ = callPanelLoginRoute(t, "/login/callback", `{"state":"qw-invalid-region-test","callback_url":"https://qoder.com/x"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid stored region callback status=%d want 400", status)
	}
	if len(saves) != 0 {
		t.Fatal("invalid region must store nothing on callback")
	}
}
