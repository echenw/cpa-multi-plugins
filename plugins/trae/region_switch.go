// region_switch.go — request-carried login region for login attempts.
//
// Each login attempt resolves its realm from the request (default intl):
//   - auth.login_start requests may carry one of the keys region /
//     login_region / login_variant / variant (top-level JSON string). When
//     present it wins; when absent the realm resolves to the intl default.
//   - Only cn/intl are accepted from the request. Any other non-empty value
//     is an explicit error (no config files are written by this path, ever).
//   - Per-account routing (requestVariantIsIntl / pollStateIsIntl /
//     sniffVariantFromJSON) is untouched: existing accounts keep the variant
//     recorded at login/adoption time.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultRequestLoginVariant is the login realm when the request carries
// no region.
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
