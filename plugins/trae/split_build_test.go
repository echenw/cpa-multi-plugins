package main

import "testing"

// TestSplitBuildPinWiring verifies the request-carried default is intl
// regardless of the legacy build pin: the pin is ignored since config-file
// realm switching was deleted. Run once WITHOUT ldflags (unified flavor)
// and once WITH
//
//	go test -ldflags "-X main.pluginVariantPin=intl" -run TestSplitBuildPinWiring
//
// (split flavor: the default must still be intl).
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
			t.Fatalf("raw %q: got %q, want intl default (pin %q ignored)", raw, got, pluginVariantPin)
		}
	}
}

// TestSplitCredentialFileName pins the split-build credential namespace: the
// registered id already encodes the variant, so no variant infix may be
// appended (would produce "trae-intl-intl-<uid>.json").
func TestSplitCredentialFileName(t *testing.T) {
	if providerName == "trae" {
		if got := credentialFileName(variantIntl, "u1"); got != "trae-intl-u1.json" {
			t.Fatalf("unified intl file = %q", got)
		}
		if got := credentialFileName(variantSolo, "u1"); got != "trae-solo-cn-u1.json" {
			t.Fatalf("unified solo file = %q", got)
		}
		return
	}
	// Split build: id encodes the variant.
	if got := credentialFileName(variantIntl, "u1"); got != providerName+"-u1.json" {
		t.Fatalf("split file = %q, want %q", got, providerName+"-u1.json")
	}
}
