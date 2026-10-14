package main

// import_json_test.go covers POST <base>/import for the third-party JSON
// credential format plus the PAT region fix. Hermetic: saves go through the
// stubbed hostAuthSaveJSONFn, PAT exchange through a stubbed transport —
// no live network.
//
// SECURITY: every fixture below is SYNTHETIC (dt-TEST..., drt-TEST...,
// pt-TEST...) in the same SHAPE as real dumps. Never paste real tokens here.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Synthetic CN-shaped entry: top-level user_id/display_name +
// shared_refresh_token win; raw token preferred for access.
const testImportCNEntry = `{
  "id": "qoder_cn_app_uid_TEST-UID-CN-001",
  "variant": "qoder_cn_ide",
  "user_id": "TEST-UID-CN-001",
  "display_name": "TEST-CN-User",
  "shared_refresh_token": "drt-TEST-CN-SHARED-001",
  "client_auth": {
    "qoder_cn_app": {
      "token": "dt-TEST-CN-CLIENT-001",
      "user": {"id": "TEST-UID-CN-001", "name": "TEST-CN-Client-Name", "phone": "13800000001"},
      "avatarUrl": "https://qoder.com.cn/avatar/TEST-UID-CN-001.png"
    }
  },
  "auth_user_info_raw": {"token": "dt-TEST-CN-RAW-001", "refreshToken": "drt-TEST-CN-RAW-001"}
}`

// Synthetic Intl-shaped entry: no variant, no top-level identity —
// fallbacks (client user.id/name, raw refreshToken) must engage.
const testImportIntlEntry = `{
  "id": "qoder_uid_TEST-UID-INTL-001",
  "client_auth": {
    "qoder_app": {
      "token": "dt-TEST-INTL-CLIENT-001",
      "user": {"id": "TEST-UID-INTL-001", "name": "TEST-Intl-User"},
      "avatar": "https://qoder.com/avatar/TEST-UID-INTL-001.png"
    }
  },
  "legacy_ids": ["leg-TEST-001"],
  "auth_user_info_raw": {"token": "", "refreshToken": "drt-TEST-INTL-RAW-001", "securityOauthToken": "sot-TEST-001"}
}`

const testImportUnknownEntry = `{
  "id": "something-else-001",
  "variant": "other_tool",
  "client_auth": {"other_app": {"token": "dt-TEST-X-001", "user": {"id": "TEST-UID-X-001", "name": "X"}}},
  "auth_user_info_raw": {"token": "dt-TEST-X-001", "refreshToken": "drt-TEST-X-001"}
}`

const testImportMissingTokenEntry = `{
  "id": "qoder_cn_app_uid_TEST-UID-NOTOK-001",
  "variant": "qoder_cn_ide",
  "user_id": "TEST-UID-NOTOK-001",
  "display_name": "TEST-NoToken",
  "client_auth": {"qoder_cn_app": {"token": "", "user": {"id": "TEST-UID-NOTOK-001", "name": "TEST-NoToken"}}},
  "auth_user_info_raw": {"token": "", "refreshToken": ""}
}`

// savedDoc parses one stubbed "name=json" save entry.
func savedDoc(t *testing.T, entry string) (string, map[string]any) {
	t.Helper()
	i := strings.Index(entry, "=")
	if i < 0 {
		t.Fatalf("bad save entry %.120s", entry)
	}
	name, raw := entry[:i], entry[i+1:]
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("save doc not JSON: %v", err)
	}
	return name, doc
}

func docRegionUID(t *testing.T, doc map[string]any) (string, string) {
	t.Helper()
	auth, _ := doc["auth"].(map[string]any)
	acct, _ := doc["account"].(map[string]any)
	region, _ := auth["region"].(string)
	uid, _ := acct["uid"].(string)
	access, _ := auth["accessToken"].(string)
	refresh, _ := auth["refreshToken"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("saved doc missing token pair (access=%q refresh=%q)", access, refresh)
	}
	return region, uid
}

func importResults(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["results"]
	if !ok {
		t.Fatalf("payload missing results: %v", payload)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("results not a list: %T", raw)
	}
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("result not an object: %T", v)
		}
		out = append(out, m)
	}
	return out
}

func TestImportJSONCNSingle(t *testing.T) {
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", testImportCNEntry)
	if status != http.StatusOK {
		t.Fatalf("CN import status=%d payload=%v", status, payload)
	}
	if payload["success"] != true {
		t.Fatalf("CN import success=false payload=%v", payload)
	}
	res := importResults(t, payload)
	if len(res) != 1 || res[0]["ok"] != true || res[0]["region"] != "cn" || res[0]["uid"] != "TEST-UID-CN-001" {
		t.Fatalf("CN per-entry report wrong: %v", res)
	}
	if len(saves) != 1 {
		t.Fatalf("CN must persist exactly once, saves=%d", len(saves))
	}
	name, doc := savedDoc(t, saves[0])
	if name != "qoder-cn-TEST-UID-CN-001.json" {
		t.Fatalf("CN file=%q want qoder-cn-TEST-UID-CN-001.json", name)
	}
	region, uid := docRegionUID(t, doc)
	if region != "cn" || uid != "TEST-UID-CN-001" {
		t.Fatalf("CN doc region=%q uid=%q", region, uid)
	}
	auth := doc["auth"].(map[string]any)
	if auth["accessToken"] != "dt-TEST-CN-RAW-001" {
		t.Fatalf("CN access must prefer raw token, got %q", auth["accessToken"])
	}
	if auth["refreshToken"] != "drt-TEST-CN-SHARED-001" {
		t.Fatalf("CN refresh must prefer shared_refresh_token, got %q", auth["refreshToken"])
	}
	if !strings.Contains(saves[0], "dt-TEST-CN-RAW-001") {
		t.Fatalf("CN save must carry access token")
	}
}

func TestImportJSONIntlFallbacks(t *testing.T) {
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", testImportIntlEntry)
	if status != http.StatusOK {
		t.Fatalf("Intl import status=%d payload=%v", status, payload)
	}
	res := importResults(t, payload)
	if len(res) != 1 || res[0]["ok"] != true || res[0]["region"] != "intl" || res[0]["uid"] != "TEST-UID-INTL-001" {
		t.Fatalf("Intl per-entry report wrong: %v", res)
	}
	if len(saves) != 1 {
		t.Fatalf("Intl must persist exactly once, saves=%d", len(saves))
	}
	name, doc := savedDoc(t, saves[0])
	if name != "qoder-intl-TEST-UID-INTL-001.json" {
		t.Fatalf("Intl file=%q want qoder-intl-TEST-UID-INTL-001.json", name)
	}
	region, uid := docRegionUID(t, doc)
	if region != "intl" || uid != "TEST-UID-INTL-001" {
		t.Fatalf("Intl doc region=%q uid=%q", region, uid)
	}
	auth := doc["auth"].(map[string]any)
	if auth["accessToken"] != "dt-TEST-INTL-CLIENT-001" {
		t.Fatalf("Intl access must fall back to client token, got %q", auth["accessToken"])
	}
	if auth["refreshToken"] != "drt-TEST-INTL-RAW-001" {
		t.Fatalf("Intl refresh must fall back to raw refreshToken, got %q", auth["refreshToken"])
	}
}

func TestImportJSONArrayMixedContinues(t *testing.T) {
	var saves []string
	stubLoginSave(t, &saves)
	body := "[" + testImportCNEntry + "," + testImportUnknownEntry + "," + testImportIntlEntry + "]"
	status, payload := callPanelLoginRoute(t, "/import", body)
	if status != http.StatusOK {
		t.Fatalf("mixed import status=%d want 200 payload=%v", status, payload)
	}
	if payload["success"] == true {
		t.Fatalf("mixed import success must be false (one entry refused)")
	}
	res := importResults(t, payload)
	if len(res) != 3 {
		t.Fatalf("mixed must report 3 entries: %v", res)
	}
	if res[0]["ok"] != true || res[1]["ok"] != false || res[2]["ok"] != true {
		t.Fatalf("mixed ok flags wrong: %v", res)
	}
	if res[1]["error"] == nil || res[1]["error"] == "" {
		t.Fatalf("refused entry must carry error: %v", res[1])
	}
	if len(saves) != 2 {
		t.Fatalf("mixed must persist 2, saves=%d", len(saves))
	}
}

func TestImportJSONUnknownRegionRefused(t *testing.T) {
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", testImportUnknownEntry)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown-region status=%d want 400 payload=%v", status, payload)
	}
	res := importResults(t, payload)
	if len(res) != 1 || res[0]["ok"] != false {
		t.Fatalf("unknown-region must report ok=false: %v", res)
	}
	if len(saves) != 0 {
		t.Fatalf("unknown-region must store nothing, saves=%d", len(saves))
	}
}

func TestImportJSONMissingTokenContinues(t *testing.T) {
	var saves []string
	stubLoginSave(t, &saves)
	body := "[" + testImportMissingTokenEntry + "," + testImportIntlEntry + "]"
	status, payload := callPanelLoginRoute(t, "/import", body)
	if status != http.StatusOK {
		t.Fatalf("missing-token batch status=%d want 200 payload=%v", status, payload)
	}
	res := importResults(t, payload)
	if len(res) != 2 || res[0]["ok"] != false || res[1]["ok"] != true {
		t.Fatalf("missing-token batch reports wrong: %v", res)
	}
	if len(saves) != 1 {
		t.Fatalf("only the good entry persists, saves=%d", len(saves))
	}
}

func stubPATExchange(t *testing.T, uid, name string) {
	t.Helper()
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/jobToken/exchange"):
			return loginJSON(http.StatusOK, `{"token":"jt-TEST-001","refresh_token":"jrt-TEST-001","expires_in":86400000,"refresh_token_expires_in":172800000}`), nil
		case strings.Contains(r.URL.Path, "/userinfo"):
			return loginJSON(http.StatusOK, `{"id":"`+uid+`","name":"`+name+`"}`), nil
		default:
			return loginJSON(http.StatusNotFound, `{}`), nil
		}
	})
}

func TestImportPATDefaultIntlUnchanged(t *testing.T) {
	stubPATExchange(t, "uid-PAT-TEST-1", "pat-user-1")
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", `{"pat":"pt-TEST-PAT-001"}`)
	if status != http.StatusOK {
		t.Fatalf("PAT default status=%d payload=%v", status, payload)
	}
	if payload["success"] != true || payload["region"] != "intl" {
		t.Fatalf("PAT default must succeed in intl: %v", payload)
	}
	if len(saves) != 1 {
		t.Fatalf("PAT must persist once, saves=%d", len(saves))
	}
	name, doc := savedDoc(t, saves[0])
	if name != "qoder-intl-uid-PAT-TEST-1.json" {
		t.Fatalf("PAT default file=%q want qoder-intl-uid-PAT-TEST-1.json", name)
	}
	region, uid := docRegionUID(t, doc)
	if region != "intl" || uid != "uid-PAT-TEST-1" {
		t.Fatalf("PAT default doc region=%q uid=%q", region, uid)
	}
}

func TestImportPATExplicitCN(t *testing.T) {
	stubPATExchange(t, "uid-PAT-TEST-2", "pat-user-2")
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", `{"pat":"pt-TEST-PAT-002","region":"cn"}`)
	if status != http.StatusOK {
		t.Fatalf("PAT cn status=%d payload=%v", status, payload)
	}
	if payload["success"] != true || payload["region"] != "cn" {
		t.Fatalf("PAT cn must succeed in cn: %v", payload)
	}
	if len(saves) != 1 {
		t.Fatalf("PAT cn must persist once, saves=%d", len(saves))
	}
	name, doc := savedDoc(t, saves[0])
	if name != "qoder-cn-uid-PAT-TEST-2.json" {
		t.Fatalf("PAT cn file=%q want qoder-cn-uid-PAT-TEST-2.json", name)
	}
	region, _ := docRegionUID(t, doc)
	if region != "cn" {
		t.Fatalf("PAT cn doc region=%q", region)
	}
}

func TestImportPATInvalidRegionNothingStored(t *testing.T) {
	calls := 0
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return loginJSON(http.StatusOK, `{}`), nil
	})
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", `{"pat":"pt-TEST-PAT-003","region":"eu"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PAT invalid region status=%d want 400 payload=%v", status, payload)
	}
	if payload["success"] == true {
		t.Fatalf("PAT invalid region must fail: %v", payload)
	}
	if calls != 0 {
		t.Fatalf("invalid region must not hit network, calls=%d", calls)
	}
	if len(saves) != 0 {
		t.Fatalf("invalid region must store nothing, saves=%d", len(saves))
	}
}

func TestImportRawPATBody(t *testing.T) {
	stubPATExchange(t, "uid-PAT-TEST-4", "pat-user-4")
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", `pt-TEST-PAT-004`)
	if status != http.StatusOK || payload["success"] != true {
		t.Fatalf("raw PAT body: status=%d payload=%v", status, payload)
	}
	if len(saves) != 1 {
		t.Fatalf("raw PAT must persist once, saves=%d", len(saves))
	}
}

// TestImportModalPATRegionSelector pins the review finding 1 fix: the import
// modal must expose a CN/Intl selector (default Intl) and importAuth must
// post the selected region with a PAT body ({pat, region}). JSON path stays
// untouched — per-entry regions ride the dump.
func TestImportModalPATRegionSelector(t *testing.T) {
	html := string(panelHTML)
	// Selector pills live in the import modal, default Intl.
	if !strings.Contains(html, `data-import-region="cn"`) {
		t.Fatal("import modal missing CN pill (data-import-region=\"cn\")")
	}
	if !strings.Contains(html, `class="lreg on" data-import-region="intl"`) {
		t.Fatal("import modal Intl pill must default on")
	}
	if !strings.Contains(html, `class="lreg" data-import-region="cn"`) {
		t.Fatal("import modal CN pill must default off")
	}
	// Pills must sit inside the import modal (not login modal / top bar).
	imp := strings.Index(html, `id="importModal"`)
	cnPill := strings.Index(html, `data-import-region="cn"`)
	login := strings.Index(html, `id="loginModal"`)
	if imp < 0 || cnPill < 0 || login < 0 || !(imp < cnPill && cnPill < login) {
		t.Fatal("import region pills must sit inside the import modal")
	}
	// importAuth posts the selected region for raw PAT input.
	if !strings.Contains(html, `body=JSON.stringify({pat:raw,region:importRegion})`) {
		t.Fatal("importAuth must post {pat, region} for raw PAT input")
	}
	// A pasted {"pat":...} wrapper keeps an explicit region, else the pill.
	if !strings.Contains(html, `region:(typeof probe.region==="string"&&probe.region)||importRegion`) {
		t.Fatal("importAuth must carry probe.region || importRegion for PAT wrapper")
	}
	// JSON credential path unchanged: raw dump posted as-is, no region injection.
	if !strings.Contains(html, `}else{body=raw;isJSON=true}`) {
		t.Fatal("JSON credential path must post the dump as-is")
	}
}

// TestImportPATCNViaUIBody is the hermetic end of the UI wiring: the exact
// body importAuth now sends for a CN-pill PAT ({pat, region:"cn"}) must land
// in the cn file. Stubbed exchange + persist, no live network.
func TestImportPATCNViaUIBody(t *testing.T) {
	stubPATExchange(t, "uid-PAT-UI-CN-1", "pat-ui-cn-1")
	var saves []string
	stubLoginSave(t, &saves)
	status, payload := callPanelLoginRoute(t, "/import", `{"pat":"pt-TEST-PAT-UI-CN-001","region":"cn"}`)
	if status != http.StatusOK {
		t.Fatalf("UI CN PAT status=%d payload=%v", status, payload)
	}
	if payload["success"] != true || payload["region"] != "cn" {
		t.Fatalf("UI CN PAT must succeed in cn: %v", payload)
	}
	if len(saves) != 1 {
		t.Fatalf("UI CN PAT must persist once, saves=%d", len(saves))
	}
	name, doc := savedDoc(t, saves[0])
	if name != "qoder-cn-uid-PAT-UI-CN-1.json" {
		t.Fatalf("UI CN PAT file=%q want qoder-cn-uid-PAT-UI-CN-1.json", name)
	}
	region, uid := docRegionUID(t, doc)
	if region != "cn" || uid != "uid-PAT-UI-CN-1" {
		t.Fatalf("UI CN PAT doc region=%q uid=%q", region, uid)
	}
}
