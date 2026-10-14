package main

// region_display_test.go pins the display-only region relabel: CN renders as
// "qoder cn" and Intl as "qoder" across panel pills, account badges, login
// toggles, modal texts and note/label strings. Identity is untouched —
// stored Region values stay cn/intl, routing/endpoints/filenames/import
// logic are asserted unchanged alongside. Hermetic: pure functions plus the
// embedded panelHTML string, no live network.

import (
	"strings"
	"testing"
)

func TestRegionDisplayNameValues(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cn", "qoder cn"},
		{"CN", "qoder cn"},
		{"", "qoder cn"},
		{"bogus", "qoder cn"},
		{"intl", "qoder"},
		{"INTL", "qoder"},
		{"global", "qoder"},
	}
	for _, c := range cases {
		if got := regionDisplayName(c.in); got != c.want {
			t.Errorf("regionDisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRegionDisplayIdentityUntouched guards the hard boundary: the relabel
// must not move stored values, routing, endpoints or login defaults.
func TestRegionDisplayIdentityUntouched(t *testing.T) {
	if normalizeRegion("cn") != regionCN || normalizeRegion("intl") != regionIntl {
		t.Fatal("normalizeRegion semantics moved")
	}
	cn := &storedAuth{}
	cn.Auth.Region = "cn"
	if got := authRegion(cn); got != regionCN {
		t.Fatalf("cn account routed to %q", got)
	}
	intl := &storedAuth{}
	intl.Auth.Region = "intl"
	if got := authRegion(intl); got != regionIntl {
		t.Fatalf("intl account routed to %q", got)
	}
	if upstreamBaseForRegion("cn") == upstreamBaseForRegion("intl") {
		t.Fatal("per-region upstream routing collapsed")
	}
	if gatewayBaseForRegion("cn") == gatewayBaseForRegion("intl") {
		t.Fatal("per-region gateway routing collapsed")
	}
	if domainForRegion("cn") != domainCN || domainForRegion("intl") != domainIntl {
		t.Fatal("per-region domain mapping moved")
	}
	if r, err := resolveRequestLoginRegion([]byte(`{}`), defaultRequestLoginRegion); err != nil || r != regionIntl {
		t.Fatalf("login default moved: region=%q err=%v", r, err)
	}
}

func TestNotePrefixDisplayLabels(t *testing.T) {
	cn := &storedAuth{}
	cn.Auth.Region = "cn"
	intl := &storedAuth{}
	intl.Auth.Region = "intl"
	cases := []struct {
		sa       *storedAuth
		disabled bool
		want     string
	}{
		{nil, false, "qoder cn"},
		{cn, false, "qoder cn"},
		{intl, false, "qoder"},
		{cn, true, "qoder cn · 已禁用"},
		{intl, true, "qoder · 已禁用"},
	}
	for _, c := range cases {
		if got := notePrefix(c.sa, c.disabled); got != c.want {
			t.Errorf("notePrefix(disabled=%v) = %q, want %q", c.disabled, got, c.want)
		}
	}
}

func TestCreditSegmentStripsDisplayAndLegacyPrefixes(t *testing.T) {
	cases := []struct {
		note string
		want string
	}{
		{"qoder cn · 余100 已用5", "余100 已用5"},
		{"qoder · 已禁用 · 余3 已用2", "余3 已用2"},
		{"qoder · 余10 已用1 池50", "余10 已用1 池50"},
		{"qoder cn · 积分未知", ""},
		{"qoder cn", ""},
		// Legacy bare-shorthand notes in the wild must keep parsing.
		{"CN · 余100 已用5", "余100 已用5"},
		{"INTL · 余10 已用1 池50", "余10 已用1 池50"},
		{"", ""},
	}
	for _, c := range cases {
		if got := creditSegmentFromNote(c.note); got != c.want {
			t.Errorf("creditSegmentFromNote(%q) = %q, want %q", c.note, got, c.want)
		}
	}
}

func TestDisplayNoteUsesDisplayPrefix(t *testing.T) {
	cn := &storedAuth{}
	cn.Auth.Region = "cn"
	intl := &storedAuth{}
	intl.Auth.Region = "intl"
	cr := &creditsSummary{TotalRemain: 7, TotalUsed: 2, TotalSize: 9}
	if got := displayNote(cn, cr, false); !strings.HasPrefix(got, "qoder cn · ") {
		t.Fatalf("cn display note prefix wrong: %q", got)
	}
	if got := displayNote(intl, cr, false); !strings.HasPrefix(got, "qoder · ") {
		t.Fatalf("intl display note prefix wrong: %q", got)
	}
	for _, sa := range []*storedAuth{cn, intl, nil} {
		got := displayNote(sa, cr, false)
		if strings.HasPrefix(got, "CN ·") || strings.HasPrefix(got, "INTL") {
			t.Fatalf("legacy bare prefix leaked into display note: %q", got)
		}
	}
}

func TestLabelForAuthDisplayTags(t *testing.T) {
	cn := &storedAuth{}
	cn.Auth.Region = "cn"
	cn.Account.Nickname = "n1"
	intl := &storedAuth{}
	intl.Auth.Region = "intl"
	intl.Account.Nickname = "n1"
	if got := labelForAuth(nil); got != "QoderWork [qoder cn]" {
		t.Fatalf("nil label = %q, want QoderWork [qoder cn]", got)
	}
	if got := labelForAuth(cn); got != "n1 [qoder cn]" {
		t.Fatalf("cn label = %q, want n1 [qoder cn]", got)
	}
	if got := labelForAuth(intl); got != "n1 [qoder]" {
		t.Fatalf("intl label = %q, want n1 [qoder]", got)
	}
}

func TestModelGroupRegionDisplayLabels(t *testing.T) {
	if got := modelGroupRegionLabel("cn"); got != "qoder cn（qoder.com.cn）" {
		t.Fatalf("cn group label = %q", got)
	}
	if got := modelGroupRegionLabel("intl"); got != "qoder（qoder.com）" {
		t.Fatalf("intl group label = %q", got)
	}
}

// TestPanelFilterPillsDisplayLabels pins pills reading 全部/qoder cn/qoder/
// 耗尽 with data-region values, order and count spans intact.
func TestPanelFilterPillsDisplayLabels(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `>qoder cn <span class="cnt" id="cntCn"`)
	mustContain(t, html, `>qoder <span class="cnt" id="cntIntl"`)
	mustOrder(t, html,
		`data-region="all"`,
		`data-region="cn"`,
		`data-region="intl"`,
		`data-region="exhausted"`,
	)
	for _, stale := range []string{`>CN <span class="cnt"`, `>Intl <span class="cnt"`} {
		if strings.Contains(html, stale) {
			t.Fatalf("panel.html still carries stale pill text %q", stale)
		}
	}
}

// TestPanelRegionTogglesDisplayLabels pins the login/import toggle pills
// (top bar, login modal, import modal) rendering qoder cn/qoder while the
// data attrs, onclick region args and the Intl default stay put.
func TestPanelRegionTogglesDisplayLabels(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `data-login-region="cn" onclick="pickLoginRegion('cn',this)">qoder cn</button>`)
	mustContain(t, html, `class="lreg on" data-login-region="intl" onclick="pickLoginRegion('intl',this)">qoder</button>`)
	mustContain(t, html, `data-login-modal-region="cn" onclick="pickLoginModalRegion('cn',this)">qoder cn</button>`)
	mustContain(t, html, `class="lreg on" data-login-modal-region="intl" onclick="pickLoginModalRegion('intl',this)">qoder</button>`)
	mustContain(t, html, `data-import-region="cn" onclick="pickImportRegion('cn',this)">qoder cn</button>`)
	mustContain(t, html, `class="lreg on" data-import-region="intl" onclick="pickImportRegion('intl',this)">qoder</button>`)
	mustContain(t, html, `let loginRegion="intl"`)
	mustContain(t, html, `let importRegion="intl"`)
	for _, stale := range []string{`'cn',this)">CN</button>`, `'intl',this)">Intl</button>`} {
		if strings.Contains(html, stale) {
			t.Fatalf("panel.html still carries stale toggle text %q", stale)
		}
	}
}

// TestPanelBadgeSummaryModalDisplayLabels pins account badges, the usage
// rollup and modal texts showing the display names.
func TestPanelBadgeSummaryModalDisplayLabels(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `${isIntl?"qoder":"qoder cn"}`)
	mustContain(t, html, `function regionLabel(r){return (r||"cn")==="intl"?"qoder":"qoder cn"}`)
	mustContain(t, html, `qoder cn 可用 ${cnRemain}`)
	mustContain(t, html, `qoder 可用 ${intlRemain}`)
	mustContain(t, html, `渠道（默认 qoder，切换会重新开始登录）`)
	mustContain(t, html, `PAT 渠道（默认 qoder；`)
	for _, stale := range []string{`${isIntl?"Intl":"CN"}`, `const region="CN";`} {
		if strings.Contains(html, stale) {
			t.Fatalf("panel.html still carries stale badge/region text %q", stale)
		}
	}
}
