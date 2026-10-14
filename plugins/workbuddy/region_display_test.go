package main

import (
	"strings"
	"testing"
)

// Display vs routing contract: accountRegion only knows cn/global (unknown
// stored values fall back to cn for routing and never crash), while
// panelRegion passes unknown stored region values through verbatim so those
// cards keep listing under the 全部 filter.

func TestAccountRegionTwoWay(t *testing.T) {
	cases := []struct {
		name   string
		sa     *storedAuth
		region string
	}{
		// explicit region wins over any domain
		{"explicit global wins", &storedAuth{Auth: storedTokens{Region: "global"}}, "global"},
		{"explicit cn wins", &storedAuth{Auth: storedTokens{Region: "cn", Domain: "workbuddy.ai"}}, "cn"},
		{"explicit case/space tolerant", &storedAuth{Auth: storedTokens{Region: " GLOBAL "}}, "global"},
		// unknown stored values fall back to cn for routing (never crash)
		{"unknown region falls back cn", &storedAuth{Auth: storedTokens{Region: "eu", Domain: "copilot.tencent.com"}}, "cn"},
		{"unknown region no domain falls back cn", &storedAuth{Auth: storedTokens{Region: "eu"}}, "cn"},
		// legacy sniff regression guards (old behaviour must survive)
		{"global domain still global", &storedAuth{Auth: storedTokens{Domain: "www.workbuddy.ai"}}, "global"},
		{"cn domain still cn", &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}, "cn"},
		{"empty still cn", &storedAuth{}, "cn"},
		{"nil still cn", nil, "cn"},
	}
	for _, tc := range cases {
		if got := accountRegion(tc.sa); got != tc.region {
			t.Errorf("%s: accountRegion() = %q, want %q", tc.name, got, tc.region)
		}
	}
}

func TestPanelRegionVerbatimFallback(t *testing.T) {
	cases := []struct {
		name   string
		sa     *storedAuth
		region string
	}{
		{"global domain", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "global"},
		{"cn domain", &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}, "cn"},
		{"unknown stored region verbatim", &storedAuth{Auth: storedTokens{Region: "eu"}}, "eu"},
		{"unknown stored region verbatim mixed case", &storedAuth{Auth: storedTokens{Region: " EU "}}, "eu"},
		{"empty still cn", &storedAuth{}, "cn"},
		{"nil still cn", nil, "cn"},
	}
	for _, tc := range cases {
		if got := panelRegion(tc.sa); got != tc.region {
			t.Errorf("%s: panelRegion() = %q, want %q", tc.name, got, tc.region)
		}
	}
}

func TestDisplayNoteTwoWay(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		// Display-only relabel: stored routing stays cn/global while the
		// user-visible note head shows workbuddy cn / workbuddy ai.
		{"unknown region routes cn", &storedAuth{Auth: storedTokens{Region: "eu"}}, "workbuddy cn · 积分未知"},
		{"global note", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "workbuddy ai · 积分未知"},
		{"cn note", &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}, "workbuddy cn · 积分未知"},
	}
	for _, tc := range cases {
		if got := displayNote(tc.sa, nil, false); got != tc.want {
			t.Errorf("%s: displayNote() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLabelForAuthTwoWay(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		{"unknown region routes cn", &storedAuth{Auth: storedTokens{Region: "eu"}}, "WorkBuddy [workbuddy cn]"},
		{"global label", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "WorkBuddy [workbuddy ai]"},
		{"cn label", &storedAuth{}, "WorkBuddy [workbuddy cn]"},
		{"nickname kept", &storedAuth{Account: storedAccount{Nickname: "Alice"}, Auth: storedTokens{Domain: "workbuddy.ai"}}, "Alice [workbuddy ai]"},
	}
	for _, tc := range cases {
		if got := labelForAuth(tc.sa); got != tc.want {
			t.Errorf("%s: labelForAuth() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPanelRegionDisplayNames pins the display-only relabel: filter pills,
// login toggles and modal texts show workbuddy cn / workbuddy ai while every
// identity carrier (data-region values, toggle defaults, JS login default,
// counters, badge classes) stays exactly as-is. Hermetic: string asserts on
// the embedded panelHTML only.
func TestPanelRegionDisplayNames(t *testing.T) {
	html := string(panelHTML)
	// Filter pills keep order 全部 → realms → 耗尽 with display names.
	allIdx := strings.Index(html, `data-region="all"`)
	cnIdx := strings.Index(html, `>workbuddy cn <span class="cnt" id="cntCn"`)
	aiIdx := strings.Index(html, `>workbuddy ai <span class="cnt" id="cntGlobal"`)
	exIdx := strings.Index(html, `data-region="exhausted"`)
	if allIdx < 0 || cnIdx < 0 || aiIdx < 0 || exIdx < 0 {
		t.Fatalf("filter pills missing display names (all=%d cn=%d ai=%d exhausted=%d)", allIdx, cnIdx, aiIdx, exIdx)
	}
	if !(allIdx < cnIdx && cnIdx < aiIdx && aiIdx < exIdx) {
		t.Fatalf("pill order broken: want 全部 → workbuddy cn → workbuddy ai → 耗尽")
	}
	// Stored region values carried by the pills are untouched.
	for _, want := range []string{`data-region="cn"`, `data-region="global"`, `filterRegion('cn'`, `filterRegion('global'`} {
		if !strings.Contains(html, want) {
			t.Errorf("panel lost identity carrier %q", want)
		}
	}
	// Login toggles (toolbar + modal) show display names, default stays Global.
	for _, want := range []string{
		`data-login-region="cn" onclick="pickLoginRegion('cn',this)">workbuddy cn<`,
		`data-login-region="global" onclick="pickLoginRegion('global',this)">workbuddy ai<`,
		`data-login-modal-region="cn" onclick="pickLoginModalRegion('cn',this)">workbuddy cn<`,
		`data-login-modal-region="global" onclick="pickLoginModalRegion('global',this)">workbuddy ai<`,
		`class="lreg on" data-login-region="global"`,
		`class="lreg on" data-login-modal-region="global"`,
		`let loginRegion="global"`,
		`默认 <code>workbuddy ai</code>`,
		`渠道（默认 workbuddy ai，切换会重新开始登录）`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel missing %q", want)
		}
	}
	// Card badge renderer maps stored regions to display names.
	for _, want := range []string{
		`isGlobal?"workbuddy ai":(isCN?"workbuddy cn":region)`,
		`currentFilter==="global"?"workbuddy ai 账号"`,
		`"workbuddy cn 账号"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("panel badge/summary missing display mapping %q", want)
		}
	}
	// No bare pill/badge label text remains: every >CN< / >Global< button
	// label is gone (data values and JS identifiers intentionally remain).
	for _, stale := range []string{`>CN<`, `>Global<`, `>CN <span`, `>Global <span`} {
		if strings.Contains(html, stale) {
			t.Errorf("panel still carries stale display label %q", stale)
		}
	}
}
