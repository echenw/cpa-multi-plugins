package main

import "testing"

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
		{"unknown region routes cn", &storedAuth{Auth: storedTokens{Region: "eu"}}, "CN · 积分未知"},
		{"global note", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "Global · 积分未知"},
		{"cn note", &storedAuth{Auth: storedTokens{Domain: "www.codebuddy.cn"}}, "CN · 积分未知"},
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
		{"unknown region routes cn", &storedAuth{Auth: storedTokens{Region: "eu"}}, "WorkBuddy [CN]"},
		{"global label", &storedAuth{Auth: storedTokens{Domain: "workbuddy.ai"}}, "WorkBuddy [Global]"},
		{"cn label", &storedAuth{}, "WorkBuddy [CN]"},
		{"nickname kept", &storedAuth{Account: storedAccount{Nickname: "Alice"}, Auth: storedTokens{Domain: "workbuddy.ai"}}, "Alice [Global]"},
	}
	for _, tc := range cases {
		if got := labelForAuth(tc.sa); got != tc.want {
			t.Errorf("%s: labelForAuth() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
