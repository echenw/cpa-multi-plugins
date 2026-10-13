package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRequestLoginVariantDefaultsIntl(t *testing.T) {
	for _, raw := range []string{`{}`, ``, `{"State":"x"}`, `not-json`} {
		got, err := resolveRequestLoginVariant([]byte(raw))
		if err != nil {
			t.Fatalf("raw %q: unexpected error %v", raw, err)
		}
		if got != variantIntl {
			t.Fatalf("raw %q: got %q, want default %q", raw, got, variantIntl)
		}
	}
}

func TestRequestLoginVariantExplicitCN(t *testing.T) {
	for _, raw := range []string{
		`{"region":"trae-cn"}`,
		`{"login_region":"trae-cn"}`,
		`{"login_variant":"trae-cn"}`,
		`{"variant":"trae-cn"}`,
		`{"region":"cn"}`,
		`{"login_region":"cn"}`,
		`{"login_variant":"cn"}`,
		`{"variant":"cn"}`,
		`{"region":"CN"}`,
		`{"region":" trae-cn "}`,
	} {
		got, err := resolveRequestLoginVariant([]byte(raw))
		if err != nil {
			t.Fatalf("raw %q: unexpected error %v", raw, err)
		}
		if got != variantCN {
			t.Fatalf("raw %q: got %q, want %q", raw, got, variantCN)
		}
	}
}

func TestRequestLoginVariantExplicitIntl(t *testing.T) {
	for _, raw := range []string{
		`{"region":"trae"}`,
		`{"login_region":"trae"}`,
		`{"variant":"trae"}`,
		`{"region":"intl"}`,
		`{"login_region":"intl"}`,
		`{"variant":"intl"}`,
	} {
		got, err := resolveRequestLoginVariant([]byte(raw))
		if err != nil {
			t.Fatalf("raw %q: unexpected error %v", raw, err)
		}
		if got != variantIntl {
			t.Fatalf("raw %q: got %q, want %q", raw, got, variantIntl)
		}
	}
}

func TestRequestLoginVariantIllegalRejected(t *testing.T) {
	for _, raw := range []string{`{"region":"eu"}`, `{"login_region":"global"}`, `{"variant":"us"}`, `{"region":"solo"}`} {
		if _, err := resolveRequestLoginVariant([]byte(raw)); err == nil {
			t.Fatalf("raw %q: want explicit rejection, got nil", raw)
		} else if !strings.Contains(err.Error(), "invalid login region") {
			t.Fatalf("raw %q: error %q should name the invalid region", raw, err)
		}
	}
}

func TestHandleStartLoginRejectsIllegalRegion(t *testing.T) {
	if _, err := handleStartLogin([]byte(`{"region":"mars"}`)); err == nil {
		t.Fatal("handleStartLogin with illegal region should fail before any network I/O")
	} else if !strings.Contains(err.Error(), "invalid login region") {
		t.Fatalf("error %q should name the invalid region", err)
	}
}

func TestExistingAccountVariantsUnchanged(t *testing.T) {
	if got := sniffVariantFromJSON([]byte(`{"auth":{"uid":"u1"}}`)); got != variantCN {
		t.Fatalf("legacy cn payload sniffed as %q", got)
	}
	if got := sniffVariantFromJSON([]byte(`{"auth":{"variant":"intl","webId":"w"}}`)); got != variantIntl {
		t.Fatalf("intl payload sniffed as %q", got)
	}
	if got := sniffVariantFromJSON([]byte(`{"auth":{"webId":"w","tenant":"t"}}`)); got != variantIntl {
		t.Fatalf("intl marker payload sniffed as %q", got)
	}
	// Retired solo spellings fall back to the trae-cn route (safe-reader:
	// stored solo accounts keep serving instead of crashing).
	for _, s := range []string{"solo", " SOLO ", "solo-intl", "trae-solo", "trae-solo-cn"} {
		if got := normalizeVariant(s); got != variantCN {
			t.Fatalf("normalize %q = %q, want %q", s, got, variantCN)
		}
	}
	if got := sniffVariantFromJSON([]byte(`{"auth":{"variant":"solo"}}`)); got != variantCN {
		t.Fatalf("stored solo payload sniffed as %q, want %q", got, variantCN)
	}
}

func countInflightLogins() int {
	n := 0
	loginStates.Range(func(_, _ any) bool { n++; return true })
	intlloginStates.Range(func(_, _ any) bool { n++; return true })
	return n
}

func postPanelLoginStart(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	base := loadedManagementBasePath() + "/plugins/" + providerName
	req, err := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodPost, Path: base + "/login/start", Body: []byte(body)})
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

func stubLoginStart(t *testing.T) (called *bool, gotRealm *string) {
	t.Helper()
	orig := loginStartForRealm
	called = new(bool)
	gotRealm = new(string)
	loginStartForRealm = func(_ []byte, realm string) (panelLoginStartResult, error) {
		*called = true
		*gotRealm = realm
		return panelLoginStartResult{
			URL:       "https://auth.example/start?realm=" + realm,
			State:     "stub-state-" + realm,
			ExpiresAt: time.Now().Add(loginTTL).UTC(),
		}, nil
	}
	t.Cleanup(func() { loginStartForRealm = orig })
	return called, gotRealm
}

func TestPanelLoginStartDefaultsIntl(t *testing.T) {
	for _, body := range []string{``, `{}`, `{"State":"x"}`} {
		called, gotRealm := stubLoginStart(t)
		before := countInflightLogins()
		status, payload := postPanelLoginStart(t, body)
		if status != http.StatusOK {
			t.Fatalf("body %q: status=%d payload=%v, want 200", body, status, payload)
		}
		if !*called || *gotRealm != variantIntl {
			t.Fatalf("body %q: start called=%v realm=%q, want trae", body, *called, *gotRealm)
		}
		if payload["realm"] != variantIntl {
			t.Fatalf("body %q: realm=%v, want trae", body, payload["realm"])
		}
		if payload["auth_url"] != "https://auth.example/start?realm="+variantIntl {
			t.Fatalf("body %q: auth_url=%v", body, payload["auth_url"])
		}
		if payload["state"] != "stub-state-"+variantIntl {
			t.Fatalf("body %q: state=%v, want the poll key for the modal", body, payload["state"])
		}
		if _, ok := payload["expires_at"]; !ok {
			t.Fatalf("body %q: payload=%v, want expires_at for the modal", body, payload)
		}
		if got := countInflightLogins(); got != before {
			t.Fatalf("body %q: stubbed start leaked %d login state(s)", body, got-before)
		}
	}
}

func TestPanelLoginStartCNRespected(t *testing.T) {
	for _, body := range []string{`{"region":"trae-cn"}`, `{"variant":"trae-cn"}`, `{"login_region":"trae-cn"}`, `{"region":"cn"}`, `{"variant":"cn"}`} {
		called, gotRealm := stubLoginStart(t)
		status, payload := postPanelLoginStart(t, body)
		if status != http.StatusOK {
			t.Fatalf("body %q: status=%d payload=%v, want 200", body, status, payload)
		}
		if !*called || *gotRealm != variantCN {
			t.Fatalf("body %q: start called=%v realm=%q, want trae-cn", body, *called, *gotRealm)
		}
		if payload["realm"] != variantCN {
			t.Fatalf("body %q: realm=%v, want trae-cn", body, payload["realm"])
		}
	}
}

func TestPanelLoginStartInvalid400NothingStarted(t *testing.T) {
	called, _ := stubLoginStart(t)
	before := countInflightLogins()
	status, payload := postPanelLoginStart(t, `{"region":"mars"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d payload=%v, want 400", status, payload)
	}
	if _, ok := payload["error"]; !ok {
		t.Fatalf("payload=%v, want error key", payload)
	}
	if *called {
		t.Fatal("invalid region must not start a login")
	}
	if got := countInflightLogins(); got != before {
		t.Fatalf("inflight logins moved %d -> %d on rejection", before, got)
	}
}
