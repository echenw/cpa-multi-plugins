package main

import (
	"strings"
	"testing"

	cnupstream "github.com/mmqz/cpa-multi-plugins/plugins/trae/upstream"
)

// region_rename_test.go — hermetic pins for the B-plan region rename
// (cn → trae-cn, intl → trae with bare = international) and the retired
// solo safe-reader fallback. No network, no host RPC.

// TestRegionRenameMapping locks the identity-string table: canonical slugs,
// legacy pre-B-plan spellings, and every retired solo spelling.
func TestRegionRenameMapping(t *testing.T) {
	cases := []struct{ in, want string }{
		{"trae-cn", variantCN},
		{"trae", variantIntl},
		{"cn", variantCN},     // legacy pre-B-plan
		{"intl", variantIntl}, // legacy pre-B-plan
		{"trae-intl", variantIntl},
		{" CN ", variantCN}, // tolerant of case/whitespace
		{"", variantCN},     // default region
		{"garbage", variantCN},
		// Retired solo spellings: never produced, always routable.
		{"solo", variantCN},
		{" SOLO ", variantCN},
		{"solo-intl", variantCN},
		{"trae-solo", variantCN},
		{"trae-solo-cn", variantCN},
	}
	for _, tc := range cases {
		if got := normalizeVariant(tc.in); got != tc.want {
			t.Errorf("normalizeVariant(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if variantCN != "trae-cn" {
		t.Errorf("variantCN = %q, want hyphenated trae-cn", variantCN)
	}
	if variantIntl != "trae" {
		t.Errorf("variantIntl = %q, want bare trae", variantIntl)
	}
}

// TestRegionOAuthIdentityConstant locks the post-solo login identity: one
// auth_from, one platform code, SaaS login never hidden — no solo branch.
func TestRegionOAuthIdentityConstant(t *testing.T) {
	for _, v := range []string{"trae-cn", "trae", "cn", "intl", "solo", ""} {
		if got := oauthAuthFor(v); got != "trae" {
			t.Errorf("oauthAuthFor(%q) = %q, want trae", v, got)
		}
		if got := oauthPlatformCodeFor(v); got != "IDE_PC" {
			t.Errorf("oauthPlatformCodeFor(%q) = %q, want IDE_PC", v, got)
		}
		if oauthHideSaasLoginFor(v) {
			t.Errorf("oauthHideSaasLoginFor(%q) = true, want false", v)
		}
	}
}

// TestSoloSafeReaderEndToEnd pins the whole stored-solo journey: the file
// name is still claimed, the content sniffs onto the trae-cn route, display
// stays verbatim, and nothing crashes.
func TestSoloSafeReaderEndToEnd(t *testing.T) {
	name := "trae-solo-cn-u9.json"
	if !isOurFamilyFileName(name) {
		t.Fatalf("%q must stay claimable by filename", name)
	}
	if !isOurDeclaredType("trae-solo-cn") {
		t.Fatalf("declared type trae-solo-cn must stay in the family")
	}
	raw := []byte(`{"type":"trae-solo-cn","provider":"trae-solo-cn","auth":{"accessToken":"a","variant":"solo"},"account":{"uid":"u9"}}`)
	if got := sniffVariantFromJSON(raw); got != variantCN {
		t.Fatalf("stored solo sniffs as %q, want trae-cn route", got)
	}
	if got := authNote("solo"); got != "SOLO" {
		t.Fatalf("stored solo note = %q, want verbatim SOLO", got)
	}
	if got := variantLabel("solo"); got != "Trae SOLO CN" {
		t.Fatalf("stored solo label = %q, want verbatim Trae SOLO CN", got)
	}
	// A stored solo account is not intl-routed.
	if isIntlStoredAuth(&storedAuth{Variant: "solo"}) {
		t.Fatal("stored solo must not route to the trae pay face")
	}
}

// TestRegionExclusionSubKeys pins the B-plan sub-key table used by both the
// Go filter call sites and the panel picker: trae-cn for the CN lane, trae
// (== provider key, idempotent) for the trae lane, and no trae-solo.
func TestRegionExclusionSubKeys(t *testing.T) {
	if variantCN != "trae-cn" {
		t.Fatalf("CN sub-key = %q, want trae-cn", variantCN)
	}
	if variantIntl != providerName {
		t.Fatalf("trae sub-key = %q, want the bare provider %q", variantIntl, providerName)
	}
}

// TestSameSnapshotRealm locks the tolerant snapshot guard: canonical match,
// legacy pre-B-plan realms readable, retired solo realm never serves.
func TestSameSnapshotRealm(t *testing.T) {
	cases := []struct {
		stored, asking string
		want           bool
	}{
		{"trae-cn", "trae-cn", true},
		{"trae", "trae", true},
		{"cn", "trae-cn", true},
		{"intl", "trae", true},
		{"cn", "trae", false},
		{"intl", "trae-cn", false},
		{"trae-cn", "trae", false},
		{"solo", "trae-cn", false},
		{"solo", "trae", false},
		{"", "trae-cn", false},
	}
	for _, tc := range cases {
		if got := sameSnapshotRealm(tc.stored, tc.asking); got != tc.want {
			t.Errorf("sameSnapshotRealm(%q,%q) = %v, want %v", tc.stored, tc.asking, got, tc.want)
		}
	}
}

// TestUpstreamCanonicalRegionMaps locks the upstream lineage table under
// B-plan keys: canonical + legacy spellings resolve, and the retired solo
// spellings keep their SOLO lineage (no cross-lineage poisoning).
func TestUpstreamCanonicalRegionMaps(t *testing.T) {
	if got := cnupstream.ClientIDFor("trae-cn"); got != "ono9krqynydwx5" {
		t.Errorf("ClientIDFor(trae-cn) = %q", got)
	}
	if got := cnupstream.ClientIDFor("trae"); got != "ono9krqynydwx5" {
		t.Errorf("ClientIDFor(trae) = %q", got)
	}
	if got := cnupstream.ClientIDFor("solo"); got != "en1oxy7wnw8j9n" {
		t.Errorf("ClientIDFor(solo) = %q, want the solo lineage (stored files)", got)
	}
	if got := cnupstream.FunctionFor("trae-cn"); got != "solo_work_lite" {
		t.Errorf("FunctionFor(trae-cn) = %q", got)
	}
	if got := cnupstream.FunctionFor("trae"); got != "solo_work_lite" {
		t.Errorf("FunctionFor(trae) = %q", got)
	}
	if got := cnupstream.PlatformIDFor("trae-cn"); got != "trae_cn" {
		t.Errorf("PlatformIDFor(trae-cn) = %q", got)
	}
	if got := cnupstream.PlatformIDFor("trae"); got != "trae" {
		t.Errorf("PlatformIDFor(trae) = %q", got)
	}
	if got := cnupstream.PlatformNameFor("solo"); got != "TRAE SOLO CN" {
		t.Errorf("PlatformNameFor(solo) = %q, want verbatim lineage", got)
	}
	for _, v := range []string{"trae", "intl", "trae-intl"} {
		if !cnupstream.IsIntlVariant(v) {
			t.Errorf("IsIntlVariant(%q) = false", v)
		}
	}
	for _, v := range []string{"trae-cn", "cn", "solo"} {
		if cnupstream.IsIntlVariant(v) {
			t.Errorf("IsIntlVariant(%q) = true", v)
		}
	}
}

// TestPanelRegionTogglePills locks the panel contract: the login toggle and
// the in-modal toggle expose exactly trae cn / trae (trae default), and the
// filter pills expose exactly 全部 / trae cn / trae / 耗尽 — no solo pill,
// while the verbatim SOLO card badge stays for stored accounts.
func TestPanelRegionTogglePills(t *testing.T) {
	html := string(panelHTML)
	for _, want := range []string{
		`data-login-region="trae-cn"`,
		`data-login-region="trae"`,
		`data-login-modal-region="trae-cn"`,
		`data-login-modal-region="trae"`,
		`let panelLoginRegion="trae"`,
		`data-region="trae-cn"`,
		`>trae cn <span`,
		`>trae <span`,
		`>SOLO</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel must carry %q", want)
		}
	}
	for _, gone := range []string{
		`data-region="solo"`,
		`data-login-region="solo"`,
		`data-login-modal-region="solo"`,
		`>SOLO <span`,
		`>CN <span`,
		`>Intl <span`,
	} {
		if strings.Contains(html, gone) {
			t.Errorf("panel must not carry retired pill/toggle %q", gone)
		}
	}
}
