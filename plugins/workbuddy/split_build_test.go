package main

import "testing"

// TestSplitBuildPinWiring verifies the split-channel build injection chain:
// config-file region switching is deleted, so the login realm always
// resolves from the request (default intl) regardless of the build-time pin.
// Run once WITHOUT ldflags (unified flavor: pin empty) and once WITH
//
//	go test -ldflags "-X main.pluginVariantPin=intl" -run TestSplitBuildPinWiring
//
// (split flavor: the pin is a validated no-op, the login default stays intl).
func TestSplitBuildPinWiring(t *testing.T) {
	switch pluginVariantPin {
	case "", "cn", "intl":
		applyVariantPin()
		got, err := resolveRequestLoginRegion([]byte(`{}`))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != regionIntl {
			t.Fatalf("pin=%q: default login realm = %q, want %q", pluginVariantPin, got, regionIntl)
		}
	default:
		t.Fatalf("invalid pin %q leaked through", pluginVariantPin)
	}
}

// TestSplitAuthFileName pins the split-build credential namespace: the
// registered id already encodes the realm, so no intl infix may be appended.
func TestSplitAuthFileName(t *testing.T) {
	if providerName == "workbuddy" {
		if got := authFileNameFor(&storedAuth{Account: storedAccount{UID: "u1"}, Auth: storedTokens{Domain: "www.codebuddy.ai"}}); got != "workbuddy-intl-u1.json" {
			t.Fatalf("unified intl file = %q", got)
		}
		return
	}
	if got := authFileNameFor(&storedAuth{Account: storedAccount{UID: "u1"}}); got != providerName+"-u1.json" {
		t.Fatalf("split file = %q, want %q", got, providerName+"-u1.json")
	}
}
