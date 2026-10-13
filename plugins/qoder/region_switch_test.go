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

func TestRequestLoginRegionDefaultsIntl(t *testing.T) {
	old := loadedLoginRegion()
	t.Cleanup(func() { setLoginRegion(old) })
	setLoginRegion(regionIntl)

	out, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin default: %v", err)
	}
	url, state, md := decodeLoginStart(t, out)
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
	if md["region"] != regionIntl || md["region_default"] != regionIntl {
		t.Fatalf("metadata = %v, want region/region_default intl", md)
	}
	if opts, ok := md["region_options"].([]any); !ok || len(opts) != 2 {
		t.Fatalf("metadata region_options = %v, want [cn intl]", md["region_options"])
	}
}

func TestRequestLoginRegionExplicitCN(t *testing.T) {
	old := loadedLoginRegion()
	t.Cleanup(func() { setLoginRegion(old) })
	setLoginRegion(regionIntl)

	out, err := handleStartLogin([]byte(`{"login_region":"cn"}`))
	if err != nil {
		t.Fatalf("handleStartLogin cn: %v", err)
	}
	url, state, md := decodeLoginStart(t, out)
	if !strings.Contains(url, "https://qoder.cn/") {
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
	if md["region"] != regionCN {
		t.Fatalf("metadata region = %v, want cn", md["region"])
	}
	if loadedLoginRegion() != regionIntl {
		t.Fatalf("request cn must not rewrite the sticky selection (got %q)", loadedLoginRegion())
	}
}

func TestRequestLoginRegionIllegalRejected(t *testing.T) {
	old := loadedLoginRegion()
	t.Cleanup(func() { setLoginRegion(old) })
	setLoginRegion(regionIntl)

	for _, raw := range []string{`{"region":"eu"}`, `{"login_region":"global"}`, `{"variant":"us"}`} {
		if _, err := handleStartLogin([]byte(raw)); err == nil {
			t.Fatalf("raw %q: want explicit rejection, got nil", raw)
		} else if !strings.Contains(err.Error(), "invalid login region") {
			t.Fatalf("raw %q: error %q should name the invalid region", raw, err)
		}
		if got := loadedLoginRegion(); got != regionIntl {
			t.Fatalf("raw %q: stored selection moved to %q on rejection", raw, got)
		}
	}
	if _, err := startLoginWithRegion(nil, "eu"); err == nil {
		t.Fatal("startLoginWithRegion with illegal region should fail")
	}
}

func TestLoginRegionSelectorPayload(t *testing.T) {
	sel := loginRegionSelectorPayload()
	if len(sel.Options) != 2 || sel.Options[0] != regionCN || sel.Options[1] != regionIntl {
		t.Fatalf("options = %v, want [cn intl]", sel.Options)
	}
	if sel.Default != regionIntl {
		t.Fatalf("default = %q, want intl", sel.Default)
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

func TestManagementLoginRegionEndpoint(t *testing.T) {
	base := loadedManagementBasePath() + "/plugins/" + providerName
	req, err := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: base + "/login/region"})
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, resp.Body)
	}
	var sel loginRegionSelector
	if err := json.Unmarshal(resp.Body, &sel); err != nil {
		t.Fatalf("decode selector: %v (body=%s)", err, resp.Body)
	}
	if len(sel.Options) != 2 || sel.Options[0] != "cn" || sel.Options[1] != "intl" {
		t.Fatalf("options = %v, want [cn intl]", sel.Options)
	}
	if sel.Default != "intl" {
		t.Fatalf("default = %q, want intl", sel.Default)
	}
}
