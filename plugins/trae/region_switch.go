// region_switch.go — request-carried login region for login attempts.
//
// Each login attempt resolves its region from the request (default trae):
//   - auth.login_start requests may carry one of the keys region /
//     login_region / login_variant / variant (top-level JSON string). When
//     present it wins; when absent the region resolves to the trae default.
//   - Only trae-cn/trae are accepted from the request (legacy cn/intl
//     spellings are tolerated onto the same regions). Any other non-empty
//     value — including the retired "solo" — is an explicit error (no
//     config files are written by this path, ever).
//   - Per-account routing (requestVariantIsIntl / pollStateIsIntl /
//     sniffVariantFromJSON) is untouched: existing accounts keep the region
//     recorded at login/adoption time, normalized by normalizeVariant.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultRequestLoginVariant is the login region when the request carries
// no region.
const defaultRequestLoginVariant = variantIntl

// parseLoginRegionField scans a raw auth.login_start request for a
// request-carried region. It returns (value, true, nil) when a supported key
// carries a valid trae-cn/trae value (legacy cn/intl tolerated), ("", true,
// err) when a supported key carries any other non-empty value (including
// the retired solo), and ("", false, nil) when no supported key is present
// (or the request is not a JSON object).
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
			raw := strings.TrimSpace(strings.Trim(s, "\"'"))
			v := strings.ToLower(raw)
			if v == "" {
				continue
			}
			switch v {
			case variantCN, variantIntl, "cn", "intl":
				return normalizeVariant(v), true, nil
			default:
				return "", true, fmt.Errorf("invalid login region %q: want \"trae-cn\" or \"trae\" (default \"trae\"); current selection unchanged", s)
			}
		}
	}
	return "", false, nil
}

// resolveRequestLoginVariant prefers the request-carried region and falls
// back to the intl default constant when the request carries none.
func resolveRequestLoginVariant(raw []byte) (string, error) {
	if v, present, err := parseLoginRegionField(raw); present {
		if err != nil {
			return "", err
		}
		return v, nil
	}
	return defaultRequestLoginVariant, nil
}
