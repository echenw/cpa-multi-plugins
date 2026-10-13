// split_build.go — split-channel flavor support (issue #29).
//
// The repo ships TWO release packages per platform from the SAME source:
//
//   - cpa-multi-plugins-<os>-<arch>.zip — the unified flavor (default):
//     one plugin per provider merging cn/intl/global realms behind a single
//     identifier with per-account realm config.
//   - cpa-multi-plugins-split-<os>-<arch>.zip — the split-channel flavor:
//     the legacy per-variant plugins (codebuddy-cn / codebuddy-intl, plus
//     the qoder/trae equivalents), each registering as its own panel entry
//     with its own OAuth login pinned to its realm.
//
// A workbuddy split build is produced purely at BUILD TIME — no source fork:
//
//		go build -ldflags "-X main.providerName=codebuddy-intl -X main.pluginVariantPin=intl"
//
//	  - providerName (var since the split flavor) → the registered plugin id,
//	    auth-file namespace and panel entry.
//	  - pluginVariantPin → build-time realm pin, retained only so split
//	    -ldflags injections still link; it no longer defaults NEW logins.
//	    Per-account realm routing (cn / global / intl) is untouched, and NEW
//	    logins resolve their realm from the request only (default intl).
//
// Empty values (the unified flavor default) keep behavior byte-for-byte.
package main

// pluginVariantPin is the build-time realm pin ("cn" / "intl"); empty for
// the unified flavor. Injected via -ldflags -X. See the file comment.
var pluginVariantPin = ""

// providerName is a VAR (split flavor, issue #29): the unified build keeps
// the default "workbuddy"; the split-channel builds rename it via
// -ldflags "-X main.providerName=codebuddy-intl". See the file comment.
var providerName = "workbuddy"

func init() {
	applyVariantPin()
}

// applyVariantPin validates the build-time pin. Config-file region switching
// is deleted, so the pin no longer seeds any login default: NEW logins
// always resolve from the request (default intl). Called from init() and
// directly from tests.
func applyVariantPin() {
	switch pluginVariantPin {
	case "", "cn", "intl":
		return
	default:
		panic("workbuddy: invalid pluginVariantPin " + pluginVariantPin)
	}
}
