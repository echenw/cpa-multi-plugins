// split_build.go — split-channel flavor support (issue #29).
//
// The repo ships TWO release packages per platform from the SAME source:
//
//   - cpa-multi-plugins-<os>-<arch>.zip — the unified flavor (default):
//     one plugin per provider merging cn/intl/solo realms behind a single
//     identifier with per-account variant config.
//   - cpa-multi-plugins-split-<os>-<arch>.zip — the split-channel flavor:
//     the legacy per-variant plugins (trae-cn / trae-solo-cn / trae-intl,
//     plus the qoder/workbuddy equivalents), each registering as its own
//     panel entry with its own OAuth login pinned to its realm.
//
// A trae split build is produced purely at BUILD TIME — no source fork:
//
//	   go build -ldflags "-X main.providerName=trae-intl"
//
//	- providerName (var since the split flavor) → the registered plugin id,
//	  credential namespace and panel entry.
//	- pluginVariantPin is retained only so old -ldflags injections still
//	  link; it is ignored. The login realm comes only from the
//	  request-carried region (default intl). Per-account variants keep
//	  their own realm routing.
//
// Empty values (the unified flavor default) keep behavior byte-for-byte.
package main

// pluginVariantPin is the legacy build-time realm pin; ignored since
// config-file realm switching was deleted. Kept so existing -ldflags
// injections still link.
var pluginVariantPin = ""

// providerName is a VAR (split flavor, issue #29): the unified build keeps
// the default "trae"; the split-channel builds rename it via
// -ldflags "-X main.providerName=trae-intl" so the plugin registers as its
// own panel entry / credential namespace. See the file comment.
var providerName = "trae"

func init() {
}

// applyVariantPin is a no-op kept for test/call compatibility: the login
// realm no longer has a sticky default to seed.
func applyVariantPin() {
}
