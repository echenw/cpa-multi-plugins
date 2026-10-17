package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func decodeLoginStart(t *testing.T, out []byte) (url, state string, md map[string]any) {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope err=%v ok=%v raw=%s", err, env.OK, out)
	}
	var start pluginapi.AuthLoginStartResponse
	if err := json.Unmarshal(env.Result, &start); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	return start.URL, start.State, start.Metadata
}

func snapshotLoginStates(t *testing.T) map[string]struct{} {
	t.Helper()
	m := map[string]struct{}{}
	loginStates.Range(func(k, _ any) bool {
		m[k.(string)] = struct{}{}
		return true
	})
	return m
}

func deleteNewLoginStates(before map[string]struct{}) {
	loginStates.Range(func(k, _ any) bool {
		if _, ok := before[k.(string)]; !ok {
			loginStates.Delete(k)
		}
		return true
	})
}

func countLoginStates() int {
	n := 0
	loginStates.Range(func(_, _ any) bool { n++; return true })
	return n
}

func callPanelLoginStart(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
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
	var payload map[string]any
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("decode payload: %v (body=%s)", err, resp.Body)
	}
	return resp.StatusCode, payload
}

func TestRequestLoginRegionDefaultsIntl(t *testing.T) {
	out, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin default: %v", err)
	}
	url, state, _ := decodeLoginStart(t, out)
	if !strings.Contains(url, "https://qoder.com/") {
		t.Fatalf("default login URL = %q, want the intl realm", url)
	}
	v, ok := loginStates.Load(state)
	if !ok {
		t.Fatal("login state not stored")
	}
	defer loginStates.Delete(state)
	if lc := v.(*loginCtx); lc.region != regionIntl {
		t.Fatalf("login-state region = %q, want intl", lc.region)
	}
}

func TestRequestLoginRegionExplicitCN(t *testing.T) {
	out, err := handleStartLogin([]byte(`{"login_region":"cn"}`))
	if err != nil {
		t.Fatalf("handleStartLogin cn: %v", err)
	}
	url, state, _ := decodeLoginStart(t, out)
	if !strings.Contains(url, "https://qoder.com.cn/") {
		t.Fatalf("cn login URL = %q, want the cn realm", url)
	}
	v, ok := loginStates.Load(state)
	if !ok {
		t.Fatal("login state not stored")
	}
	defer loginStates.Delete(state)
	if lc := v.(*loginCtx); lc.region != regionCN {
		t.Fatalf("login-state region = %q, want cn", lc.region)
	}
}

func TestRequestLoginRegionVariantKeys(t *testing.T) {
	for _, raw := range []string{`{"region":"cn"}`, `{"variant":"cn"}`, `{"REGION":"cn"}`} {
		out, err := handleStartLogin([]byte(raw))
		if err != nil {
			t.Fatalf("raw %q: %v", raw, err)
		}
		_, state, _ := decodeLoginStart(t, out)
		v, ok := loginStates.Load(state)
		if !ok {
			t.Fatalf("raw %q: login state not stored", raw)
		}
		func() {
			defer loginStates.Delete(state)
			if lc := v.(*loginCtx); lc.region != regionCN {
				t.Fatalf("raw %q: login-state region = %q, want cn", raw, lc.region)
			}
		}()
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

func TestLoginRegionConfigFileIgnored(t *testing.T) {
	wire := func(cfg string) []byte {
		b, _ := json.Marshal(struct {
			ConfigYAML []byte `json:"config_yaml"`
		}{ConfigYAML: []byte(cfg)})
		return b
	}
	// A config-file region key must not move the login default: absent
	// request region still resolves to intl.
	configure(wire("enabled: true\nlogin_region: cn\npriority: 0\n"))
	out, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin after config: %v", err)
	}
	_, state, _ := decodeLoginStart(t, out)
	v, ok := loginStates.Load(state)
	if !ok {
		t.Fatal("login state not stored")
	}
	defer loginStates.Delete(state)
	if lc := v.(*loginCtx); lc.region != regionIntl {
		t.Fatalf("config login_region moved the default to %q, want intl", lc.region)
	}
}

func TestExistingAccountRegionsUnchanged(t *testing.T) {
	cn := &storedAuth{}
	cn.Auth.Region = ""
	cn.Auth.Domain = "openapi.qoder.com.cn"
	if got := authRegion(cn); got != regionCN {
		t.Fatalf("cn account routed to %q", got)
	}
	intl := &storedAuth{}
	intl.Auth.Region = regionIntl
	if got := authRegion(intl); got != regionIntl {
		t.Fatalf("intl account routed to %q", got)
	}
	if normalizeRegion("global") != regionIntl || normalizeRegion("bogus") != regionCN {
		t.Fatal("normalizeRegion semantics changed")
	}
}

func TestPanelLoginStartDefaultsIntl(t *testing.T) {
	before := snapshotLoginStates(t)
	t.Cleanup(func() { deleteNewLoginStates(before) })

	for _, body := range []string{`{}`, ``} {
		status, payload := callPanelLoginStart(t, body)
		if status != http.StatusOK {
			t.Fatalf("body %q: status = %d, payload=%v", body, status, payload)
		}
		if payload["region"] != regionIntl {
			t.Fatalf("body %q: region = %v, want intl", body, payload["region"])
		}
		authURL, _ := payload["auth_url"].(string)
		if !strings.Contains(authURL, "https://qoder.com/") {
			t.Fatalf("body %q: auth_url = %q, want the intl realm", body, authURL)
		}
	}
}

func TestPanelLoginStartExplicitCN(t *testing.T) {
	before := snapshotLoginStates(t)
	t.Cleanup(func() { deleteNewLoginStates(before) })

	status, payload := callPanelLoginStart(t, `{"region":"cn"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, payload=%v", status, payload)
	}
	if payload["region"] != regionCN {
		t.Fatalf("region = %v, want cn", payload["region"])
	}
	authURL, _ := payload["auth_url"].(string)
	if !strings.Contains(authURL, "https://qoder.com.cn/") {
		t.Fatalf("auth_url = %q, want the cn realm", authURL)
	}
}

func TestPanelLoginStartInvalidRejected(t *testing.T) {
	for _, body := range []string{`{"region":"eu"}`, `{"region":"global"}`, `{"variant":"us"}`} {
		nBefore := countLoginStates()
		status, payload := callPanelLoginStart(t, body)
		if status != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400 (payload=%v)", body, status, payload)
		}
		if _, ok := payload["auth_url"]; ok {
			t.Fatalf("body %q: invalid region must not return an auth_url", body)
		}
		if got := countLoginStates(); got != nBefore {
			deleteNewLoginStates(nil)
			t.Fatalf("body %q: login state stored on rejection (%d -> %d)", body, nBefore, got)
		}
	}
}
