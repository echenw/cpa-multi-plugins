package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func setLoginRegionForTest(t *testing.T, region string) {
	t.Helper()
	loginRegionMu.Lock()
	old := loginRegion
	loginRegion = region
	loginRegionMu.Unlock()
	t.Cleanup(func() {
		loginRegionMu.Lock()
		loginRegion = old
		loginRegionMu.Unlock()
	})
}

func decodeWBLoginStart(t *testing.T, out []byte) (state string, md map[string]any) {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		t.Fatalf("envelope err=%v ok=%v raw=%s", err, env.OK, out)
	}
	var start pluginapi.AuthLoginStartResponse
	if err := json.Unmarshal(env.Result, &start); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	return start.State, start.Metadata
}

func TestRequestLoginRegionDefaultsIntl(t *testing.T) {
	setLoginRegionForTest(t, regionIntl)
	_, platforms := withLoginEndpointsServer(t)

	out, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin default: %v", err)
	}
	state, md := decodeWBLoginStart(t, out)
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
	if len(*platforms) != 1 || (*platforms)[0] != "ide" {
		t.Fatalf("platforms = %v, want [ide] for the intl entry", *platforms)
	}
}

func TestRequestLoginRegionExplicitCN(t *testing.T) {
	setLoginRegionForTest(t, regionIntl)
	_, platforms := withLoginEndpointsServer(t)

	out, err := handleStartLogin([]byte(`{"region":"cn"}`))
	if err != nil {
		t.Fatalf("handleStartLogin cn: %v", err)
	}
	state, md := decodeWBLoginStart(t, out)
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
	if len(*platforms) != 1 || (*platforms)[0] != "CLI" {
		t.Fatalf("platforms = %v, want [CLI] for the cn entry", *platforms)
	}
	if got := loadedLoginRegion(); got != regionIntl {
		t.Fatalf("request cn must not rewrite the sticky selection (got %q)", got)
	}
}

func TestRequestLoginRegionIllegalRejected(t *testing.T) {
	setLoginRegionForTest(t, regionIntl)
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
	setLoginRegionForTest(t, regionIntl)
	sel := loginRegionSelectorPayload()
	if len(sel.Options) != 2 || sel.Options[0] != regionCN || sel.Options[1] != regionIntl {
		t.Fatalf("options = %v, want [cn intl]", sel.Options)
	}
	if sel.Default != regionIntl {
		t.Fatalf("default = %q, want intl", sel.Default)
	}
	if sel.Current != regionIntl {
		t.Fatalf("current = %q, want sticky intl", sel.Current)
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
