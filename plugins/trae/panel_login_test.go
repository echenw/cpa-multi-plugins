package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// In-panel OAuth modal endpoints (panel_login.go). All tests are hermetic:
// no live network — pending/unknown/expired/callback-replay paths touch no
// network by construction, and the networked exchange step is stubbed via
// the panelDrivePoll / panelPersistCredential seams.

func postPanelRoute(t *testing.T, method, subpath, body string) (int, map[string]any) {
	t.Helper()
	base := loadedManagementBasePath() + "/plugins/" + providerName
	req, err := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: base + subpath, Body: []byte(body)})
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
	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, resp.Body)
	}
	return resp.StatusCode, payload
}

func stubPanelDrive(t *testing.T, raw []byte, err error) {
	t.Helper()
	orig := panelDrivePoll
	panelDrivePoll = func(_ string) ([]byte, error) { return raw, err }
	t.Cleanup(func() { panelDrivePoll = orig })
}

func stubPanelPersist(t *testing.T) (saved *map[string][]byte) {
	t.Helper()
	orig := panelPersistCredential
	saved = &map[string][]byte{}
	panelPersistCredential = func(name string, doc []byte) error {
		(*saved)[name] = doc
		return nil
	}
	t.Cleanup(func() { panelPersistCredential = orig })
	return saved
}

func pendingPanelPollResponse(status pluginapi.AuthLoginStatus) []byte {
	raw, _ := okEnvelope(pluginapi.AuthLoginPollResponse{Status: status, Message: "waiting for browser login"})
	return raw
}

func TestPanelLoginPollPending(t *testing.T) {
	state := "panel-poll-pending-001"
	loginStates.Store(state, &loginCtx{variant: variantCN, state: state, loginTraceID: state, expires: time.Now().Add(time.Minute)})
	defer loginStates.Delete(state)

	status, payload := postPanelRoute(t, http.MethodPost, "/login/poll", `{"state":`+mustJSON(state)+`}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d payload=%v, want 200", status, payload)
	}
	if payload["status"] != "pending" {
		t.Fatalf("payload=%v, want status=pending", payload)
	}
}

func TestPanelLoginPollUnknownStateRestartHint(t *testing.T) {
	status, payload := postPanelRoute(t, http.MethodPost, "/login/poll", `{"state":"panel-no-such-state-xyz"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d payload=%v, want 404", status, payload)
	}
	if payload["status"] != "expired" {
		t.Fatalf("payload=%v, want status=expired", payload)
	}
	errText, _ := payload["error"].(string)
	if !strings.Contains(strings.ToLower(errText), "restart") {
		t.Fatalf("error=%q, want a restart hint", errText)
	}
	for k := range payload {
		if k == "StorageJSON" || k == "storage" {
			t.Fatalf("payload=%v must not carry credential material", payload)
		}
	}
}

func TestPanelLoginPollExpiredDeletesState(t *testing.T) {
	state := "panel-poll-expired-001"
	loginStates.Store(state, &loginCtx{variant: variantCN, state: state, loginTraceID: state, expires: time.Now().Add(-time.Minute)})
	defer loginStates.Delete(state)

	status, payload := postPanelRoute(t, http.MethodPost, "/login/poll", `{"state":`+mustJSON(state)+`}`)
	if payload["status"] != "expired" {
		t.Fatalf("status=%d payload=%v, want status=expired", status, payload)
	}
	if _, ok := loginStates.Load(state); ok {
		t.Fatal("expired state must be deleted")
	}
}

func TestPanelLoginPollSuccessPersists(t *testing.T) {
	state := "panel-poll-success-001"
	loginStates.Store(state, &loginCtx{variant: variantCN, state: state, loginTraceID: state, expires: time.Now().Add(time.Minute)})

	storage, _ := json.Marshal(map[string]any{
		"type":    providerName,
		"auth":    map[string]any{"accessToken": "tok-panel-1"},
		"account": map[string]any{"uid": "panel-u1"},
	})
	successRaw, _ := okEnvelope(pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusSuccess,
		Message: "login complete (uid=panel-u1)",
		Auth: pluginapi.AuthData{
			Provider:    providerName,
			ID:          "trae-panel-u1.json",
			FileName:    "trae-panel-u1.json",
			StorageJSON: storage,
		},
	})
	stubPanelDrive(t, successRaw, nil)
	saved := stubPanelPersist(t)

	status, payload := postPanelRoute(t, http.MethodPost, "/login/poll", `{"state":`+mustJSON(state)+`}`)
	if status != http.StatusOK || payload["status"] != "success" {
		t.Fatalf("status=%d payload=%v, want 200 success", status, payload)
	}
	doc, ok := (*saved)["trae-panel-u1.json"]
	if !ok {
		t.Fatalf("persist helper not called with the credential file name, saved=%v payload=%v", *saved, payload)
	}
	if !strings.Contains(string(doc), "panel-u1") {
		t.Fatalf("persisted doc missing the credential identity: %s", doc)
	}
	if _, live := loginStates.Load(state); live {
		t.Fatal("completed state must be deleted")
	}
}

func TestPanelLoginCallbackAccepted(t *testing.T) {
	state := "panel-callback-accepted-001"
	lc := &loginCtx{variant: variantCN, state: state, loginTraceID: state, expires: time.Now().Add(time.Minute)}
	loginStates.Store(state, lc)
	defer loginStates.Delete(state)
	// The replayed callback captures the auth code through the real shared
	// path; the follow-up poll is stubbed pending so no network is touched
	// (the grace self-complete finds the state already deleted and exits).
	stubPanelDrive(t, pendingPanelPollResponse(pluginapi.AuthLoginStatusPending), nil)

	status, payload := postPanelRoute(t, http.MethodPost, "/login/callback",
		`{"state":`+mustJSON(state)+`,"callback_url":`+mustJSON(realFormRedirectURL(state))+`}`)
	if status != http.StatusOK {
		t.Fatalf("status=%d payload=%v, want 200", status, payload)
	}
	if payload["status"] != "pending" {
		t.Fatalf("payload=%v, want the immediate poll status=pending", payload)
	}
	if payload["state"] != state {
		t.Fatalf("payload=%v, want echoed state", payload)
	}
	if lc.authCode != "REAL-FORM-CODE-1" {
		t.Fatalf("callback URL not accepted through the shared path: %+v", lc)
	}
}

func TestPanelLoginCallbackInvalid(t *testing.T) {
	if status, payload := postPanelRoute(t, http.MethodPost, "/login/callback", `{}`); status != http.StatusBadRequest || payload["status"] != "error" {
		t.Fatalf("empty body: status=%d payload=%v, want 400 error", status, payload)
	}
	truncated := "http://127.0.0.1:41961/authorize?isRedirect=true&authCodeInfo={…truncated…}&loginTraceID=x"
	if status, payload := postPanelRoute(t, http.MethodPost, "/login/callback", `{"state":"x","callback_url":`+mustJSON(truncated)+`}`); status != http.StatusBadRequest || payload["status"] != "error" {
		t.Fatalf("truncated url: status=%d payload=%v, want 400 error", status, payload)
	}
	if status, payload := postPanelRoute(t, http.MethodPost, "/login/callback", `{"state":"x","callback_url":"http://127.0.0.1:1/plain"}`); status != http.StatusBadRequest {
		t.Fatalf("url without query: status=%d payload=%v, want 400", status, payload)
	}
}

func TestPanelLoginCallbackUnknownStateRestartHint(t *testing.T) {
	status, payload := postPanelRoute(t, http.MethodPost, "/login/callback",
		`{"callback_url":`+mustJSON(realFormRedirectURL("panel-callback-ghost-001"))+`}`)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d payload=%v, want 404", status, payload)
	}
	if payload["status"] != "expired" {
		t.Fatalf("payload=%v, want status=expired", payload)
	}
	errText, _ := payload["error"].(string)
	if !strings.Contains(strings.ToLower(errText), "restart") {
		t.Fatalf("error=%q, want a restart hint", errText)
	}
}

func TestPanelLoginStartInvalidRealmStoresNothing(t *testing.T) {
	called, _ := stubLoginStart(t)
	status, payload := postPanelLoginStart(t, `{"region":"mars"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d payload=%v, want 400", status, payload)
	}
	if *called {
		t.Fatal("invalid region must not start a login")
	}
	if _, ok := payload["state"]; ok {
		t.Fatalf("payload=%v must not carry a state on rejection", payload)
	}
}
