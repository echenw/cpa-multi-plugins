package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func traeExclusionModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		{ID: "auto", Name: "auto", OwnedBy: providerName},
		{ID: "auto-intl", Name: "auto-intl", OwnedBy: providerName},
		{ID: "free", Name: "free", OwnedBy: providerName},
	}
}

func assertTraeModelIDs(t *testing.T, got []pluginapi.ModelInfo, wantIDs []string) {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if len(ids) != len(wantIDs) {
		t.Fatalf("model ids = %v, want %v", ids, wantIDs)
	}
	for i := range wantIDs {
		if ids[i] != wantIDs[i] {
			t.Fatalf("model ids = %v, want %v", ids, wantIDs)
		}
	}
}

// TestFilterExcludedRegionSubKeys pins the v0.12.62 granularity contract
// under B-plan regions: the bare "trae" provider key excludes across every
// region namespace, while the "trae-cn" / "trae" sub-keys exclude one
// namespace each (the trae sub-key coincides with the provider key, so it
// is idempotent). Because trae namespaces are model-ID suffixes (trae-cn
// bare, trae "-intl"), exclusion patterns always match the advertised id.
// The retired "trae-solo" sub-key is gone: no code path consults it.
func TestFilterExcludedRegionSubKeys(t *testing.T) {
	host := pluginapi.HostConfigSummary{
		ExcludedModels: map[string][]string{
			"trae":    {"free"},
			"trae-cn": {"auto"},
		},
	}

	// Provider key alone (static path): every namespace loses "free".
	assertTraeModelIDs(t, filterExcludedModels(traeExclusionModels(), host),
		[]string{"auto", "auto-intl"})

	// trae-cn credential: provider key ∪ trae-cn.
	assertTraeModelIDs(t, filterExcludedModels(traeExclusionModels(), host, variantCN),
		[]string{"auto-intl"})

	// trae credential: provider key ∪ trae sub-key.
	assertTraeModelIDs(t, filterExcludedModels(traeExclusionModels(), host, variantIntl),
		[]string{"auto", "auto-intl"})

	// Hand-written key case drift is tolerated by the scan fallback.
	hostDrift := pluginapi.HostConfigSummary{
		ExcludedModels: map[string][]string{
			"Trae": {"free"},
		},
	}
	assertTraeModelIDs(t, filterExcludedModels(traeExclusionModels(), hostDrift, variantIntl),
		[]string{"auto", "auto-intl"})
}

// TestFilterExcludedSubKeyIsolation proves the trae-cn/trae sub-keys never
// bleed into each other: excluding "auto" under trae-cn must not remove
// trae's virtual bare "auto" from a trae credential's catalog when only
// the trae sub-key is consulted, and vice versa.
func TestFilterExcludedSubKeyIsolation(t *testing.T) {
	host := pluginapi.HostConfigSummary{
		ExcludedModels: map[string][]string{
			"trae-cn": {"auto"},
		},
	}
	// trae credential consults ONLY trae (+ provider key): the trae-cn
	// sub-key's "auto" entry must not touch it.
	assertTraeModelIDs(t, filterExcludedModels(traeExclusionModels(), host, variantIntl),
		[]string{"auto", "auto-intl", "free"})
	// trae-cn credential consults trae-cn: bare "auto" goes.
	assertTraeModelIDs(t, filterExcludedModels(traeExclusionModels(), host, variantCN),
		[]string{"auto-intl", "free"})
}
