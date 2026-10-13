// oauth.go implements the AuthProvider login flow: browser-driven OAuth via
// CodeBuddy's login endpoints (CN and Global), login state polling, and token
// refresh. Each login flow gets an isolated cookie jar so multi-account flows
// never cross-contaminate session state.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// newLoginClient builds an isolated client with its own cookie jar so that the
// browser login for one state can never leak into another.
func newLoginClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: sharedHTTPClient().Transport,
		Jar:       jar,
	}
}

// doJSON sends method to fullURL with the given headers, parses the {code,msg,data}
// envelope, and returns the inner data payload. httpStatus is the upstream code.
func doJSON(client *http.Client, method, fullURL string, headers func(*http.Request), body io.Reader) (json.RawMessage, int, error) {
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, 0, err
	}
	if headers != nil {
		headers(req)
	} else {
		loginHeaders(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, fmt.Errorf("http_error: upstream %d", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		// Redirects: Go's client follows them for GET, but a 3xx that lands
		// here (e.g. POST 307/308 not re-sent, or a new upstream gateway) would
		// otherwise surface as a misleading JSON "parse failed".
		return nil, resp.StatusCode, fmt.Errorf("http_error: upstream redirect %d (location: %s)", resp.StatusCode, resp.Header.Get("Location"))
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("parse failed: %w", err)
	}
	if env.Code != 0 {
		return nil, resp.StatusCode, fmt.Errorf("code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	return env.Data, resp.StatusCode, nil
}

// loginHeaders applies the base headers plus the X-IDE-* client headers when
// the current login platform is "ide" (CodeBuddy IDE login variant).
func loginHeaders(req *http.Request) {
	commonHeaders(req)
	applyPlatformHeaders(req, currentLoginPlatform())
}

// loginEndpointsOverride（issue #36 测试缝）：非空时把登录三端点
// （auth/state、auth/token、login/account，CN 与 Global 路径同形）重定向到该
// base，让 hermetic 测试用 httptest 服务器驱动完整登录流（与
// growthChatBaseOverride 同型）。生产恒为空，三个 base 解析函数在生产路径
// 与原常量拼接字节级等价。
var loginEndpointsOverride string

// loginStateBaseFor / loginTokenBaseFor / loginAccountBaseFor resolve the
// per-realm login endpoint bases, honoring the test override above.
func loginStateBaseFor(region string) string {
	if loginEndpointsOverride != "" {
		return loginEndpointsOverride + "/v2/plugin/auth/state?platform="
	}
	if region == regionGlobal {
		return upstreamBaseForRegion(region) + "/v2/plugin/auth/state?platform="
	}
	return endpointAuthStateBase
}

func loginTokenBaseFor(region string) string {
	if loginEndpointsOverride != "" {
		return loginEndpointsOverride + "/v2/plugin/auth/token?state="
	}
	if region == regionGlobal {
		return upstreamBaseForRegion(region) + "/v2/plugin/auth/token?state="
	}
	return endpointAuthToken
}

func loginAccountBaseFor(region string) string {
	if loginEndpointsOverride != "" {
		return loginEndpointsOverride + "/v2/plugin/login/account?state="
	}
	if region == regionGlobal {
		return upstreamBaseForRegion(region) + "/v2/plugin/login/account?state="
	}
	return endpointLoginAcct
}

func handleStartLogin(raw []byte) ([]byte, error) {
	// Request-carried region only (default global). Illegal values fail before
	// any network I/O.
	region, err := resolveRequestLoginRegion(raw)
	if err != nil {
		return nil, err
	}
	return startLoginWithRegion(raw, region)
}

// startLoginWithRegion starts a login flow pinned to region. The host RPC
// entry (handleStartLogin) resolves the request-carried region (default
// global); there is no config fallback.
func startLoginWithRegion(raw []byte, region string) ([]byte, error) {
	// Only cn/global route; anything else is an explicit rejection (this path
	// never writes config).
	if region != regionCN && region != regionGlobal {
		return nil, fmt.Errorf("invalid login region %q: want \"cn\" or \"global\" (default \"global\")", region)
	}
	client := newLoginClient()
	platform := currentLoginPlatform()
	stateBase := loginStateBaseFor(region)
	data, _, err := doJSON(client, http.MethodPost, stateBase+platform, nil, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("auth state failed: %w", err)
	}
	var st authStateData
	_ = json.Unmarshal(data, &st)
	if st.State == "" || st.AuthURL == "" {
		return nil, fmt.Errorf("auth state: missing state or authUrl — please restart the login flow")
	}
	// issue #36（Part 2）：把实际使用的平台随登录态存下——poll 成功时写进
	// sa.Auth.LoginPlatform，钉死 token 血统的作用域（方言头族）。
	loginStates.Store(st.State, &loginCtx{client: client, region: region, platform: platform, expires: time.Now().Add(loginTTL)})
	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  providerName,
		URL:       st.AuthURL,
		State:     st.State,
		ExpiresAt: time.Now().Add(loginTTL).UTC(),
		Metadata:  map[string]any{"logo": pluginLogoURL},
	})
}

// handleLoginStart serves POST <base>/login/start for the management panel
// authorize flow. The body may carry {region: cn|global} (login_region /
// variant keys are accepted too); when absent the login defaults to global.
// Any other non-empty value is a 400 and nothing is persisted. On success it
// returns {auth_url, region, state, expires_at}; the panel shows the URL in
// an in-panel OAuth modal and polls POST <base>/login/poll until success.
func handleLoginStart(req pluginapi.ManagementRequest) (int, any) {
	region, present, err := parseLoginRegionField(req.Body)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	if !present {
		region = defaultRequestLoginRegion
	}
	out, err := startLoginWithRegion(req.Body, region)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error(), "region": region}
	}
	var env envelope
	if uerr := json.Unmarshal(out, &env); uerr != nil || !env.OK {
		return http.StatusBadRequest, map[string]any{"error": "login start failed", "region": region}
	}
	var start pluginapi.AuthLoginStartResponse
	if uerr := json.Unmarshal(env.Result, &start); uerr != nil || start.URL == "" {
		return http.StatusBadRequest, map[string]any{"error": "login start failed", "region": region}
	}
	return http.StatusOK, map[string]any{
		"auth_url":   start.URL,
		"region":     region,
		"state":      start.State,
		"expires_at": start.ExpiresAt,
	}
}

// loginPollOutcome is the panel-facing result of a single login poll attempt.
// Status mirrors the host-poll vocabulary (pending | success | error) plus
// "expired" synthesized for lost/timed-out login sessions so the panel can
// show a restart hint without parsing error text.
type loginPollOutcome struct {
	Status  string
	SA      *storedAuth
	Message string
	Err     error
}

// loadLoginCtx resolves an in-flight login session by state. Unknown states
// (lost session — re-initiate login) and TTL-expired sessions (deleted here)
// both surface restart-hint errors shared by the host poll RPC and the panel
// poll/callback endpoints.
func loadLoginCtx(state string) (*loginCtx, error) {
	v, ok := loginStates.Load(state)
	if !ok {
		return nil, fmt.Errorf("poll: unknown state (restart login) — the login session was lost; please re-initiate login")
	}
	lc := v.(*loginCtx)
	if time.Now().After(lc.expires) {
		loginStates.Delete(state)
		return nil, fmt.Errorf("poll: login expired (5 min timeout) — please re-initiate login and complete within 5 minutes")
	}
	return lc, nil
}

// attemptLoginPoll runs ONE login-completion attempt against the loginCtx's
// cookie-affined client: auth/token is authoritative (non-zero business code
// or 4xx = still pending; transport/5xx = real error), then login/account,
// then plugin-side persist via persistLoginCredential and state deletion.
// Identical mechanics for the host poll RPC and the panel endpoints.
func attemptLoginPoll(state string, lc *loginCtx) loginPollOutcome {
	// auth/token is the authoritative login-status endpoint: the application
	// layer returns a non-zero code ("login ing") while pending, and code 0
	// with the token bundle once complete. login/account sits behind the
	// openresty gateway and is rejected (401) until login finishes, so probe
	// token first and only fetch account once we hold a bearer.
	tokenBase := loginTokenBaseFor(lc.region)
	tokRaw, status, errTok := doJSON(lc.client, http.MethodGet, tokenBase+state, nil, nil)
	if errTok != nil {
		// Transport-level failures and 5xx are real errors, not "still waiting":
		// surface them so the user sees a failure instead of polling until TTL.
		if status == 0 || status >= 500 {
			loginStates.Delete(state)
			return loginPollOutcome{Status: string(pluginapi.AuthLoginStatusError), Err: fmt.Errorf("poll: token endpoint error: %w — upstream may be temporarily unavailable; retry in a few minutes", errTok)}
		}
		// 4xx / business-code responses mean the login is still pending.
		return loginPollOutcome{Status: string(pluginapi.AuthLoginStatusPending), Message: "waiting for login"}
	}
	var tok tokenData
	if err := json.Unmarshal(tokRaw, &tok); err != nil || tok.AccessToken == "" {
		return loginPollOutcome{Status: string(pluginapi.AuthLoginStatusPending), Message: "waiting for login"}
	}

	acct, acctErr := fetchLoginAccount(lc.client, lc.region, state, tok.AccessToken)
	if strings.TrimSpace(acct.UID) == "" {
		// Identity fallback: pull the uid out of the access token's JWT
		// claims when the account endpoint stayed empty after the retry
		// ladder (upstream outage or response-shape change).
		acct.UID = uidFromTokenClaims(tok.AccessToken)
	}
	if strings.TrimSpace(acct.UID) == "" {
		// v0.9.43: never mint a UID-less credential. The old behavior
		// silently continued with an empty account, which made
		// toAuthDataOptsWithNote fall back to the legacy single-account
		// filename workbuddy.json — a name the panel's family filter
		// does not list. The login looked successful on both the
		// upstream page AND the CPA UI (the host had saved the file),
		// yet no credential card ever appeared. Failing the poll
		// surfaces the real cause in the CPA UI instead.
		loginStates.Delete(state)
		if acctErr == nil {
			acctErr = fmt.Errorf("account payload carried no uid")
		}
		return loginPollOutcome{Status: string(pluginapi.AuthLoginStatusError), Err: fmt.Errorf("login: upstream account endpoint returned no uid after retries (%v) — credential withheld; please retry login", acctErr)}
	}

	sa := &storedAuth{
		Auth: storedTokens{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
			Domain:       tok.Domain,
		},
		Account: storedAccount{
			UID:          acct.UID,
			EnterpriseID: acct.EnterpriseID,
			Nickname:     acct.Nickname,
		},
	}
	// Pin the region explicitly (v0.12.15): credential-manager notes and
	// labels resolve via accountRegion; domain sniffing is only a legacy
	// fallback.
	sa.Auth.Region = lc.region
	// issue #36（Part 2）：登录平台血统持久化——不写这一行，IDE 流程铸造的
	// token 在聊天时会以 CLI 头族出现（platformForAuth 回退 CLI），token
	// 血统与请求方言错配。遗留值保持原样（adopt 路径有自己的回填）。
	sa.Auth.LoginPlatform = firstNonEmpty(lc.platform, "CLI")
	// v0.9.44: persist the credential plugin-side BEFORE reporting success.
	// The host saves the poll's Auth payload itself, but that save rides the
	// login dialog's lifecycle — mimo hit the same failure class (v0.2.10:
	// "login complete" shown while the credential never landed). Persisting
	// here makes "poll success" imply "credential file exists under the
	// canonical name" regardless of what the host does next; the host's own
	// save converges on the same FileName (same record key, no duplicate).
	// Non-fatal: the host save stays primary; a bridge hiccup here only
	// costs the redundancy, never the login.
	persistLoginCredential(sa)
	loginStates.Delete(state)
	return loginPollOutcome{Status: string(pluginapi.AuthLoginStatusSuccess), SA: sa}
}

// runLoginPollAttempt is attemptLoginPoll after the shared state lookup:
// unknown/expired sessions map to the "expired" status carrying the same
// restart-hint error the host poll RPC returns.
func runLoginPollAttempt(state string) loginPollOutcome {
	lc, err := loadLoginCtx(state)
	if err != nil {
		return loginPollOutcome{Status: "expired", Err: err}
	}
	return attemptLoginPoll(state, lc)
}

// loginPollStatusPayload maps one poll outcome to the panel status envelope
// {status: pending|success|expired|error} shared by /login/poll and
// /login/callback.
func loginPollStatusPayload(out loginPollOutcome) map[string]any {
	switch out.Status {
	case string(pluginapi.AuthLoginStatusSuccess):
		payload := map[string]any{"status": "success"}
		if out.SA != nil {
			payload["uid"] = out.SA.Account.UID
			if out.SA.Account.Nickname != "" {
				payload["nickname"] = out.SA.Account.Nickname
			}
		}
		return payload
	case string(pluginapi.AuthLoginStatusPending):
		return map[string]any{"status": "pending", "message": out.Message}
	case "expired":
		return map[string]any{"status": "expired", "error": out.Err.Error()}
	default:
		msg := "login failed"
		if out.Err != nil {
			msg = out.Err.Error()
		}
		return map[string]any{"status": "error", "error": msg}
	}
}

// handleLoginPoll serves POST <base>/login/poll for the in-panel OAuth modal.
// The body carries {state} from POST <base>/login/start. It runs a single
// poll attempt with the plugin's existing login-completion mechanics
// (attemptLoginPoll: token probe → account fetch → persistLoginCredential →
// state deletion) and returns {status: pending|success|expired|error}.
// Unknown/expired states return the "expired" status with a restart hint
// (mirroring the host poll RPC's error text); malformed bodies are 400.
func handleLoginPoll(req pluginapi.ManagementRequest) (int, any) {
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return http.StatusBadRequest, map[string]any{"error": "login poll: invalid JSON body — please restart the login flow"}
	}
	state := strings.TrimSpace(body.State)
	if state == "" {
		return http.StatusBadRequest, map[string]any{"error": "poll: empty state — please restart the login flow"}
	}
	return http.StatusOK, loginPollStatusPayload(runLoginPollAttempt(state))
}

// parseLoginCallbackBody extracts the {state, callback_url} pair from a
// POST <base>/login/callback body. callback_url accepts the callback_url /
// callbackUrl / callback key spellings (the panel sends callback_url).
func parseLoginCallbackBody(raw []byte) (state, callbackURL string, err error) {
	var m map[string]json.RawMessage
	if uerr := json.Unmarshal(raw, &m); uerr != nil || len(m) == 0 {
		return "", "", fmt.Errorf("login callback: invalid JSON body — please restart the login flow")
	}
	for mk, mv := range m {
		switch strings.ToLower(strings.TrimSpace(mk)) {
		case "state":
			var s string
			if uerr := json.Unmarshal(mv, &s); uerr != nil {
				s = strings.Trim(string(mv), "\"'")
			}
			state = strings.TrimSpace(strings.Trim(s, "\"'"))
		case "callback_url", "callbackurl", "callback":
			var s string
			if uerr := json.Unmarshal(mv, &s); uerr != nil {
				s = strings.Trim(string(mv), "\"'")
			}
			callbackURL = strings.TrimSpace(strings.Trim(s, "\"'"))
		}
	}
	return state, callbackURL, nil
}

// handleLoginCallback serves POST <base>/login/callback for the in-panel
// OAuth modal. The body carries {state, callback_url}: the callback URL the
// user pasted from the browser address bar after authorizing. WorkBuddy's
// login mechanics complete purely via the auth/token probe keyed by state
// (there is no authCode-exchange flow to feed), so after validating the
// pair the handler runs one immediate poll attempt (attemptLoginPoll) and
// returns its {status: pending|success|expired|error} envelope. A callback
// URL carrying a *different* state query param is a 400 (wrong login pasted).
// Invalid region in the body is a 400 and nothing is stored; unknown/expired
// states return the restart-hint error.
func handleLoginCallback(req pluginapi.ManagementRequest) (int, any) {
	// A valid region echo is accepted and ignored: the login realm is
	// already pinned on the stored loginCtx at start time.
	if _, _, err := parseLoginRegionField(req.Body); err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	state, callbackURL, err := parseLoginCallbackBody(req.Body)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	if state == "" {
		return http.StatusBadRequest, map[string]any{"error": "login callback: empty state — please restart the login flow"}
	}
	lc, err := loadLoginCtx(state)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"error": err.Error()}
	}
	if callbackURL == "" {
		return http.StatusBadRequest, map[string]any{"error": "login callback: empty callback_url — authorize in the opened link, then paste the full browser address-bar URL"}
	}
	if u, uerr := url.Parse(callbackURL); uerr == nil && u != nil {
		if echoed := strings.TrimSpace(u.Query().Get("state")); echoed != "" && echoed != state {
			return http.StatusBadRequest, map[string]any{"error": "login callback: pasted URL belongs to a different login (state mismatch) — paste the URL from THIS login attempt, or restart the login flow"}
		}
	}
	return http.StatusOK, loginPollStatusPayload(attemptLoginPoll(state, lc))
}

func handlePollLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	state := strings.TrimSpace(req.State)
	if state == "" {
		return nil, fmt.Errorf("poll: empty state")
	}
	lc, err := loadLoginCtx(state)
	if err != nil {
		return nil, err
	}

	// Single-shot poll per RPC: the host drives the polling cadence.
	out := attemptLoginPoll(state, lc)
	switch out.Status {
	case string(pluginapi.AuthLoginStatusPending):
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for login",
		})
	case string(pluginapi.AuthLoginStatusSuccess):
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status: pluginapi.AuthLoginStatusSuccess,
			Auth:   toAuthData(out.SA),
		})
	default:
		return nil, out.Err
	}
}

// persistLoginCredential writes the just-minted login credential to the host
// auth store under the canonical file-layer name (authFileNameFor).
// Best-effort: failures are logged and swallowed
// because the host's post-poll save remains the primary persistence path.
func persistLoginCredential(sa *storedAuth) {
	raw, err := buildAuthFileJSON(sa, false, displayNoteWithPrev(sa, nil, false, ""), nil)
	if err != nil {
		log.Printf("workbuddy: login persist (plugin-side) marshal failed: %v", err)
		return
	}
	if err := hostAuthPersistFn(authFileNameFor(sa), "", raw); err != nil {
		log.Printf("workbuddy: login persist (plugin-side) failed — host save remains primary: %v", err)
	}
}

// loginAcctRetryDelays bounds the in-poll retry ladder for the account
// fetch. Documented upstream race (see handlePollLogin): login/account sits
// behind the openresty gateway and is rejected (401) for a short moment
// right after auth/token succeeds. Var so tests can collapse the wait.
var loginAcctRetryDelays = []time.Duration{400 * time.Millisecond, 800 * time.Millisecond}

// fetchLoginAccount fetches the login/account payload for a completed token
// exchange, retrying transient failures. An empty-UID payload counts as a
// failure — the caller decides whether to fall back or fail the login.
func fetchLoginAccount(client *http.Client, region, state, accessToken string) (accountData, error) {
	return fetchLoginAccountAt(client, loginAccountBaseFor(region), region, state, accessToken)
}

// fetchLoginAccountAt is fetchLoginAccount against an explicit base URL
// (test seam: endpointLoginAcct is a package const).
func fetchLoginAccountAt(client *http.Client, acctBase, region, state, accessToken string) (accountData, error) {
	headers := func(r *http.Request) {
		loginHeaders(r)
		r.Header.Set("Authorization", "Bearer "+accessToken)
	}
	var acct accountData
	var lastErr error
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if attempt > len(loginAcctRetryDelays) {
				return acct, lastErr
			}
			time.Sleep(loginAcctRetryDelays[attempt-1])
		}
		acctRaw, status, err := doJSON(client, http.MethodGet, acctBase+state, headers, nil)
		if err == nil {
			if parsed, ok := parseLoginAccount(acctRaw); ok {
				return parsed, nil
			}
			lastErr = fmt.Errorf("http %d: payload carried no uid", status)
			continue
		}
		lastErr = err
	}
}

// parseLoginAccount tolerates the account payload shapes seen across realms
// and gateway versions: flat {"uid":...}, numeric uid, and nested
// {"user":{...}} / {"account":{...}} wrappers. Previously a shape change
// silently produced an empty account (and via the bare-filename fallback, an
// invisible credential).
func parseLoginAccount(raw json.RawMessage) (accountData, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return accountData{}, false
	}
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		return accountData{}, false
	}
	if acct, ok := accountDataFromMap(flat); ok {
		return acct, true
	}
	for _, key := range []string{"user", "account", "info", "profile"} {
		nested, exists := flat[key]
		if !exists {
			continue
		}
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(nested, &inner); err != nil {
			continue
		}
		if acct, ok := accountDataFromMap(inner); ok {
			return acct, true
		}
	}
	return accountData{}, false
}

// accountDataFromMap extracts accountData tolerating string or numeric uid.
func accountDataFromMap(m map[string]json.RawMessage) (accountData, bool) {
	var acct accountData
	uid, ok := jsonStringish(m["uid"])
	if !ok || strings.TrimSpace(uid) == "" {
		return accountData{}, false
	}
	acct.UID = uid
	if v, ok := jsonStringish(m["enterpriseId"]); ok {
		acct.EnterpriseID = v
	}
	if v, ok := jsonStringish(m["nickname"]); ok {
		acct.Nickname = v
	}
	return acct, true
}

// jsonStringish decodes a JSON value that may be a string or a number into
// a string. Upstream gateways have been observed flipping uid between both.
func jsonStringish(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), true
	}
	return "", false
}

// uidFromTokenClaims is the last-resort identity fallback: decode the access
// token's JWT payload and return the first claim that looks like an account
// id. Returns "" when the token is not a JWT carrying any recognizable
// claim. Standard `sub` values that are URIs or emails are skipped — they
// are subject identifiers, not CodeBuddy account ids.
func uidFromTokenClaims(accessToken string) string {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	if pad := len(payload) % 4; pad != 0 {
		payload += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims map[string]json.RawMessage
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	for _, key := range []string{"uid", "user_id", "userId", "sub"} {
		v, ok := jsonStringish(claims[key])
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" || strings.Contains(v, "://") || strings.Contains(v, "@") || strings.Contains(v, " ") {
			continue
		}
		return v
	}
	return ""
}

func handleRefreshAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := parseStored(req.StorageJSON)
	if err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}
	// Route via host.http.do so request-log captures the refresh call (H2
	// compliance: was doJSON(sharedHTTPClient()) — bypassed host transport
	// policy + logging for the X-Refresh-Token endpoint).
	data, raw2, status, err := refreshCall(sa)
	if err != nil {
		if status >= 400 {
			return nil, fmt.Errorf("refresh rejected (HTTP %d)", status)
		}
		return nil, fmt.Errorf("refresh: %w", err)
	}
	_ = raw2
	var tok tokenData
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("refresh_failed: no accessToken in response — the refresh token may be expired; re-login required")
	}
	sa.Auth.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		sa.Auth.RefreshToken = tok.RefreshToken
	}
	if tok.Domain != "" {
		sa.Auth.Domain = tok.Domain
	}
	sa.Auth.ExpiresAt = preserveExpiry(
		time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second).Unix(),
		sa.Auth.ExpiresAt,
	)
	// No explicit host.auth.save here: the host's auth Manager persists the
	// refreshed credential itself after Refresh returns (conductor.go
	// refreshAuth → m.Update → persist). Writing from the plugin too would
	// double-write the file.
	return okEnvelope(pluginapi.AuthRefreshResponse{Auth: toAuthDataForRefresh(sa)})
}

// preserveExpiry reuses the previous token's expiresAt when the refresh
// response omits expiresIn (some CodeBuddy deployments return only the token
// pair). Zero would tell the host the credential is permanently expired and
// trigger a refresh storm on every request.
func preserveExpiry(newExpiry, oldExpiry int64) int64 {
	if newExpiry > 0 {
		return newExpiry
	}
	return oldExpiry
}

// toAuthDataForRefresh returns AuthData with FileName left EMPTY so the CPA
// host backfills the original auth.FileName (auth_provider.go:371).
//
// CPA uses FileName (relative to auth dir) as auth ID. If we set it to
// "workbuddy-<uid>.json" while the original file was "workbuddy.json"
// (legacy single-account name), the host treats it as a rename, writes a
// NEW file, and the old one stays → duplicate auth records.
//
// Returning empty FileName = "keep what you had" → no rename, no dup.
func toAuthDataForRefresh(sa *storedAuth) pluginapi.AuthData {
	ad := toAuthDataOpts(sa, nil, false)
	ad.FileName = "" // let host backfill original
	ad.ID = ""       // let host compute from path (prevents ID mismatch dupes)
	return ad
}
