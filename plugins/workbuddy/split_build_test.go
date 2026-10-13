package main

import "testing"

// TestSplitBuildPinWiring verifies the split-channel build injection chain:
// config-file region switching is deleted, so the login realm always
// resolves from the request (default global) regardless of the build-time pin.
// Run once WITHOUT ldflags (unified flavor: pin empty) and once WITH
//
//	go test -ldflags "-X main.pluginVariantPin=cn" -run TestSplitBuildPinWiring
//
// (split flavor: the pin is a validated no-op, the login default stays global).
func TestSplitBuildPinWiring(t *testing.T) {
	switch pluginVariantPin {
	case "", "cn":
		applyVariantPin()
		got, err := resolveRequestLoginRegion([]byte(`{}`))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != regionGlobal {
			t.Fatalf("pin=%q: default login realm = %q, want %q", pluginVariantPin, got, regionGlobal)
		}
	default:
		t.Fatalf("invalid pin %q leaked through", pluginVariantPin)
	}
}

// TestSplitAuthFileName pins the split-build credential namespace: the
// registered id already encodes the channel, so no extra infix may be appended.
func TestSplitAuthFileName(t *testing.T) {
	if providerName == "workbuddy" {
		if got := authFileNameFor(&storedAuth{Account: storedAccount{UID: "u1"}, Auth: storedTokens{Domain: "www.workbuddy.ai"}}); got != "workbuddy-u1.json" {
			t.Fatalf("unified global file = %q", got)
		}
		return
	}
	if got := authFileNameFor(&storedAuth{Account: storedAccount{UID: "u1"}}); got != providerName+"-u1.json" {
		t.Fatalf("split file = %q, want %q", got, providerName+"-u1.json")
	}
}
