package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Issue #24: login_platform must be sticky across bare reconfigures and must
// also be seen when the host sends the config as a flow-style mapping or
// JSON — neither of which the line-scan can match. Config-file login region
// switching is deleted: a login_region key is ignored and the login realm
// always resolves from the request (default global).

func wbConfigureWire(t *testing.T, cfg string) {
	t.Helper()
	raw, err := json.Marshal(struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{ConfigYAML: []byte(cfg)})
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	configure(raw)
}

func TestWorkbuddyConfigureStickyLoginPlatform(t *testing.T) {
	oldPlatform := loginPlatform
	t.Cleanup(func() {
		loginPlatformMu.Lock()
		loginPlatform = oldPlatform
		loginPlatformMu.Unlock()
	})

	wbConfigureWire(t, "enabled: true\nlogin_platform: ide\npriority: 0\n")
	if loginPlatform != "ide" {
		t.Fatalf("login_platform ide not applied: %q", loginPlatform)
	}

	// Bare reconfigure (auth-store churn) must NOT reset to CLI.
	wbConfigureWire(t, "enabled: true\npriority: 0\n")
	if loginPlatform != "ide" {
		t.Fatalf("bare reconfigure reset sticky platform: %q", loginPlatform)
	}

	// Explicit switch back still works.
	wbConfigureWire(t, "enabled: true\nlogin_platform: CLI\n")
	if loginPlatform != "CLI" {
		t.Fatalf("explicit reset not applied: platform=%q", loginPlatform)
	}
}

func TestWorkbuddyConfigureLoginRegionIgnored(t *testing.T) {
	// A login_region key in any serialization style must not move the login
	// realm: resolution is request-only with a global default.
	for _, cfg := range []string{
		"enabled: true\nlogin_region: global\npriority: 0\n",
		"enabled: true\nlogin_region: cn\npriority: 0\n",
		"{enabled: true, login_region: global, priority: 0}\n",
		`{"enabled":true,"login_region":"global"}`,
	} {
		wbConfigureWire(t, cfg)
		got, err := resolveRequestLoginRegion([]byte(`{}`))
		if err != nil {
			t.Fatalf("cfg %q: resolve: %v", cfg, err)
		}
		if got != regionGlobal {
			t.Fatalf("cfg %q: default login realm = %q, want global", cfg, got)
		}
	}
}

// Issue #25: the save funnel must carry host-owned operator fields across
// typed rebuilds.

func TestWorkbuddyPreservePluginDocKeysCarriesHostOwnedFields(t *testing.T) {
	physical := `{"type":"workbuddy","note":"old note","model_cache":{"x":1},"proxy_url":"http://127.0.0.1:7890","weight":3,"priority":9,"prefix":"teamA","label":"主号","request_retry":2,"headers":{"X-Extra":"1"}}`
	origList, origGet := hostAuthListFn, hostAuthGetPhysicalFn
	hostAuthListFn = func() ([]pluginapi.HostAuthFileEntry, error) {
		return []pluginapi.HostAuthFileEntry{{AuthIndex: "i1", Name: "workbuddy-u1.json"}}, nil
	}
	hostAuthGetPhysicalFn = func(string) (*hostAuthPhysical, error) {
		return &hostAuthPhysical{AuthIndex: "i1", Name: "workbuddy-u1.json", JSON: []byte(physical)}, nil
	}
	t.Cleanup(func() { hostAuthListFn, hostAuthGetPhysicalFn = origList, origGet })

	sa := &storedAuth{Account: storedAccount{UID: "u1"}}
	fresh, err := buildAuthFileJSON(sa, false, "new note", nil)
	if err != nil {
		t.Fatalf("buildAuthFileJSON: %v", err)
	}
	merged := preservePluginDocKeys("workbuddy-u1.json", fresh)

	var out map[string]json.RawMessage
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	for _, key := range []string{"proxy_url", "weight", "priority", "prefix", "label", "request_retry", "headers", "model_cache"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("merged doc lost host-owned key %q", key)
		}
	}
	// Plugin-owned fields keep the caller's fresh values.
	if string(out["note"]) != `"new note"` {
		t.Fatalf("note = %s, want fresh caller value", out["note"])
	}
}
