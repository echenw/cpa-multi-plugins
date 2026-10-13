package main

import (
	"net/http"
	"strings"
	"testing"
)

// Hermetic proofs for the retired-realm removal (codebuddy.ai realm deleted;
// remaining realms: cn + global):
//
// (a) a request carrying the retired region is an explicit 400 and stores
// nothing;
// (b) the login default is global;
// (c) an already-stored retired-realm account keeps listing (verbatim badge,
// routing never crashes, family ownership still claims its files);
// (d) the panel carries no retired-realm pill/toggle pill and the Global
// toggle defaults on.
//
// NOTE: this is the one test file allowed to name the retired "intl" value
// (safe-reader fallback coverage) — see the task report.

func TestRetiredRegionRequestRejected400(t *testing.T) {
	for _, raw := range []string{`{"region":"intl"}`, `{"login_region":"intl"}`, `{"variant":"intl"}`} {
		if _, _, err := parseLoginRegionField([]byte(raw)); err == nil {
			t.Fatalf("raw %q: want explicit rejection, got nil", raw)
		} else if !strings.Contains(err.Error(), "invalid login region") {
			t.Fatalf("raw %q: error %q should name the invalid region", raw, err)
		}
		if _, err := handleStartLogin([]byte(raw)); err == nil {
			t.Fatalf("handleStartLogin %q: want explicit rejection, got nil", raw)
		}
	}

	before := countLoginStates()
	for _, body := range []string{`{"region":"intl"}`, `{"login_region":"intl"}`} {
		status, payload := callLoginStartEndpoint(t, body)
		if status != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400 (payload=%v)", body, status, payload)
		}
		if _, ok := payload["error"]; !ok {
			t.Fatalf("body %q: no error in %v", body, payload)
		}
	}
	if got := countLoginStates(); got != before {
		t.Fatalf("retired region persisted %d login state(s); want none", got-before)
	}
}

func TestRetiredRemovalDefaultIsGlobal(t *testing.T) {
	if defaultRequestLoginRegion != regionGlobal {
		t.Fatalf("defaultRequestLoginRegion = %q, want %q", defaultRequestLoginRegion, regionGlobal)
	}
	got, err := resolveRequestLoginRegion([]byte(`{}`))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != regionGlobal {
		t.Fatalf("empty request resolves %q, want %q", got, regionGlobal)
	}
}

func TestRetiredStoredAccountStillLists(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "t", Domain: "codebuddy.ai", Region: "intl"},
		Account: storedAccount{UID: "u1", Nickname: "legacy"},
	}
	// Display keeps the stored value verbatim (badge text, 全部 filter).
	if got := panelRegion(sa); got != "intl" {
		t.Fatalf("panelRegion(stored intl) = %q, want verbatim %q", got, "intl")
	}
	// Routing never crashes: unknown values fall back to the cn path.
	if got := accountRegion(sa); got != "cn" {
		t.Fatalf("accountRegion(stored intl) = %q, want cn fallback", got)
	}
	if got := billingBaseFor(sa); got != billingBase {
		t.Fatalf("billingBaseFor(stored intl) = %q, want cn fallback %q", got, billingBase)
	}
	if got := upstreamBaseFor(sa); got != upstreamBaseCN {
		t.Fatalf("upstreamBaseFor(stored intl) = %q, want cn fallback %q", got, upstreamBaseCN)
	}
	if got := originRefererFor(sa); got != originReferer {
		t.Fatalf("originRefererFor(stored intl) = %q, want cn fallback %q", got, originReferer)
	}
	// Family ownership still claims the retired-realm files and types, so
	// the accounts keep listing instead of vanishing.
	for _, name := range []string{"workbuddy-intl-u1.json", "codebuddy-intl-u1.json", "codebuddy-intl.json"} {
		if !isOurFamilyFileName(name) {
			t.Errorf("isOurFamilyFileName(%q) = false, want true (stored account must keep listing)", name)
		}
	}
	for _, typ := range []string{"workbuddy-intl", "codebuddy-intl"} {
		if !isOurDeclaredType(typ) {
			t.Errorf("isOurDeclaredType(%q) = false, want true", typ)
		}
		if foreign, _ := foreignAuthOwner(typ, ""); foreign {
			t.Errorf("foreignAuthOwner(%q) = foreign, want family", typ)
		}
	}
	// Legacy note prefixes from older builds never resurrect as credit data.
	if got := creditSegmentFromNote("INTL · 余10 已用2"); got != "余10 已用2" {
		t.Fatalf("creditSegmentFromNote legacy prefix = %q, want stripped credits", got)
	}
}

func TestRetiredPanelHasNoRealmPill(t *testing.T) {
	html := string(panelHTML)
	for _, pill := range []string{`data-region="intl"`, `data-login-region="intl"`, `data-login-modal-region="intl"`, `id="cntIntl"`} {
		if strings.Contains(html, pill) {
			t.Errorf("panel still carries retired-realm markup %q", pill)
		}
	}
	if !strings.Contains(html, `class="lreg on" data-login-region="global"`) {
		t.Error("panel Global login toggle must default on")
	}
	if !strings.Contains(html, `class="lreg on" data-login-modal-region="global"`) {
		t.Error("panel modal Global toggle must default on")
	}
	if !strings.Contains(html, `let loginRegion="global"`) {
		t.Error("panel loginRegion JS default must be global")
	}
}
