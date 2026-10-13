package main

import (
	"strings"
	"testing"
)

// TestSplitBuildPinWiring verifies the login default stays request-carried
// (default intl) in every build flavor: the build-time variant pin is
// retained only for -ldflags link compatibility and must not seed any stored
// login default (config-file region switching is deleted).
//
// Run once WITHOUT ldflags (unified flavor: pin empty) and once WITH
//
//	go test -ldflags "-X main.pluginVariantPin=intl" -run TestSplitBuildPinWiring
//
// (split flavor: the login default must still be intl).
func TestSplitBuildPinWiring(t *testing.T) {
	applyVariantPin() // must not panic for any valid pin
	switch pluginVariantPin {
	case "", regionCN, regionIntl:
	default:
		t.Fatalf("invalid pin %q leaked through", pluginVariantPin)
	}
	out, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin default: %v", err)
	}
	url, state, _ := decodeLoginStart(t, out)
	v, ok := loginStates.Load(state)
	if !ok {
		t.Fatal("login state not stored")
	}
	defer loginStates.Delete(state)
	if lc := v.(*loginCtx); lc.region != regionIntl {
		t.Fatalf("pin=%q: login default = %q, want %q (request-carried only)", pluginVariantPin, lc.region, regionIntl)
	}
	if !strings.Contains(url, "https://qoder.com/") {
		t.Fatalf("pin=%q: login URL = %q, want the intl realm", pluginVariantPin, url)
	}
}
