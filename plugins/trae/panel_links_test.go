package main

import (
	"net/http"
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

// Unified spec (a): <title> matches the H1 family ("Trae 面板" /
// "Trae 账号面板"); the stale "Trae SOLO CN 面板" title is gone. (The H1 text
// itself is rewritten per provider at runtime — providerName routing — so this
// pins the static markup, not the dynamic variant label.)
func TestPanelTitleSymmetry(t *testing.T) {
	html := string(panelHTML)
	if !strings.Contains(html, "<title>Trae 面板</title>") {
		t.Errorf("unified panel <title> must be \"Trae 面板\"")
	}
	if strings.Contains(html, "<title>Trae SOLO CN 面板</title>") {
		t.Errorf("stale <title>\"Trae SOLO CN 面板\" must be gone")
	}
	if !strings.Contains(html, `<h1 id="title">Trae 账号面板</h1>`) {
		t.Errorf("unified panel H1 must be \"Trae 账号面板\"")
	}
}

// Unified spec (b)+(d): top action row order — 授权登录 (loginBtn + CN/Intl
// toggle default Intl) → 导入凭证 (importBtn) → 刷新数据 (refreshBtn) →
// 全部签到 → 模型禁用.
func TestPanelTopBarOrder(t *testing.T) {
	html := string(panelHTML)
	marks := []string{
		`id="loginBtn"`,
		`id="importBtn"`,
		`id="refreshBtn"`,
		`>全部签到</button>`,
		`>模型禁用</button>`,
	}
	prev := -1
	for _, m := range marks {
		i := strings.Index(html, m)
		if i < 0 {
			t.Fatalf("top bar must carry %q", m)
		}
		if i <= prev {
			t.Fatalf("top bar order broken: %q not after previous mark", m)
		}
		prev = i
	}
	// The CN/Intl toggle next to 授权登录 defaults to Intl (.lreg.on on Intl,
	// in both the bar and the modal copies).
	for _, want := range []string{
		`class="lreg on" data-login-region="intl"`,
		`class="lreg on" data-login-modal-region="intl"`,
		`class="lreg" data-login-region="cn"`,
		`class="lreg" data-login-modal-region="cn"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("login region toggle must carry %q", want)
		}
	}
}

// Unified spec (c): single login entry — the modal flow only. The old second
// entry (standalone 完成登录 card: cbPaste + submitCallbackURL + extra
// 授权登录 button + radio realm toggle + loginModalScrim one-off ids) is
// gone; the modal's own submit covers the backend /login/callback endpoint
// (endpoints untouched).
func TestPanelSingleLoginEntry(t *testing.T) {
	html := string(panelHTML)
	for _, gone := range []string{
		"submitCallbackURL", "cbPaste", "完成登录", "loginModalScrim",
		"loginAuthUrl", "loginCallbackUrl", "loginModalStatus",
		`name="loginRealm"`, "panelLoginRealm(", "panelLoginRestart",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("unified panel must not carry standalone login remnant %q", gone)
		}
	}
	if n := strings.Count(html, `id="loginModal"`); n != 1 {
		t.Errorf("unified panel must carry exactly one login modal, got %d", n)
	}
	for _, want := range []string{
		"panelLoginOpen", "panelLoginSubmitCallback", "/login/callback",
		"panelLoginClose", "onLoginMaskClick",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("modal login flow must carry %q", want)
		}
	}
}

// Panel authorize-login: the live unified panel carries the 授权登录 button
// wired to an in-panel OAuth modal with the unified element-id conventions
// (loginModal / loginModalTitle=授权登录 / loginAuthURL + copy +
// open-in-new-tab-stays-open / loginCallbackURL + submit / loginStatus
// polling + close-safe note + .lreg CN/Intl toggles defaulting to Intl).
// The legacy intl_panel.html (split-binary trae-intl deployments only) stays
// untouched.
func TestPanelAuthorizeLoginUI(t *testing.T) {
	html := string(panelHTML)
	if !strings.Contains(html, "授权登录") {
		t.Errorf("unified panel must carry the authorize-login button")
	}
	for _, want := range []string{
		`id="loginModal"`,
		`id="loginModalTitle">授权登录<`,
		`id="loginAuthURL"`,
		`id="loginCallbackURL"`,
		`id="loginStatus"`,
		"/login/start",
		"/login/poll",
		"/login/callback",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unified login modal must carry %q (canonical element-id conventions)", want)
		}
	}
	for _, want := range []string{"复制链接", "打开链接", "提交回调", "步骤 1", "步骤 2", "关闭弹窗不会中断"} {
		if !strings.Contains(html, want) {
			t.Errorf("modal must carry %q (steps + copy/open/submit + close-safe note)", want)
		}
	}
	if strings.Contains(string(intlpanelHTML), "授权登录") {
		t.Errorf("legacy intl panel must stay untouched (no authorize-login button)")
	}
	if strings.Contains(string(intlpanelHTML), "loginModal") {
		t.Errorf("legacy intl panel must stay untouched (no OAuth modal)")
	}
}

// Unified spec (b) import-slot verdict: trae HAS a credential-import mechanism
// (POST /import → handleImportAuth, nested/flat JSON), so the unified panel
// carries the 导入凭证 button + modal in the canonical pattern at the
// canonical slot (nothing invented — the modal posts to the existing route).
func TestPanelImportCredential(t *testing.T) {
	html := string(panelHTML)
	for _, want := range []string{
		`id="importBtn"`, `>导入凭证<`,
		`id="importModal"`, `id="importModalTitle"`, `id="importJSON"`,
		"importAuth", `api("/import"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unified panel must carry import-credential %q (mechanism exists: POST /import)", want)
		}
	}
	if strings.Contains(string(intlpanelHTML), "importModal") {
		t.Errorf("legacy intl panel must stay untouched (no import modal)")
	}
}

// The panel's import modal posts to a real backend route (not invented UI):
// POST /import with an empty body is rejected by the handler
// (success=false), which proves the route is registered rather than 404.
func TestPanelImportEndpointRegistered(t *testing.T) {
	status, payload := postPanelRoute(t, http.MethodPost, "/import", `{}`)
	if status != http.StatusOK {
		t.Fatalf("POST /import status=%d, want 200 (route must exist)", status)
	}
	if payload["success"] != false {
		t.Fatalf("POST /import {} payload=%v, want success=false (missing payload)", payload)
	}
}

// Unified spec (f): filter pills — the .ftag component, order
// 全部 → CN → SOLO → Intl → 耗尽 with counts. The realm set kept is trae's
// real one (cn / solo / Intl from variant routing + card variants) — nothing
// invented. Cards carry data-variant for the filter.
func TestPanelFilterPills(t *testing.T) {
	html := string(panelHTML)
	if !strings.Contains(html, `class="filter-bar"`) {
		t.Fatalf("unified panel must carry the .filter-bar pill row")
	}
	marks := []string{
		`data-region="all"`, `data-region="cn"`, `data-region="solo"`,
		`data-region="intl"`, `data-region="exhausted"`,
	}
	prev := -1
	for _, m := range marks {
		i := strings.Index(html, m)
		if i < 0 {
			t.Fatalf("filter pills must carry %q", m)
		}
		if i <= prev {
			t.Fatalf("filter pill order broken: %q not after previous", m)
		}
		prev = i
	}
	for _, want := range []string{
		`id="cntAll"`, `id="cntCn"`, `id="cntSolo"`, `id="cntIntl"`, `id="cntExhausted"`,
		"filterRegion(", "updateFilterCounts(", `data-variant="${variant}"`,
		`class="ftag active" data-region="all"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("filter pills must carry %q", want)
		}
	}
}

// Unified spec (g): shared canonical CSS class names for bar/buttons/modal/
// mask/cards; trae one-off duplicates (.modal-scrim/.modal-box/.modal-status
// and friends) are gone.
func TestPanelCanonicalCSS(t *testing.T) {
	html := string(panelHTML)
	for _, want := range []string{
		`class="bar"`, "filter-bar", "ftag", "lreg",
		"modal-mask", `class="modal"`, "modal-hd", "modal-bd", "modal-ft", "modal-x",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("unified panel must carry canonical CSS class %q", want)
		}
	}
	for _, gone := range []string{
		"modal-scrim", "modal-box", "modal-status", "modal-intro", "modal-step", "step-t",
	} {
		if strings.Contains(html, gone) {
			t.Errorf("unified panel must not carry one-off CSS class %q", gone)
		}
	}
}
