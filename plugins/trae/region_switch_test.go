package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRequestLoginVariantDefaultsIntl(t *testing.T) {
	for _, raw := range []string{`{}`, ``, `{"State":"x"}`, `not-json`} {
		got, err := resolveRequestLoginVariant([]byte(raw), "")
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
		`{"region":"cn"}`,
		`{"login_region":"cn"}`,
		`{"login_variant":"cn"}`,
		`{"variant":"cn"}`,
		`{"region":"CN"}`,
		`{"region":" cn "}`,
	} {
		got, err := resolveRequestLoginVariant([]byte(raw), variantIntl)
		if err != nil {
			t.Fatalf("raw %q: unexpected error %v", raw, err)
		}
		if got != variantCN {
			t.Fatalf("raw %q: got %q, want %q", raw, got, variantCN)
		}
	}
}

func TestRequestLoginVariantStickyFallback(t *testing.T) {
	for _, fb := range []string{variantCN, variantSolo, variantIntl} {
		got, err := resolveRequestLoginVariant([]byte(`{}`), fb)
		if err != nil {
			t.Fatalf("fallback %q: unexpected error %v", fb, err)
		}
		if got != fb {
			t.Fatalf("fallback %q: got %q, want sticky fallback", fb, got)
		}
	}
}

func TestRequestLoginVariantIllegalRejected(t *testing.T) {
	before := loadedLoginVariant()
	for _, raw := range []string{`{"region":"eu"}`, `{"login_region":"global"}`, `{"variant":"us"}`} {
		if _, err := resolveRequestLoginVariant([]byte(raw), variantIntl); err == nil {
			t.Fatalf("raw %q: want explicit rejection, got nil", raw)
		} else if !strings.Contains(err.Error(), "invalid login region") {
			t.Fatalf("raw %q: error %q should name the invalid region", raw, err)
		}
		if got := loadedLoginVariant(); got != before {
			t.Fatalf("raw %q: stored selection moved %q -> %q on rejection", raw, before, got)
		}
	}
}

func TestHandleStartLoginRejectsIllegalRegion(t *testing.T) {
	before := loadedLoginVariant()
	if _, err := handleStartLogin([]byte(`{"region":"mars"}`)); err == nil {
		t.Fatal("handleStartLogin with illegal region should fail before any network I/O")
	} else if !strings.Contains(err.Error(), "invalid login region") {
		t.Fatalf("error %q should name the invalid region", err)
	}
	if got := loadedLoginVariant(); got != before {
		t.Fatalf("stored selection moved %q -> %q on rejection", before, got)
	}
}

func TestLoginRegionSelectorPayload(t *testing.T) {
	sel := loginRegionSelectorPayload()
	if len(sel.Options) != 2 || sel.Options[0] != variantCN || sel.Options[1] != variantIntl {
		t.Fatalf("options = %v, want [cn intl]", sel.Options)
	}
	if sel.Default != variantIntl {
		t.Fatalf("default = %q, want intl", sel.Default)
	}
	if sel.Current != normalizeVariant(loadedLoginVariant()) {
		t.Fatalf("current = %q, want sticky %q", sel.Current, loadedLoginVariant())
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
	if got := normalizeVariant("solo"); got != variantSolo {
		t.Fatalf("normalize solo = %q", got)
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
