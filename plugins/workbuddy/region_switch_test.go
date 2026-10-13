package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Config-file region switching is deleted: there is no stored selection to
// pin in tests. Login realm comes from the request only (default intl).

func decodeWBLoginState(t *testing.T, out []byte) string {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope err=%v ok=%v raw=%s", err, env.OK, out)
	}
	var start pluginapi.AuthLoginStartResponse
	if err := json.Unmarshal(env.Result, &start); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	return start.State
}

func TestRequestLoginRegionDefaultsIntl(t *testing.T) {
	_, platforms := withLoginEndpointsServer(t)

	out, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin default: %v", err)
	}
	state := decodeWBLoginState(t, out)
	v, ok := loginStates.Load(state)
	if !ok {
		t.Fatal("login state not stored")
	}
	defer loginStates.Delete(state)
	if lc := v.(*loginCtx); lc.region != regionIntl {
		t.Fatalf("login-state region = %q, want intl", lc.region)
	}
	if len(*platforms) != 1 || (*platforms)[0] != "ide" {
		t.Fatalf("platforms = %v, want [ide] for the intl entry", *platforms)
	}
}

func TestRequestLoginRegionExplicitCN(t *testing.T) {
	_, platforms := withLoginEndpointsServer(t)

	for _, raw := range []string{`{"region":"cn"}`, `{"login_region":"cn"}`, `{"variant":"cn"}`} {
		out, err := handleStartLogin([]byte(raw))
		if err != nil {
			t.Fatalf("handleStartLogin %s: %v", raw, err)
		}
		state := decodeWBLoginState(t, out)
		v, ok := loginStates.Load(state)
		if !ok {
			t.Fatal("login state not stored")
		}
		loginStates.Delete(state)
		if lc := v.(*loginCtx); lc.region != regionCN {
			t.Fatalf("raw %s: login-state region = %q, want cn", raw, lc.region)
		}
	}
	if len(*platforms) == 0 || (*platforms)[0] != "CLI" {
		t.Fatalf("platforms = %v, want [CLI] for the cn entry", *platforms)
	}
}

func TestRequestLoginRegionExplicitIntl(t *testing.T) {
	_, _ = withLoginEndpointsServer(t)

	for _, raw := range []string{`{"region":"intl"}`, `{"login_region":"intl"}`, `{"variant":"intl"}`, `{}`} {
		out, err := handleStartLogin([]byte(raw))
		if err != nil {
			t.Fatalf("handleStartLogin %s: %v", raw, err)
		}
		state := decodeWBLoginState(t, out)
		v, ok := loginStates.Load(state)
		if !ok {
			t.Fatal("login state not stored")
		}
		loginStates.Delete(state)
		if lc := v.(*loginCtx); lc.region != regionIntl {
			t.Fatalf("raw %s: login-state region = %q, want intl", raw, lc.region)
		}
	}
}

func TestRequestLoginRegionIllegalRejected(t *testing.T) {
	for _, raw := range []string{`{"region":"eu"}`, `{"login_region":"global"}`, `{"variant":"us"}`} {
		if _, err := handleStartLogin([]byte(raw)); err == nil {
			t.Fatalf("raw %q: want explicit rejection, got nil", raw)
		} else if !strings.Contains(err.Error(), "invalid login region") {
			t.Fatalf("raw %q: error %q should name the invalid region", raw, err)
		}
	}
	if _, err := startLoginWithRegion(nil, "eu"); err == nil {
		t.Fatal("startLoginWithRegion with illegal region should fail")
	}
}

func TestExistingAccountRealmsUnchanged(t *testing.T) {
	if got := realmForStorage([]byte(`{"auth":{"domain":"copilot.tencent.com"}}`), ""); got != "cn" {
		t.Fatalf("cn account realm = %q", got)
	}
	if got := realmForStorage([]byte(`{"auth":{"region":"intl"}}`), ""); got != "intl" {
		t.Fatalf("intl account realm = %q", got)
	}
	if got := realmForStorage([]byte(`{"auth":{"region":"global"}}`), ""); got != "global" {
		t.Fatalf("global account realm = %q", got)
	}
}

// resetMgmtRateLimit clears the shared plugin-layer bucket so back-to-back
// POST endpoint tests never see a 429 from earlier tests.
func resetMgmtRateLimit(t *testing.T) {
	t.Helper()
	mgmtRateLimitMu.Lock()
	mgmtRateLimit = map[string]*mgmtRateEntry{}
	mgmtRateLimitMu.Unlock()
}

func countLoginStates() int {
	n := 0
	loginStates.Range(func(_, _ any) bool { n++; return true })
	return n
}

func snapshotLoginStates() map[any]bool {
	seen := map[any]bool{}
	loginStates.Range(func(k, _ any) bool { seen[k] = true; return true })
	return seen
}

// drainNewLoginStates deletes only the states created after the snapshot so
// no login session leaks between tests (and other tests' states are kept).
func drainNewLoginStates(before map[any]bool) {
	loginStates.Range(func(k, _ any) bool {
		if !before[k] {
			loginStates.Delete(k)
		}
		return true
	})
}

func callLoginStartEndpoint(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	resetMgmtRateLimit(t)
	base := loadedManagementBasePath() + "/plugins/" + providerName
	req, err := json.Marshal(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   base + "/login/start",
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

func TestManagementLoginStartDefaultIntl(t *testing.T) {
	_, _ = withLoginEndpointsServer(t)

	for _, body := range []string{`{}`, ``} {
		before := snapshotLoginStates()
		status, payload := callLoginStartEndpoint(t, body)
		if status != http.StatusOK {
			t.Fatalf("body %q: status = %d, payload=%v", body, status, payload)
		}
		if payload["region"] != regionIntl {
			t.Fatalf("body %q: region = %v, want intl", body, payload["region"])
		}
		url, _ := payload["auth_url"].(string)
		if url == "" {
			t.Fatalf("body %q: empty auth_url in %v", body, payload)
		}
		drainNewLoginStates(before)
		if got := countLoginStates(); got != len(before) {
			t.Fatalf("body %q: leaked %d login state(s)", body, got-len(before))
		}
	}
}

func TestManagementLoginStartCN(t *testing.T) {
	_, _ = withLoginEndpointsServer(t)

	before := snapshotLoginStates()
	status, payload := callLoginStartEndpoint(t, `{"region":"cn"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, payload=%v", status, payload)
	}
	if payload["region"] != regionCN {
		t.Fatalf("region = %v, want cn", payload["region"])
	}
	if url, _ := payload["auth_url"].(string); url == "" {
		t.Fatalf("empty auth_url in %v", payload)
	}
	drainNewLoginStates(before)
}

func TestManagementLoginStartInvalid(t *testing.T) {
	_, _ = withLoginEndpointsServer(t)

	before := countLoginStates()
	for _, body := range []string{`{"region":"eu"}`, `{"login_region":"global"}`, `{"variant":"us"}`} {
		status, payload := callLoginStartEndpoint(t, body)
		if status != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400 (payload=%v)", body, status, payload)
		}
		if _, ok := payload["error"]; !ok {
			t.Fatalf("body %q: no error in %v", body, payload)
		}
	}
	if got := countLoginStates(); got != before {
		t.Fatalf("invalid regions persisted %d login state(s); want none", got-before)
	}
}
