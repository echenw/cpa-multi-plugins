// split_build.go — split-channel flavor support (issue #29).
//
// The repo ships TWO release packages per platform from the SAME source:
//
//   - cpa-multi-plugins-<os>-<arch>.zip — the unified flavor (default):
//     one plugin per provider merging cn/intl/solo realms behind a single
//     identifier with per-account region config.
//   - cpa-multi-plugins-split-<os>-<arch>.zip — the split-channel flavor:
//     the legacy per-variant plugins (codebuddy-cn / codebuddy-intl /
//     qoder-cn / qoder-intl / trae-cn / trae-solo-cn / trae-intl), each
//     registering as its own panel entry with its own OAuth login pinned
//     to its realm (issue #29's request).
//
// A split build is produced purely at BUILD TIME — no source fork:
//
//		go build -ldflags "-X main.providerName=qoder-intl -X main.pluginVariantPin=intl"
//
//	  - providerName (var since the split flavor) → the registered plugin id,
//	    auth-file namespace and panel entry.
//	  - pluginVariantPin → retained only so existing -ldflags -X injections
//	    still link; it no longer affects login (login region comes only from
//	    the request-carried field, default intl).
//
// Empty values (the unified flavor default) keep behavior byte-for-byte.
package main

// pluginVariantPin is the build-time realm pin ("cn" / "intl"); empty for
// the unified flavor. Injected via -ldflags -X. See the file comment.
var pluginVariantPin = ""

func init() {
	applyVariantPin()
}

// applyVariantPin is a no-op: config-file region switching is deleted, so a
// build-time pin must not seed any stored login default. Kept (and still
// called from init) so split-channel -ldflags builds keep linking while the
// login path stays request-carried-only (default intl).
func applyVariantPin() {
	switch pluginVariantPin {
	case "", regionCN, regionIntl:
		return
	default:
		panic("qoder: invalid pluginVariantPin " + pluginVariantPin)
	}
}
