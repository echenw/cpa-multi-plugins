// region_switch.go — request-carried login region for NEW logins.
//
// The login realm for one login attempt comes from the request only
// (default global). Config-file region switching is deleted, the host popup
// carries no selector, and region selection lives in the management panel
// (POST <base>/login/start, default Global with a CN toggle).
//   - auth.login_start requests may carry one of the keys region /
//     login_region / variant (top-level JSON string). When present it wins;
//     when absent the login defaults to global.
//   - Only cn/global are accepted from the request. Any other non-empty value
//     (including values written by older builds) is an explicit error (no
//     config files are written by this path, ever).
//   - Per-account routing (realmForStorage / upstreamBaseForRegion) is
//     untouched: existing accounts keep the realm recorded at login/import
//     time. Stored accounts carrying a retired region value keep listing via
//     panelRegion's verbatim fallback; they are never deleted here.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultRequestLoginRegion is the login realm when the request names none.
const defaultRequestLoginRegion = regionGlobal

// parseLoginRegionField scans a raw auth.login_start request for a
// request-carried region. It returns (value, true, nil) when a supported key
// carries a valid cn/global value, ("", true, err) when a supported key
// carries any other non-empty value, and ("", false, nil) when no supported
// key is present (or the request is not a JSON object).
func parseLoginRegionField(raw []byte) (string, bool, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return "", false, nil
	}
	for _, key := range []string{"region", "login_region", "variant"} {
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
			case regionCN, regionGlobal:
				return v, true, nil
			default:
				return "", true, fmt.Errorf("invalid login region %q: want \"cn\" or \"global\" (default \"global\")", s)
			}
		}
	}
	return "", false, nil
}

// resolveRequestLoginRegion resolves the login realm for one login attempt
// from the request-carried region only (T24). Absent keys default to global.
func resolveRequestLoginRegion(raw []byte) (string, error) {
	if v, present, err := parseLoginRegionField(raw); present {
		if err != nil {
			return "", err
		}
		return v, nil
	}
	return defaultRequestLoginRegion, nil
}
