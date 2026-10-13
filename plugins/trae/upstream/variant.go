// variant.go — per-region platform constants for the merged trae plugin.
//
// Regions follow the B-plan classification: "trae-cn" (suffixed CN) and
// "trae" (bare = international). The legacy pre-B-plan spellings ("cn",
// "intl") stay readable as aliases of the same values.
//
// The retired solo spellings ("solo", "solo-intl") keep their SOLO-lineage
// values (en1oxy7wnw8j9n client, trae_solo_cn platform): already-stored
// solo credentials carry those values, and resolving them to the wrong
// lineage would poison refresh/exchange with a cross-lineage token
// (HTTP 401 code 10101). Routing of those accounts still falls back to
// the trae-cn lane (main.normalizeVariant); only the lineage-sensitive
// identities stay solo here so stored accounts never crash.
package upstream

import "strings"

// clientIDByVariant / functionByVariant map account region → platform values
// (constants verified upstream; 禁止改动).
//
// v0.12.44: aligned with cockpit-tools' 2×2 lineage matrix
// (trae_account_core_platform_storage.rs:196-199) — the ClientID depends ONLY
// on solo-ness (en1oxy7wnw8j9n solo / ono9krqynydwx5 non-solo), the auth
// domain only on CN-ness (www.trae.cn / www.trae.ai).
//
// v0.12.79 (issue #9): the function map deviates from the official client on
// purpose. llm_utils_chat rejects EVERY function value except solo_work_lite
// with in-stream 4001 "param is invalid" — corroborated by trae2api-web
// RESEARCH.md ("其他 function 值如 work/solo/work_lite 均无效"), trae2api-more
// IsModelConfigMismatch (4001 = model not available under current function)
// and the reporter's region-flip experiment (same trae-cn JWT, only the function
// changed, SOLO catalog lit up). The old inline_chat lane was dead: every
// model 4001'd. trae-cn now rides solo_work_lite (reporter: 2 CN accounts, 11
// models chat-verified usable). trae accounts never reach llm_utils_chat at
// all — they use the Web SOLO chat_sessions protocol (intlupstream module) —
// so their entry is unreachable from the chat path; it is aligned to
// solo_work_lite anyway so this map can never emit a dead function value
// again.
var (
	clientIDByVariant = map[string]string{
		"trae-cn":   "ono9krqynydwx5", // non-solo (Trae CN)
		"cn":        "ono9krqynydwx5", // legacy pre-B-plan spelling
		"solo":      "en1oxy7wnw8j9n", // retired SOLO lineage (stored files)
		"trae":      "ono9krqynydwx5", // TRAE intl — same non-solo client id (cockpit TRAE_AUTH_CLIENT_ID)
		"intl":      "ono9krqynydwx5", // legacy pre-B-plan spelling
		"solo-intl": "en1oxy7wnw8j9n", // retired SOLO intl lineage (stored files)
	}
	functionByVariant = map[string]string{
		"trae-cn":   "solo_work_lite", // v0.12.79 (issue #9): llm_utils_chat's only live function
		"cn":        "solo_work_lite",
		"solo":      "solo_work_lite",
		"trae":      "solo_work_lite", // unreachable from chat (intlupstream chat_sessions); kept live
		"intl":      "solo_work_lite",
		"solo-intl": "solo_work_lite",
	}
)

// platformByVariant maps region → official platformId / platformName
// (values cross-checked against a live cockpit-tools export of a
// TRAE SOLO CN account: platformId=trae_solo_cn, platformName="TRAE SOLO CN";
// provider_key scheme: trae_account_core_product_paths.rs:654-658).
var (
	platformIDByVariant = map[string]string{
		"trae-cn":   "trae_cn",
		"cn":        "trae_cn",
		"solo":      "trae_solo_cn",
		"trae":      "trae",
		"intl":      "trae",
		"solo-intl": "trae_solo_intl",
	}
	platformNameByVariant = map[string]string{
		"trae-cn":   "TRAE CN",
		"cn":        "TRAE CN",
		"solo":      "TRAE SOLO CN",
		"trae":      "TRAE",
		"intl":      "TRAE",
		"solo-intl": "TRAE SOLO",
	}
)

// PlatformIDFor returns the official platformId for a region (default
// trae-cn lineage). Used to stamp credential files with the explicit
// lineage cockpit-tools exports carry, so a credential is self-describing.
func PlatformIDFor(variant string) string {
	if v, ok := platformIDByVariant[variant]; ok {
		return v
	}
	return platformIDByVariant["trae-cn"]
}

// PlatformNameFor returns the human platform name for a region.
func PlatformNameFor(variant string) string {
	if v, ok := platformNameByVariant[variant]; ok {
		return v
	}
	return platformNameByVariant["trae-cn"]
}

// IsSoloVariant reports whether the region belongs to the retired SOLO
// lineage (client id en1oxy7wnw8j9n). Mirrors cockpit
// TraePlatformKind::is_solo. Only stored solo credentials match now; it
// keeps their lineage-sensitive paths (exchange ladder, checkin req_source,
// UA) on the solo identity instead of crashing or cross-poisoning.
func IsSoloVariant(variant string) bool {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "solo", "solo-intl":
		return true
	}
	return false
}

// IsIntlVariant reports whether the region belongs to the non-CN side
// (auth domain www.trae.ai). Mirrors cockpit TraePlatformKind::is_cn inverse.
func IsIntlVariant(variant string) bool {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "trae", "intl", "trae-intl", "solo-intl":
		return true
	}
	return false
}

// ClientIDFor returns the OAuth client id for a region (default trae-cn).
func ClientIDFor(variant string) string {
	if v, ok := clientIDByVariant[variant]; ok {
		return v
	}
	return clientIDByVariant["trae-cn"]
}

// FunctionFor returns the llm_utils_chat function value for a region.
func FunctionFor(variant string) string {
	if v, ok := functionByVariant[variant]; ok {
		return v
	}
	return functionByVariant["trae-cn"]
}
