// panel_login.go — in-panel OAuth modal API (v0.12.84).
//
// The management panel's 授权登录 button used to open the authorization URL
// in a new tab and poll /accounts for a new credential file. Users rejected
// the new-tab-only flow ("我要的是弹窗，有链接和回调地址，不要给我只就跳转").
// These endpoints back the in-panel modal in panel.html instead:
//
//	POST <base>/login/start    — unchanged route, extended payload:
//	                             {auth_url, realm, state, expires_at}.
//	POST <base>/login/poll     — {state} → {status, ...} with status one of
//	                             pending|success|expired|error.
//	POST <base>/login/callback — {state, callback_url} → replays the pasted
//	                             browser address-bar URL through the exact same
//	                             resolution path as a real callback hit, then
//	                             performs one immediate poll attempt.
//
// Completion mechanics are NOT re-implemented here: poll drives one attempt
// through the plugin's existing host-RPC handlers (handlePollLogin /
// intlhandlePollLogin, selected the same way the auth.login.poll dispatch
// selects — by where the state lives), and success persists via the existing
// hostAuthSave helper. No new host RPCs, no duplicated exchange logic.
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// panelLoginStartResult is the full login-start outcome the modal needs:
// the browser-facing authorization URL plus the poll key and its expiry.
type panelLoginStartResult struct {
	URL       string
	State     string
	ExpiresAt time.Time
}

// panelDrivePoll drives ONE login-completion attempt through the plugin's
// existing host-RPC login mechanics. State-location routing mirrors the
// auth.login.poll dispatch in handleMethod: an Intl state goes to the Intl
// handler, everything else to the CN/SOLO handler (which also serves the
// unknown/expired-state errors). Package var so panel tests can stub the
// networked exchange while exercising the real status translation.
var panelDrivePoll = func(state string) ([]byte, error) {
	pollReq, _ := json.Marshal(pluginapi.AuthLoginPollRequest{State: state})
	if _, ok := intlloginStates.Load(state); ok {
		return intlhandlePollLogin(pollReq)
	}
	return handlePollLogin(pollReq)
}

// panelPersistCredential persists a completed login's credential. Production
// path is the existing hostAuthSave helper (no duplication, no new host
// RPC); package var so tests can capture the write hermetically.
var panelPersistCredential = func(name string, raw []byte) error {
	return hostAuthSave(name, raw)
}

// panelPollState parses a {state|State} body for the panel poll endpoint.
func panelPollState(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var body struct {
		State string `json:"state"`
		Upper string `json:"State"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	if s := strings.TrimSpace(body.State); s != "" {
		return s
	}
	return strings.TrimSpace(body.Upper)
}

// translatePanelPollResult maps one panelDrivePoll outcome onto the modal's
// {status: pending|success|expired|error} shape. Unknown/expired states
// surface the restart hint the underlying mechanics already carry
// ("please restart login" / "please re-initiate"). state is the polled key,
// used only for defensive cleanup (the underlying handlers already delete
// the state on every terminal path; the deletes here are idempotent).
func translatePanelPollResult(raw []byte, driveErr error, state string) (int, any) {
	if driveErr != nil {
		msg := driveErr.Error()
		switch {
		case strings.Contains(msg, "unknown state"):
			return http.StatusNotFound, map[string]any{"status": "expired", "error": msg}
		case strings.Contains(msg, "expired"):
			return http.StatusGone, map[string]any{"status": "expired", "error": msg}
		case strings.Contains(msg, "empty state"):
			return http.StatusBadRequest, map[string]any{"status": "error", "error": msg}
		default:
			return http.StatusInternalServerError, map[string]any{"status": "error", "error": msg}
		}
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		msg := "poll: bad response envelope"
		if env.Error != nil && strings.TrimSpace(env.Error.Message) != "" {
			msg = env.Error.Message
		}
		return http.StatusInternalServerError, map[string]any{"status": "error", "error": msg}
	}
	var pr pluginapi.AuthLoginPollResponse
	if err := json.Unmarshal(env.Result, &pr); err != nil {
		return http.StatusInternalServerError, map[string]any{"status": "error", "error": "poll: decode response: " + err.Error()}
	}
	switch pr.Status {
	case pluginapi.AuthLoginStatusPending:
		return http.StatusOK, map[string]any{"status": "pending", "message": pr.Message}
	case pluginapi.AuthLoginStatusSuccess:
		name := strings.TrimSpace(pr.Auth.FileName)
		if name == "" {
			name = strings.TrimSpace(pr.Auth.ID)
		}
		if name == "" || len(pr.Auth.StorageJSON) == 0 {
			return http.StatusInternalServerError, map[string]any{"status": "error", "error": "login completed but the credential payload is empty — please restart the login"}
		}
		if err := panelPersistCredential(name, pr.Auth.StorageJSON); err != nil {
			return http.StatusInternalServerError, map[string]any{"status": "error", "error": "login completed but saving the credential failed: " + err.Error()}
		}
		// The underlying handler already deleted the state on success;
		// delete defensively (idempotent) so a stubbed drive in tests and
		// any future path leave nothing behind.
		loginStates.Delete(state)
		intlloginStates.Delete(state)
		return http.StatusOK, map[string]any{"status": "success", "message": pr.Message, "file": name}
	default:
		return http.StatusOK, map[string]any{"status": "error", "error": pr.Message}
	}
}

func handlePanelLoginPoll(req pluginapi.ManagementRequest) (int, any) {
	state := panelPollState(req.Body)
	if state == "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "missing state — please restart the login"}
	}
	raw, driveErr := panelDrivePoll(state)
	status, payload := translatePanelPollResult(raw, driveErr, state)
	if status == http.StatusNotFound {
		// The login already finished between modal polls (auto-callback, or
		// a previous poll drained the state): answer from the outcome cache
		// instead of the confusing unknown-state page — same semantics as
		// the oauth_callback resource route (v0.12.17).
		if o, ok := lookupLoginOutcome(state); ok {
			if o.ok {
				return http.StatusOK, map[string]any{"status": "success", "message": "该登录此前已完成，凭证已保存。This login was already completed — the credential is saved."}
			}
			return http.StatusOK, map[string]any{"status": "error", "error": o.msg + " — 请重新开始登录 (please restart the login)"}
		}
	}
	// Defensive cleanup for terminal non-success outcomes: the underlying
	// handlers already delete on every terminal path, this only covers a
	// stubbed drive in tests.
	if m, ok := payload.(map[string]any); ok {
		if s, _ := m["status"].(string); s == "expired" || s == "error" {
			loginStates.Delete(state)
			intlloginStates.Delete(state)
		}
	}
	return status, payload
}

// panelCallbackBody is the POST <base>/login/callback body. The URL key
// accepts the same spellings the oauth_submit fallback accepts (cb_url/url),
// so modal and paste-box payloads stay interchangeable.
type panelCallbackBody struct {
	State       string `json:"state"`
	UpperState  string `json:"State"`
	CallbackURL string `json:"callback_url"`
	CbURL       string `json:"cb_url"`
	URL         string `json:"url"`
}

// panelCallbackInput splits the body into the explicit poll state and the
// pasted browser URL.
func panelCallbackInput(raw []byte) (state, callbackURL string) {
	var body panelCallbackBody
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	state = strings.TrimSpace(body.State)
	if state == "" {
		state = strings.TrimSpace(body.UpperState)
	}
	callbackURL = strings.TrimSpace(body.CallbackURL)
	if callbackURL == "" {
		callbackURL = strings.TrimSpace(body.CbURL)
	}
	if callbackURL == "" {
		callbackURL = strings.TrimSpace(body.URL)
	}
	return state, callbackURL
}

// handlePanelLoginCallback serves POST <base>/login/callback — the modal's
// step-2 submit. It accepts exactly what the existing mechanics accept: the
// full browser address-bar callback URL is replayed through the shared
// resolution path (resolveCallbackState + authCodeInfo extraction — the same
// function the oauth_callback / oauth_submit resource routes use; no new
// flows are invented here). Afterwards exactly one poll attempt runs and its
// status is returned.
func handlePanelLoginCallback(req pluginapi.ManagementRequest) (int, any) {
	explicit, rawURL := panelCallbackInput(req.Body)
	if rawURL == "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "missing callback_url — 请粘贴浏览器地址栏的完整回调链接 (paste the full callback URL from the browser address bar)"}
	}
	clean, truncErr := sanitizePastedCallback(rawURL)
	if truncErr != "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": truncErr}
	}
	u, errParse := url.Parse(clean)
	if errParse != nil || len(u.Query()) == 0 {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "The pasted text is not a parseable URL with query parameters. Copy the full address-bar URL shown after the authorization redirect."}
	}
	q := u.Query()
	// Bind an explicit modal state only when the URL does not already
	// resolve (real Trae redirects echo login_trace_id, which wins — the
	// port-scoped cockpit-tools semantic).
	if explicit != "" && resolveCallbackState(q) == "" {
		q.Set("state", explicit)
	}
	resolved := resolveCallbackState(q)
	if resolved == "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "missing state parameter — 回调链接缺少状态参数，请回到面板重新点「登录」，完成授权后立即粘贴新链接 (please restart the login)"}
	}
	// Replay through the shared callback path (no new flows).
	_ = handleOAuthCallbackResource(pluginapi.ManagementRequest{Query: q})
	// One immediate poll attempt, then report its status.
	raw, driveErr := panelDrivePoll(resolved)
	status, payload := translatePanelPollResult(raw, driveErr, resolved)
	if status == http.StatusNotFound {
		if o, ok := lookupLoginOutcome(resolved); ok {
			if o.ok {
				return http.StatusOK, map[string]any{"status": "success", "message": "该回调链接的登录已完成，凭证已保存。This login was already completed — the credential is saved.", "state": resolved}
			}
			return http.StatusOK, map[string]any{"status": "error", "error": o.msg + " — 请重新开始登录并粘贴新链接。", "state": resolved}
		}
	}
	if m, ok := payload.(map[string]any); ok {
		m["state"] = resolved
	}
	return status, payload
}
