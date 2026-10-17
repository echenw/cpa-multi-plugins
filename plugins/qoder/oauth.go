// oauth.go implements region-pinned PKCE device authorization and PAT import.
// Login parameters follow cpa-workbuddy-plugin/qoderwork (CN) and qoder-ai
// (Intl); device grants use dt-/drt-, while PAT grants use jt-/jrt-.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// jobTokenResponse mirrors the upstream /api/v1/jobToken/{exchange,refresh}
// response payload.
type jobTokenResponse struct {
	Token                 string `json:"token"`         // jt-..., 24h
	RefreshToken          string `json:"refresh_token"` // jrt-..., 48h
	ExpiresAt             string `json:"expires_at"`    // RFC3339
	ExpiresIn             int64  `json:"expires_in"`    // ms
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"` // ms
}

// doRawJSON sends method to fullURL with the given headers and returns the
// raw response body. Used for endpoints that return plain JSON (no envelope)
// like /api/v1/jobToken/exchange and /api/v1/jobToken/refresh.
func doRawJSON(client *http.Client, method, fullURL string, headers func(*http.Request), body io.Reader) (json.RawMessage, int, error) {
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, 0, err
	}
	if headers != nil {
		headers(req)
	} else {
		commonHeaders(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("http_error: upstream %d body=%s", resp.StatusCode, truncateRedacted(string(raw), 200))
	}
	if resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("http_error: upstream redirect %d (location: %s)", resp.StatusCode, resp.Header.Get("Location"))
	}
	return raw, resp.StatusCode, nil
}

// exchangePATForJobToken calls POST /api/v1/jobToken/exchange with a PAT and
// returns the resulting jt-/jrt- pair.
func exchangePATForJobToken(pat, region string) (*jobTokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"personal_token": pat})
	data, _, err := doRawJSON(sharedHTTPClient(), http.MethodPost, upstreamBaseForRegion(region)+"/api/v1/jobToken/exchange", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out jobTokenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("jobToken exchange parse: %w", err)
	}
	if out.Token == "" || out.RefreshToken == "" {
		return nil, fmt.Errorf("jobToken exchange: empty token pair in response")
	}
	return &out, nil
}

// refreshJobToken calls POST /api/v1/jobToken/refresh with a jrt-.
func refreshJobToken(jrt, region string) (*jobTokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": jrt})
	data, _, err := doRawJSON(sharedHTTPClient(), http.MethodPost, upstreamBaseForRegion(region)+"/api/v1/jobToken/refresh", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out jobTokenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("jobToken refresh parse: %w", err)
	}
	if out.Token == "" {
		return nil, fmt.Errorf("jobToken refresh: empty token in response")
	}
	return &out, nil
}

// userInfoResponse is the minimal subset of GET /api/v1/userinfo we need to
// populate auth identity (uid, nickname).
// Upstream returns: {"id":"...","name":"aliyun...","username":"...",
// "organization_id":"","organization_name":"",...}
type userInfoResponse struct {
	ID               string `json:"id"`       // uuid — used as our UID
	Name             string `json:"name"`     // display name
	Username         string `json:"username"` // alternate uuid
	Avatar           string `json:"avatar"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	UserType         string `json:"user_type"` // may be absent; falls back to "personal_professional_trial"
}

// fetchUserInfo queries /api/v1/userinfo with a jt- Bearer to populate the
// auth's identity fields (uid, nickname, user_type for COSY signing).
// The endpoint returns plain JSON (no envelope) — same as jobToken/exchange.
func fetchUserInfo(jt, region string) (*userInfoResponse, error) {
	req, err := http.NewRequest(http.MethodGet, endpointUserInfoForRegion(region), nil)
	if err != nil {
		return nil, err
	}
	commonHeaders(req)
	req.Header.Set("Authorization", "Bearer "+jt)
	resp, err := sharedHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("userinfo: http %d body=%s", resp.StatusCode, truncateRedacted(string(raw), 200))
	}
	var out userInfoResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("userinfo parse: %w (body=%s)", err, truncateRedacted(string(raw), 200))
	}
	return &out, nil
}

// buildStoredAuthFromJobToken constructs a storedAuth from a PAT + jobToken
// pair + user identity. Called by both PAT import and OAuth-like poll.
func buildStoredAuthFromJobToken(pat string, tok *jobTokenResponse, ui *userInfoResponse, region string) *storedAuth {
	expiresAt := time.Now().Add(24 * time.Hour).Unix()
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Millisecond).Unix()
	}

	nickname := ""
	uid := ""
	if ui != nil {
		uid = ui.ID
		nickname = ui.Name
	}

	// PersonalToken is stored inside RefreshToken only when refresh is via PAT
	// (no jrt available). For QoderWork we have a real jrt- (refresh token),
	// so RefreshToken carries the jrt- and the PAT itself is kept in Domain
	// alongside the realm for later re-exchange when jrt- expires (48h).
	return &storedAuth{
		Auth: storedTokens{
			AccessToken:   tok.Token,
			RefreshToken:  tok.RefreshToken,
			PersonalToken: pat, // PAT stored for automatic re-exchange when jrt- expires (48h)
			ExpiresAt:     expiresAt,
			Domain:        domainForRegion(region),
			Region:        region,
		},
		Account: storedAccount{
			UID:      uid,
			Nickname: nickname,
		},
	}
}

func uiUserType(ui *userInfoResponse) string {
	if ui == nil || ui.UserType == "" {
		return "personal_professional_trial"
	}
	return ui.UserType
}

// Device-authorization constants match cpa-workbuddy-plugin (806c61f).
// Authorization has its own random nonce/machine ID; UMID is resolved later
// for international campaign requests, never while starting a login.
const (
	qoderWebsiteCN       = "https://qoder.com.cn"
	qoderClientIDCN      = "1c5e33e1-364d-4ce6-b02c-acaa81274a5c"
	qoderRedirectURICN   = "qoder-work-cn://"
	qoderWebsiteIntl     = "https://qoder.com"
	qoderClientIDIntl    = qoderClientIDCN
	qoderRedirectURIIntl = "qoder-work://"
)

// deviceTokenResponse mirrors /api/v1/deviceToken/{poll,refresh} payloads.
// expires_in / refresh_token_expires_in are MILLISECONDS (client main.js and
// the live Global sample agree).
type deviceTokenResponse struct {
	Token                 string `json:"token"`
	DeviceToken           string `json:"device_token"` // refresh path returns this key instead
	RefreshToken          string `json:"refresh_token"`
	UserID                string `json:"user_id"`
	ExpiresAt             string `json:"expires_at"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
}

func (d *deviceTokenResponse) accessToken() string {
	if d.Token != "" {
		return d.Token
	}
	return d.DeviceToken
}

// makePKCE returns (verifier, challenge) per RFC 7636 (S256).
func makePKCE() (string, string) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	raw := make([]byte, 64)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failure is fatal for PKCE; fall back to uuid material.
		s := uuid.NewString() + uuid.NewString()
		sum := sha256.Sum256([]byte(s))
		v := base64.RawURLEncoding.EncodeToString(sum[:])
		return v, v
	}
	verifier := make([]byte, 64)
	for i, b := range raw {
		verifier[i] = alphabet[int(b)%len(alphabet)]
	}
	sum := sha256.Sum256(verifier)
	return string(verifier), base64.RawURLEncoding.EncodeToString(sum[:])
}

// handleStartLogin implements AuthProvider.StartLogin: build the device
// authorization URL and stash the PKCE verifier under the returned state.
func handleStartLogin(raw []byte) ([]byte, error) {
	// Request-carried region wins (default intl). Illegal values fail
	// before any state is stored.
	region, err := resolveRequestLoginRegion(raw, defaultRequestLoginRegion)
	if err != nil {
		return nil, err
	}
	return startLoginWithRegion(raw, region)
}

// startLoginWithRegion starts the reference device flow in the requested
// region. The management panel and host RPC share this single entry point.
// login_dialect remains a deprecated configuration no-op.
func startLoginWithRegion(raw []byte, region string) ([]byte, error) {
	if region != regionCN && region != regionIntl {
		return nil, fmt.Errorf("invalid login region %q: want \"cn\" or \"intl\" (default \"intl\"); current selection unchanged", region)
	}
	verifier, challenge := makePKCE()
	nonce := uuid.NewString()
	machineID := uuid.NewString()
	prompt := "在打开的页面中登录并授权 Qoder（设备授权，无需 PAT）。授权完成后插件会自动完成轮询登录。"

	q := url.Values{}
	q.Set("challenge", challenge)
	q.Set("challenge_method", "S256")
	q.Set("nonce", nonce)
	q.Set("machine_id", machineID)
	q.Set("client_id", qoderClientIDFor(region))
	if r := qoderRedirectURIFor(region); r != "" {
		q.Set("redirect_uri", r)
	}
	authURL := qoderWebsiteFor(region) + "/device/selectAccounts?" + q.Encode()

	now := time.Now()
	state := fmt.Sprintf("qw-%d", now.UnixNano())
	loginStates.Store(state, &loginCtx{verifier: verifier, nonce: nonce, region: region, expires: now.Add(loginTTL)})
	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  providerName,
		URL:       authURL,
		State:     state,
		ExpiresAt: now.Add(loginTTL).UTC(),
		Metadata: map[string]any{
			"logo":   pluginLogoURL,
			"prompt": prompt,
		},
	})
}

// persistPanelLoginAuth persists a freshly-authorized panel login through
// the same funnel as the panel PAT import (buildAuthFileJSON +
// hostAuthSaveJSONFn). It is the single persistence helper for all
// panel-side login completions (device poll success + callback PAT legacy);
// callers must not duplicate host.auth.save logic.
func persistPanelLoginAuth(sa *storedAuth) (string, error) {
	fileJSON, err := buildAuthFileJSON(sa, false, displayNote(sa, nil, false), nil)
	if err != nil {
		return "", err
	}
	name := authFileNameFor(sa)
	if strings.TrimSpace(name) == "" {
		name = authFileName
	}
	if err := hostAuthSaveJSONFn(name, fileJSON); err != nil {
		return "", err
	}
	return name, nil
}

// handlePanelLoginStart serves POST <base>/login/start (management panel
// "授权登录" button): the JSON body carries the same region keys as
// auth.login_start (absent → intl default; any other value → 400 and nothing
// is stored). It runs one startLoginWithRegion and returns {auth_url,
// region, state, expires_at} for the panel OAuth modal; the modal keeps the
// state and polls POST <base>/login/poll (5s) until success or expiry.
func handlePanelLoginStart(req pluginapi.ManagementRequest) (int, map[string]any) {
	region, present, err := parseLoginRegionField(req.Body)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	if !present {
		region = defaultRequestLoginRegion
	}
	out, err := startLoginWithRegion(nil, region)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		return http.StatusInternalServerError, map[string]any{"error": "login start failed"}
	}
	var start pluginapi.AuthLoginStartResponse
	if err := json.Unmarshal(env.Result, &start); err != nil || start.URL == "" {
		return http.StatusInternalServerError, map[string]any{"error": "login start decode failed"}
	}
	return http.StatusOK, map[string]any{
		"auth_url":   start.URL,
		"region":     region,
		"state":      start.State,
		"expires_at": start.ExpiresAt.UTC().Format(time.RFC3339),
	}
}

// panelLoginPollSuccess persists a device-token grant and returns the
// success payload shared by poll + callback. The state is deleted only
// after the auth file lands.
func panelLoginPollSuccess(state string, lc *loginCtx, tok *deviceTokenResponse) (int, map[string]any) {
	sa := buildStoredAuthFromDeviceToken(tok, nil, lc.region)
	name, err := persistPanelLoginAuth(sa)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"status": "error", "error": "persist auth: " + err.Error()}
	}
	loginStates.Delete(state)
	return http.StatusOK, map[string]any{
		"status":   "success",
		"file":     name,
		"uid":      sa.Account.UID,
		"nickname": sa.Account.Nickname,
		"region":   lc.region,
	}
}

// handlePanelLoginPoll serves POST <base>/login/poll {state}: one
// device-authorization poll via pollDeviceToken. Unknown state → error with
// a restart hint; expired state is deleted and reported as expired.
func handlePanelLoginPoll(req pluginapi.ManagementRequest) (int, map[string]any) {
	var body struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(req.Body, &body)
	state := strings.TrimSpace(body.State)
	if state == "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "missing state (restart login)"}
	}
	v, ok := loginStates.Load(state)
	if !ok {
		return http.StatusNotFound, map[string]any{"status": "error", "error": "poll: unknown state (restart login)"}
	}
	lc := v.(*loginCtx)
	if time.Now().After(lc.expires) {
		loginStates.Delete(state)
		return http.StatusGone, map[string]any{"status": "expired", "error": "poll: login expired (restart login)"}
	}
	if lc.region != regionCN && lc.region != regionIntl {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": fmt.Sprintf("invalid login region %q: want \"cn\" or \"intl\" (restart login)", lc.region)}
	}
	tok, pending, err := pollDeviceToken(lc.nonce, lc.verifier, lc.region)
	if err != nil {
		return http.StatusBadGateway, map[string]any{"status": "error", "error": "device authorization poll: " + err.Error()}
	}
	if !pending && tok != nil {
		return panelLoginPollSuccess(state, lc, tok)
	}
	return http.StatusOK, map[string]any{"status": "pending", "message": "等待浏览器完成 Qoder 设备授权"}
}

// handlePanelLoginCallback serves POST <base>/login/callback
// {state, callback_url}: a pasted pt- PAT takes the legacy PAT exchange
// path (same as handlePollLogin Metadata pat); any other callback value
// triggers exactly one immediate device-poll attempt and returns its
// status. No code-exchange flow is invented — the callback URL is only the
// user's completion signal. Invalid region → 400 with nothing stored;
// unknown/expired state → error with a restart hint.
func handlePanelLoginCallback(req pluginapi.ManagementRequest) (int, map[string]any) {
	var body struct {
		State       string `json:"state"`
		CallbackURL string `json:"callback_url"`
		Callback    string `json:"callback"`
		CallbackUrl string `json:"callbackUrl"`
		URL         string `json:"url"`
		PAT         string `json:"pat"`
	}
	_ = json.Unmarshal(req.Body, &body)
	state := strings.TrimSpace(body.State)
	if state == "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "missing state (restart login)"}
	}
	v, ok := loginStates.Load(state)
	if !ok {
		return http.StatusNotFound, map[string]any{"status": "error", "error": "poll: unknown state (restart login)"}
	}
	lc := v.(*loginCtx)
	if time.Now().After(lc.expires) {
		loginStates.Delete(state)
		return http.StatusGone, map[string]any{"status": "expired", "error": "poll: login expired (restart login)"}
	}
	if lc.region != regionCN && lc.region != regionIntl {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": fmt.Sprintf("invalid login region %q: want \"cn\" or \"intl\" (restart login)", lc.region)}
	}
	cb := strings.TrimSpace(body.CallbackURL)
	if cb == "" {
		cb = strings.TrimSpace(body.Callback)
	}
	if cb == "" {
		cb = strings.TrimSpace(body.CallbackUrl)
	}
	if cb == "" {
		cb = strings.TrimSpace(body.URL)
	}
	if cb == "" {
		cb = strings.TrimSpace(body.PAT)
	}
	if cb == "" {
		return http.StatusBadRequest, map[string]any{"status": "error", "error": "missing callback_url"}
	}
	// Legacy path: user pasted a PAT into the modal (same exchange as
	// handlePollLogin Metadata pat).
	if strings.HasPrefix(cb, "pt-") {
		tok, err := exchangePATForJobToken(cb, lc.region)
		if err != nil {
			return http.StatusBadGateway, map[string]any{"status": "error", "error": "PAT exchange failed: " + err.Error()}
		}
		ui, _ := fetchUserInfo(tok.Token, lc.region) // best-effort
		sa := buildStoredAuthFromJobToken(cb, tok, ui, lc.region)
		name, err := persistPanelLoginAuth(sa)
		if err != nil {
			return http.StatusInternalServerError, map[string]any{"status": "error", "error": "persist auth: " + err.Error()}
		}
		loginStates.Delete(state)
		return http.StatusOK, map[string]any{
			"status":   "success",
			"file":     name,
			"uid":      sa.Account.UID,
			"nickname": sa.Account.Nickname,
			"region":   lc.region,
		}
	}
	// Otherwise: exactly one immediate device-poll attempt.
	tok, pending, err := pollDeviceToken(lc.nonce, lc.verifier, lc.region)
	if err != nil {
		return http.StatusBadGateway, map[string]any{"status": "error", "error": "device authorization poll: " + err.Error()}
	}
	if !pending && tok != nil {
		return panelLoginPollSuccess(state, lc, tok)
	}
	return http.StatusOK, map[string]any{"status": "pending", "message": "等待浏览器完成 Qoder 设备授权"}
}

// pollDeviceToken performs one GET against /api/v1/deviceToken/poll.
// Returns (tok, pending, error): pending=true means the user hasn't finished
// authorizing yet (upstream 404/202) — the host should keep polling.
func pollDeviceToken(nonce, verifier, region string) (*deviceTokenResponse, bool, error) {
	q := url.Values{}
	q.Set("nonce", nonce)
	q.Set("verifier", verifier)
	q.Set("challenge_method", "S256")
	fullURL := upstreamBaseForRegion(region) + "/api/v1/deviceToken/poll?" + q.Encode()

	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/json")
	if region == regionIntl {
		req.Header.Set("User-Agent", "Qoder AI")
	} else {
		req.Header.Set("User-Agent", "QoderWork")
	}
	resp, err := sharedHTTPClient().Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusAccepted {
		return nil, true, nil
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("deviceToken poll: http %d body=%s", resp.StatusCode, truncateRedacted(string(raw), 200))
	}
	var out deviceTokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("deviceToken poll parse: %w", err)
	}
	if out.accessToken() == "" {
		return nil, true, nil // 200 without token yet — treat as pending
	}
	return &out, false, nil
}

// refreshDeviceToken calls POST /api/v1/deviceToken/refresh with a drt-.
func refreshDeviceToken(drt, region string) (*deviceTokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": drt})
	data, _, err := doRawJSON(sharedHTTPClient(), http.MethodPost, upstreamBaseForRegion(region)+"/api/v1/deviceToken/refresh", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return parseDeviceTokenRefresh(data)
}

// Both proactive keepalive and host-triggered refresh consume this contract.
func parseDeviceTokenRefresh(data []byte) (*deviceTokenResponse, error) {
	var out deviceTokenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("deviceToken refresh parse: %w", err)
	}
	if out.accessToken() == "" || out.RefreshToken == "" {
		return nil, fmt.Errorf("deviceToken refresh: incomplete token pair in response")
	}
	return &out, nil
}

// handlePollLogin completes this state through its PKCE device grant or an
// explicitly supplied PAT. Unrelated auth-file arrivals cannot complete it.
func handlePollLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	state := strings.TrimSpace(req.State)
	if state == "" {
		return nil, fmt.Errorf("poll: empty state")
	}
	v, ok := loginStates.Load(state)
	if !ok {
		return nil, fmt.Errorf("poll: unknown state (restart login)")
	}
	lc := v.(*loginCtx)
	if time.Now().After(lc.expires) {
		loginStates.Delete(state)
		return nil, fmt.Errorf("poll: login expired (10 min timeout)")
	}

	// Legacy path: user pasted PAT into the OAuth modal callback.
	pat := ""
	if req.Metadata != nil {
		if v, ok := req.Metadata["pat"].(string); ok {
			pat = strings.TrimSpace(v)
		}
	}
	if pat != "" {
		if !strings.HasPrefix(pat, "pt-") {
			return nil, fmt.Errorf("poll: PAT must start with pt-")
		}
		tok, err := exchangePATForJobToken(pat, lc.region)
		if err != nil {
			return nil, fmt.Errorf("PAT exchange failed: %w", err)
		}
		ui, _ := fetchUserInfo(tok.Token, lc.region)
		sa := buildStoredAuthFromJobToken(pat, tok, ui, lc.region)
		loginStates.Delete(state)
		ad := toAuthData(sa)
		// Let the host derive the registration ID from the saved file path.
		ad.ID = ""
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status: pluginapi.AuthLoginStatusSuccess,
			Auth:   ad,
		})
	}

	// Device-authorization path: poll the grant. The verifier never left
	// this process; the nonce pairs it to the auth URL we handed out.
	tok, pending, err := pollDeviceToken(lc.nonce, lc.verifier, lc.region)
	if err != nil {
		return nil, fmt.Errorf("device authorization poll: %w", err)
	}
	if !pending && tok != nil {
		// Auth file must land IMMEDIATELY — no upstream calls before return.
		// The host writes the auth file from AuthLoginPollResponse, so any
		// blocking work (fetchUserInfo, hostAuthList) delays persistence.
		// UserID is already in the poll response; nickname is fetched lazily
		// by the panel on first load. PAT coalescing happens on keepalive.
		sa := buildStoredAuthFromDeviceToken(tok, nil, lc.region)
		loginStates.Delete(state)
		ad := toAuthData(sa)
		// Let the host derive the registration ID from the saved file path.
		ad.ID = ""
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status: pluginapi.AuthLoginStatusSuccess,
			Auth:   ad,
		})
	}

	return okEnvelope(pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusPending,
		Message: "等待浏览器完成 Qoder 设备授权",
	})
}

// deviceExpiryUnix resolves the access-token expiry from a deviceToken
// poll/refresh response. Poll returns expires_in (ms); refresh returns only
// expires_at (RFC3339). Default: 30 days (observed dt- lifetime).
func deviceExpiryUnix(tok *deviceTokenResponse) int64 {
	if tok.ExpiresIn > 0 {
		return time.Now().Add(time.Duration(tok.ExpiresIn) * time.Millisecond).Unix()
	}
	if tok.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, tok.ExpiresAt); err == nil {
			return t.Unix()
		}
	}
	return time.Now().Add(30 * 24 * time.Hour).Unix()
}

// buildStoredAuthFromDeviceToken maps a device-token grant onto storedAuth.
// AccessToken=dt-, RefreshToken=drt-. When ui is nil (fast path during
// PollLogin), UserID comes from the poll response and nickname is empty —
// the panel fills it lazily on first load. preservePAT=false skips the
// hostAuthList scan to avoid blocking auth-file persistence.
func buildStoredAuthFromDeviceToken(tok *deviceTokenResponse, ui *userInfoResponse, region string) *storedAuth {
	expiresAt := deviceExpiryUnix(tok)
	uid := tok.UserID
	nickname := ""
	if ui != nil {
		if ui.ID != "" {
			uid = ui.ID
		}
		nickname = ui.Name
	}
	// When ui is nil (fast PollLogin path), derive a readable nickname from
	// the uid so the panel doesn't fall back to the raw filename label.
	if nickname == "" {
		if len(uid) > 8 {
			nickname = "u" + uid[len(uid)-8:]
		} else {
			nickname = "u" + uid
		}
	}
	return &storedAuth{
		Auth: storedTokens{
			AccessToken:   tok.accessToken(),
			RefreshToken:  tok.RefreshToken,
			PersonalToken: "", // PAT coalescing happens on keepalive, not here
			ExpiresAt:     expiresAt,
			Domain:        domainForRegion(region),
			Region:        region,
		},
		Account: storedAccount{UID: uid, Nickname: nickname},
	}
}

// existingPATForUID looks up an existing qoderwork auth file for the same
// uid and returns its stored PAT (empty when none). Lets OAuth re-login
// preserve a previously imported PAT instead of wiping it.
//
// NOTE: This calls hostAuthList which is a blocking host RPC. It must NOT
// be called during PollLogin (delays auth file persistence). It is safe to
// call during keepalive/refresh which happens long after the file is written.
func existingPATForUID(uid string) string {
	if uid == "" {
		return ""
	}
	files, err := hostAuthList()
	if err != nil {
		return ""
	}
	for _, f := range files {
		if !strings.HasPrefix(strings.ToLower(f.Name), providerName+"-") {
			continue
		}
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil || sa == nil {
			continue
		}
		if sa.Account.UID == uid && strings.HasPrefix(sa.Auth.PersonalToken, "pt-") {
			return sa.Auth.PersonalToken
		}
	}
	return ""
}

// handleRefreshAuth implements AuthProvider.Refresh. The two credential
// families coexist in one auth file and are routed by TOKEN PREFIX, not by
// the presence of a PAT — an OAuth account may ALSO carry a PAT for fallback:
//
//  1. OAuth device family (refresh token starts with "drt-"): POST
//     /api/v1/deviceToken/refresh — the device flow's own endpoint.
//     The PAT (if any) is preserved untouched in the file.
//  2. Legacy PAT family (refresh token starts with "jrt-", or a bare PAT):
//     jrt- refresh → PAT re-exchange, as before.
//
// The previous PersonalToken-presence routing had a fatal flaw: once a PAT
// coexisted, a device account was force-refreshed through the jobToken
// endpoints, which reject drt- tokens, and the PAT fallback then OVERWROTE
// the OAuth credential with a jobToken pair — destroying the OAuth session.
func handleRefreshAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := parseStored(req.StorageJSON)
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}

	if strings.HasPrefix(sa.Auth.RefreshToken, "drt-") {
		// OAuth device family — deviceToken/refresh ONLY.
		tok, err := refreshDeviceToken(sa.Auth.RefreshToken, authRegion(sa))
		if err != nil {
			return nil, fmt.Errorf("refresh rejected: %w — deviceToken refresh failed; re-login via OAuth required", err)
		}
		sa.Auth.AccessToken = tok.accessToken()
		sa.Auth.RefreshToken = tok.RefreshToken
		sa.Auth.ExpiresAt = preserveExpiry(deviceExpiryUnix(tok), sa.Auth.ExpiresAt)
		invalidateCosySession(sa.Account.UID)
		return okEnvelope(pluginapi.AuthRefreshResponse{Auth: toAuthDataForRefresh(sa)})
	}

	// Legacy PAT family: Tier 1 jrt- refresh → Tier 2 PAT re-exchange.
	tok, err := refreshJobToken(sa.Auth.RefreshToken, authRegion(sa))
	if err != nil && sa.Auth.PersonalToken != "" {
		// Tier 2: jrt- expired — fall back to PAT re-exchange.
		tok, err = exchangePATForJobToken(sa.Auth.PersonalToken, authRegion(sa))
	}
	if err != nil {
		return nil, fmt.Errorf("refresh rejected: %w — both jrt- refresh and PAT re-exchange failed; re-import PAT required", err)
	}
	sa.Auth.AccessToken = tok.Token
	if tok.RefreshToken != "" {
		sa.Auth.RefreshToken = tok.RefreshToken
	}
	sa.Auth.ExpiresAt = preserveExpiry(
		time.Now().Add(time.Duration(tok.ExpiresIn)*time.Millisecond).Unix(),
		sa.Auth.ExpiresAt,
	)
	invalidateCosySession(sa.Account.UID)
	// Host persists the refreshed credential itself after Refresh returns
	// (conductor.go refreshAuth → m.Update → persist). Writing from the
	// plugin too would double-write the file.
	return okEnvelope(pluginapi.AuthRefreshResponse{Auth: toAuthDataForRefresh(sa)})
}

// preserveExpiry reuses the previous token's expiresAt when the refresh
// response omits expiresIn. Zero would tell the host the credential is
// permanently expired and trigger a refresh storm on every request.
// Both sides are normalised through tokenExpiryUnix (0.8.42, adapted from
// bfSan f05e9e3): the stored field is seconds by contract, but historical
// writers produced millisecond values, which read back as 1970 and made
// every token look long expired.
func preserveExpiry(newExpiry, oldExpiry int64) int64 {
	if normalized := tokenExpiryUnix(newExpiry); normalized > 0 {
		return normalized
	}
	return tokenExpiryUnix(oldExpiry)
}

// toAuthDataForRefresh mirrors the workbuddy helper: blank out FileName and
// ID so the host backfills from the original auth path (prevents ID mismatch
// duplicate files when Refresh round-trips the record).
func toAuthDataForRefresh(sa *storedAuth) pluginapi.AuthData {
	ad := toAuthDataOpts(sa, nil, false)
	ad.FileName = "" // let host backfill original
	ad.ID = ""       // let host compute from path (prevents ID mismatch dupes)
	return ad
}
