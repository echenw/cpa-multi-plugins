package main

import "testing"

// TestSplitBuildPinWiring verifies the request-carried default is trae
// regardless of the legacy build pin: the pin is ignored since config-file
// realm switching was deleted. Run once WITHOUT ldflags (unified flavor)
// and once WITH
//
//	go test -ldflags "-X main.pluginVariantPin=trae" -run TestSplitBuildPinWiring
//
// (split flavor: the default must still be trae).
func TestSplitBuildPinWiring(t *testing.T) {
	if defaultRequestLoginVariant != variantIntl {
		t.Fatalf("default = %q, want %q", defaultRequestLoginVariant, variantIntl)
	}
	for _, raw := range []string{`{}`, ``} {
		got, err := resolveRequestLoginVariant([]byte(raw))
		if err != nil {
			t.Fatalf("raw %q: %v", raw, err)
		}
		if got != variantIntl {
			t.Fatalf("raw %q: got %q, want trae default (pin %q ignored)", raw, got, pluginVariantPin)
		}
	}
}

// TestSplitCredentialFileName pins the split-build credential namespace: the
// registered id already encodes the region, so no region infix may be
// appended.
func TestSplitCredentialFileName(t *testing.T) {
	if providerName == "trae" {
		if got := credentialFileName(variantIntl, "u1"); got != "trae-u1.json" {
			t.Fatalf("unified trae file = %q", got)
		}
		if got := credentialFileName(variantCN, "u1"); got != "trae-cn-u1.json" {
			t.Fatalf("unified trae-cn file = %q", got)
		}
		return
	}
	// Split build: id encodes the region.
	if got := credentialFileName(variantIntl, "u1"); got != providerName+"-u1.json" {
		t.Fatalf("split file = %q, want %q", got, providerName+"-u1.json")
	}
}
