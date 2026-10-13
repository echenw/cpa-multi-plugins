package main

import (
	"strings"
	"testing"
)

// v0.12.18: the unified panel's "切换到 Trae Intl 账号面板 →" link pointed at
// the /intl_panel resource path, which — since the v0.12.2 merge — serves the
// SAME unified panel.html. Clicking it reloaded an identical page, which users
// read as "click = refresh". The link is gone (replaced by a muted note: Intl
// accounts render in the same list with INTL badges), and the legacy
// intl_panel.html page (only reachable on split-binary deployments serving the
// trae-intl.so build) now links back to the trae plugin's panel via an
// ABSOLUTE cross-plugin path — its old relative href="panel" resolved against
// /v0/resource/plugins/trae-intl/panel and reloaded itself.
func TestPanelNoDeadIntlSwitchLink(t *testing.T) {
	if strings.Contains(string(panelHTML), `href="intl_panel"`) {
		t.Errorf("unified panel still carries the dead self-alias switch link (click = reload)")
	}
	if !strings.Contains(string(panelHTML), "INTL 徽标") {
		t.Errorf("unified panel must explain where Intl accounts live, got no muted note")
	}
}

func TestIntlPanelSwitchLinkIsAbsolute(t *testing.T) {
	html := string(intlpanelHTML)
	if strings.Contains(html, `href="panel"`) {
		t.Errorf("intl panel switch link must not be relative (self-reload on trae-intl deployments)")
	}
	if !strings.Contains(html, `href="/v0/resource/plugins/trae/panel"`) {
		t.Errorf("intl panel must link the trae plugin panel by absolute path")
	}
}

// Panel authorize-login: the live unified panel carries the 授权登录 button
// wired to an in-panel OAuth modal (NOT a bare new-tab jump): title + intro,
// CN/Intl toggle (default Intl, switching restarts the login), step 1
// readonly auth URL (copy + open-in-new-tab, modal stays open), step 2
// callback-URL input + submit, and a status line polling POST /login/poll
// (~5s) until success (close + reload) or expiry (restart hint). The legacy
// intl_panel.html (split-binary trae-intl deployments only) stays untouched.
func TestPanelAuthorizeLoginUI(t *testing.T) {
	html := string(panelHTML)
	if !strings.Contains(html, "授权登录") {
		t.Errorf("unified panel must carry the authorize-login button")
	}
	if !strings.Contains(html, `name="loginRealm"`) {
		t.Errorf("unified panel must carry the CN/Intl realm toggle")
	}
	if !strings.Contains(html, `value="intl" checked`) {
		t.Errorf("realm toggle must default to Intl")
	}
	if !strings.Contains(html, "/login/start") {
		t.Errorf("panel JS must call POST /login/start")
	}
	if !strings.Contains(html, `id="loginModalScrim"`) {
		t.Errorf("unified panel must carry the in-panel OAuth modal")
	}
	if !strings.Contains(html, "/login/poll") {
		t.Errorf("modal JS must poll POST /login/poll for completion")
	}
	if !strings.Contains(html, "/login/callback") {
		t.Errorf("modal JS must submit the callback URL to POST /login/callback")
	}
	for _, want := range []string{"loginAuthUrl", "loginCallbackUrl", "loginModalStatus", "复制链接", "打开链接", "提交回调"} {
		if !strings.Contains(html, want) {
			t.Errorf("modal must carry %q (auth-URL + copy + open-link, callback input + submit, status line)", want)
		}
	}
	if strings.Contains(string(intlpanelHTML), "授权登录") {
		t.Errorf("legacy intl panel must stay untouched (no authorize-login button)")
	}
	if strings.Contains(string(intlpanelHTML), "loginModalScrim") {
		t.Errorf("legacy intl panel must stay untouched (no OAuth modal)")
	}
}
