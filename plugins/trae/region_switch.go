// region_switch.go — T24: request-carried login region + panel selector.
//
// The authorization popup selects its realm per login attempt (default intl,
// switchable to cn) instead of only via the config-file login_variant:
//   - auth.login_start requests may carry one of the keys region /
//     login_region / login_variant / variant (top-level JSON string). When
//     present it wins; when absent the sticky login_variant config stays the
//     fallback (including solo); an empty fallback resolves to intl.
//   - Only cn/intl are accepted from the request. Any other non-empty value
//     is an explicit error and the stored selection is left untouched (no
//     config files are written by this path, ever).
//   - The selector (options + default + current) is surfaced in the
//     login-start response Metadata and at GET <base>/login/region.
//   - Per-account routing (requestVariantIsIntl / pollStateIsIntl /
//     sniffVariantFromJSON) is untouched: existing accounts keep the variant
//     recorded at login/adoption time.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// loginRegionOptions is the exact selector surface: cn/intl only, default intl.
var loginRegionOptions = []string{variantCN, variantIntl}

// defaultRequestLoginVariant is the login realm when neither the request nor
// the sticky config names one.
const defaultRequestLoginVariant = variantIntl

// parseLoginRegionField scans a raw auth.login_start request for a
// request-carried region. It returns (value, true, nil) when a supported key
// carries a valid cn/intl value, ("", true, err) when a supported key
// carries any other non-empty value, and ("", false, nil) when no supported
// key is present (or the request is not a JSON object).
func parseLoginRegionField(raw []byte) (string, bool, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return "", false, nil
	}
	for _, key := range []string{"region", "login_region", "login_variant", "variant"} {
		for mk, mv := range m {
			if !strings.EqualFold(mk, key) {
				continue
			}
			var s string
			if err := json.Unmarshal(mv, &s); err != nil {
				s = strings.Trim(string(mv), "\"'")
			}
			v := strings.ToLower(strings.TrimSpace(strings.Trim(s, "\"'")))
			if v == "" {
				continue
			}
			switch v {
			case variantCN, variantIntl:
				return v, true, nil
			default:
				return "", true, fmt.Errorf("invalid login region %q: want \"cn\" or \"intl\" (default \"intl\"); current selection unchanged", s)
			}
		}
	}
	return "", false, nil
}

// resolveRequestLoginVariant prefers the request-carried region (T24) and
// falls back to the sticky login_variant config (login_variant stays as the
// fallback default only). An empty fallback resolves to intl.
func resolveRequestLoginVariant(raw []byte, fallback string) (string, error) {
	if v, present, err := parseLoginRegionField(raw); present {
		if err != nil {
			return "", err
		}
		return v, nil
	}
	if strings.TrimSpace(fallback) == "" {
		return defaultRequestLoginVariant, nil
	}
	return normalizeVariant(fallback), nil
}

// loginRegionSelector is the panel/login-start selector payload.
type loginRegionSelector struct {
	Options []string `json:"options"`
	Default string   `json:"default"`
	Current string   `json:"current"`
}

// loginRegionSelectorPayload reports the cn/intl options (default intl) plus
// the current sticky selection. Read-only: it never writes config files.
func loginRegionSelectorPayload() loginRegionSelector {
	return loginRegionSelector{
		Options: append([]string(nil), loginRegionOptions...),
		Default: defaultRequestLoginVariant,
		Current: normalizeVariant(loadedLoginVariant()),
	}
}

// withLoginRegionMetadata stamps the selector onto a login-start response
// Metadata map (login-start surface, no config writes).
func withLoginRegionMetadata(md map[string]any, current string) map[string]any {
	if md == nil {
		md = map[string]any{}
	}
	md["region"] = current
	md["region_options"] = append([]string(nil), loginRegionOptions...)
	md["region_default"] = defaultRequestLoginVariant
	return md
}
