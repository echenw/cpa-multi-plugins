package main

// panel_structure_test.go asserts the canonical unified-panel structure
// (shared across the OAuth-sibling panels; qoder/panel.html is the reference).
// Structure/labels/order only — no behavior is exercised here.

import (
	"strings"
	"testing"
)

func panelHTMLStr(t *testing.T) string {
	t.Helper()
	if len(panelHTML) == 0 {
		t.Fatal("panelHTML is empty")
	}
	return string(panelHTML)
}

func mustContain(t *testing.T, html, sub string) {
	t.Helper()
	if !strings.Contains(html, sub) {
		t.Fatalf("panel.html missing %q", sub)
	}
}

// mustOrder asserts subs appear in html strictly in the given order.
func mustOrder(t *testing.T, html string, subs ...string) {
	t.Helper()
	prev := -1
	for _, s := range subs {
		i := strings.Index(html, s)
		if i < 0 {
			t.Fatalf("panel.html missing %q for order check", s)
		}
		if i <= prev {
			t.Fatalf("panel.html order violation: %q must come after previous marker", s)
		}
		prev = i
	}
}

// (a) <title>"<Product> 面板" matches H1 "<Product> 账号面板".
func TestPanelTitleH1Symmetry(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, "<title>QoderWork 面板</title>")
	mustContain(t, html, "<h1>QoderWork 账号面板</h1>")
}

// (b) Top action row order: login → import → refresh → checkin-all →
// model-disable, with stable ids; login toggle defaults to Intl.
func TestPanelTopActionOrder(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `<button id="loginBtn"`)
	mustContain(t, html, `id="importBtn"`)
	mustContain(t, html, `<button id="refreshBtn"`)
	mustContain(t, html, `id="checkinAllBtn"`)
	mustContain(t, html, `id="modelDisableBtn"`)
	mustContain(t, html, `>授权登录</button>`)
	mustContain(t, html, `>刷新数据</button>`)
	mustContain(t, html, `>全部签到</button>`)
	mustContain(t, html, `>模型禁用</button>`)
	mustOrder(t, html,
		`id="loginBtn"`,
		`id="importBtn"`,
		`id="refreshBtn"`,
		`id="checkinAllBtn"`,
		`id="modelDisableBtn"`,
	)
	// CN/Intl toggle sits with the login button and defaults to Intl.
	mustContain(t, html, `class="lreg on" data-login-region="intl"`)
	mustContain(t, html, `class="lreg" data-login-region="cn"`)
}

// (c) Login modal: ids, two steps, polling status + close-safe note,
// dedicated toggle pills (NOT the filter .ftag class).
func TestPanelLoginModalStructure(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `id="loginModal"`)
	mustContain(t, html, `<h2 id="loginModalTitle">授权登录</h2>`)
	// Step 1: readonly auth URL + copy + open-link (new tab, modal stays open).
	mustContain(t, html, `id="loginAuthURL"`)
	if !strings.Contains(html, `id="loginAuthURL" type="text" readonly`) &&
		!strings.Contains(html, `readonly`) {
		t.Fatal("loginAuthURL must be readonly")
	}
	mustContain(t, html, `copyLoginURL`)
	mustContain(t, html, `openLoginURL`)
	mustContain(t, html, `window.open(v,"_blank"`)
	// Step 2: callback input + submit.
	mustContain(t, html, `id="loginCallbackURL"`)
	mustContain(t, html, `submitLoginCallback`)
	// Status line + close-safe note.
	mustContain(t, html, `id="loginStatus"`)
	mustContain(t, html, "关闭弹窗不会中断")
	// Modal toggle uses dedicated pills, default Intl — never .ftag.
	mustContain(t, html, `class="lreg on" data-login-modal-region="intl"`)
	mustContain(t, html, `class="lreg" data-login-modal-region="cn"`)
	if strings.Contains(html, `ftag" data-login-modal-region`) ||
		strings.Contains(html, `ftag" data-login-region`) {
		t.Fatal("login toggles must not use the filter .ftag class")
	}
}

// (d) Import modal: mask/hd/bd/ft pattern, ids, import + cancel buttons.
func TestPanelImportModalStructure(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `class="modal-mask" id="importModal"`)
	mustContain(t, html, `id="importModalTitle"`)
	mustContain(t, html, `class="modal-hd"`)
	mustContain(t, html, `class="modal-bd"`)
	mustContain(t, html, `class="modal-ft"`)
	mustContain(t, html, `importAuth`)
	mustContain(t, html, `closeImportModal`)
}

// (e) Filter pills: .ftag, order 全部 → cn → intl → 耗尽, with count spans.
func TestPanelFilterPills(t *testing.T) {
	html := panelHTMLStr(t)
	mustContain(t, html, `class="ftag active" data-region="all"`)
	mustContain(t, html, `class="ftag" data-region="cn"`)
	mustContain(t, html, `class="ftag" data-region="intl"`)
	mustContain(t, html, `class="ftag" data-region="exhausted"`)
	mustOrder(t, html,
		`data-region="all"`,
		`data-region="cn"`,
		`data-region="intl"`,
		`data-region="exhausted"`,
	)
	for _, id := range []string{`id="cntAll"`, `id="cntCn"`, `id="cntIntl"`, `id="cntExhausted"`} {
		mustContain(t, html, id)
	}
}

// (f) Shared canonical CSS class set present for bar/modal/mask/cards.
func TestPanelCanonicalClasses(t *testing.T) {
	html := panelHTMLStr(t)
	for _, cls := range []string{
		`.bar{`, `.filter-bar{`, `.ftag{`, `.modal-mask{`, `.modal{`,
		`.modal-hd{`, `.modal-bd{`, `.modal-ft{`, `.card{`,
	} {
		mustContain(t, html, cls)
	}
}
