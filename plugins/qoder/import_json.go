// import_json.go implements JSON credential import on POST <base>/import.
//
// Third-party export format (array of entries; a single object is accepted
// as a one-entry batch). Two shapes, distinguished WITHOUT guessing:
//
//	CN entry:   id like qoder_cn_app_uid_<uid>, variant like qoder_cn_ide,
//	            shared_refresh_token drt-, client_auth.qoder_cn_app{token dt-,
//	            user{id,name,phone}, avatarUrl qoder.com.cn},
//	            auth_user_info_raw{token, refreshToken, ...}.
//	Intl entry: id like qoder_uid_<uid>, NO variant,
//	            client_auth.qoder_app{token, user{...}}, avatar qoder.com,
//	            legacy_ids, auth_user_info_raw{token, refreshToken,
//	            securityOauthToken, ...}.
//
// REGION RULE (evaluated in order, CN signals first, never guess):
//
//	1. variant containing _cn_ (or == cn) → cn.
//	2. avatarUrl/user avatar containing qoder.com.cn → cn.
//	3. client_auth key qoder_cn_app or id prefix qoder_cn_ → cn.
//	4. client_auth key qoder_app / avatar qoder.com (no .cn) / id prefix
//	   qoder_uid_ / qoder_app_ → intl.
//	5. else → per-entry refuse (unknown region, nothing stored for it).
//
// TOKEN RULE:
//
//	accessToken  = auth_user_info_raw.token if present else client_auth.*.token.
//	refreshToken = shared_refresh_token else auth_user_info_raw.refreshToken.
//	uid          = user_id else client_auth user.id.
//	nickname     = display_name else client_auth user.name.
//	Missing uid/token/refresh → per-entry error, continue with others.
//
// Persistence reuses the SAME funnel as PAT import
// (buildStoredAuthFromDeviceToken + persistPanelLoginAuth → buildAuthFileJSON
// + hostAuthSaveJSONFn) so filenames stay canonical qoder-<region>-<uid>.json.
// No live-network validation: imported tokens are accepted as-is;
// keepalive/refresh owns freshness afterwards.
//
// SECURITY: tests use SYNTHETIC fixtures only (dt-TEST..., drt-TEST...).
// Never paste real user tokens into tests or comments.
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// jsonImportClientAuth is one client_auth.* value: the live token pair plus
// the identity/avatar the region sniff and token fallback read.
type jsonImportClientAuth struct {
	Token     string `json:"token"`
	AvatarURL string `json:"avatarUrl"`
	Avatar    string `json:"avatar"`
	User      struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Phone     string `json:"phone"`
		Avatar    string `json:"avatar"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"user"`
}

// jsonImportRawInfo is auth_user_info_raw: the preferred token source.
type jsonImportRawInfo struct {
	Token           string `json:"token"`
	RefreshToken    string `json:"refreshToken"`
	RefreshTokenAlt string `json:"refresh_token"`
	// securityOauthToken and friends are carried by Intl dumps; parsed to
	// tolerate the shape, never used as credentials.
	SecurityOauthToken string `json:"securityOauthToken"`
}

// jsonImportEntry is one third-party export entry. Unknown keys (legacy_ids
// and the rest of the dump) are ignored.
type jsonImportEntry struct {
	ID                 string                     `json:"id"`
	Variant            string                     `json:"variant"`
	UserID             string                     `json:"user_id"`
	DisplayName        string                     `json:"display_name"`
	SharedRefreshToken string                     `json:"shared_refresh_token"`
	ClientAuth         map[string]json.RawMessage `json:"client_auth"`
	AuthUserInfoRaw    json.RawMessage            `json:"auth_user_info_raw"`
}

// jsonImportResult is the per-entry report: {uid, region, ok, error}
// (+ file on success for panel display).
type jsonImportResult struct {
	UID    string `json:"uid"`
	Region string `json:"region"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	File   string `json:"file,omitempty"`
}

// parseImportRegion normalizes an explicit region param (cn|intl, default
// def). Anything else is a 400 — never guess.
func parseImportRegion(v, def string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return def, nil
	}
	if v == regionCN || v == regionIntl {
		return v, nil
	}
	return "", &importRegionError{region: strings.TrimSpace(v)}
}

type importRegionError struct{ region string }

func (e *importRegionError) Error() string {
	return "invalid region " + jsonStringOf(e.region) + ": want \"cn\" or \"intl\""
}

func jsonStringOf(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// detectJSONImportRegion applies the REGION RULE in order. ok=false means
// "unknown region — refuse, never guess".
func detectJSONImportRegion(e *jsonImportEntry) (region string, ok bool) {
	id := strings.ToLower(strings.TrimSpace(e.ID))
	variant := strings.ToLower(strings.TrimSpace(e.Variant))

	// CN signals (checked first, in rule order).
	if variant == regionCN || strings.Contains(variant, "_cn_") {
		return regionCN, true
	}
	for _, av := range jsonImportAvatars(e) {
		if strings.Contains(strings.ToLower(av), "qoder.com.cn") {
			return regionCN, true
		}
	}
	for k := range e.ClientAuth {
		if strings.Contains(strings.ToLower(strings.TrimSpace(k)), "qoder_cn_app") {
			return regionCN, true
		}
	}
	if strings.HasPrefix(id, "qoder_cn_") {
		return regionCN, true
	}

	// Intl signals.
	for k := range e.ClientAuth {
		if strings.Contains(strings.ToLower(strings.TrimSpace(k)), "qoder_app") {
			return regionIntl, true
		}
	}
	for _, av := range jsonImportAvatars(e) {
		a := strings.ToLower(av)
		if strings.Contains(a, "qoder.com") && !strings.Contains(a, "qoder.com.cn") {
			return regionIntl, true
		}
	}
	if strings.HasPrefix(id, "qoder_uid_") || strings.HasPrefix(id, "qoder_app_") {
		return regionIntl, true
	}
	return "", false
}

// jsonImportAvatars collects every avatarUrl/avatar (top-level and nested
// under user) across all client_auth values for the region sniff.
func jsonImportAvatars(e *jsonImportEntry) []string {
	var out []string
	for _, raw := range e.ClientAuth {
		var v jsonImportClientAuth
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		out = append(out, v.AvatarURL, v.Avatar, v.User.AvatarURL, v.User.Avatar)
	}
	return out
}

// jsonImportClientAuthSorted parses client_auth values in deterministic key
// order (Go map iteration is random; token fallback must be stable).
func jsonImportClientAuthSorted(e *jsonImportEntry) ([]string, []*jsonImportClientAuth) {
	keys := make([]string, 0, len(e.ClientAuth))
	for k := range e.ClientAuth {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]*jsonImportClientAuth, 0, len(keys))
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		var v jsonImportClientAuth
		if err := json.Unmarshal(e.ClientAuth[k], &v); err != nil {
			continue
		}
		names = append(names, k)
		vv := v
		vals = append(vals, &vv)
	}
	return names, vals
}

// mapJSONImportEntry applies the TOKEN RULE and persists one entry through
// the shared panel funnel. It returns the per-entry report; a report with
// OK=false stores nothing.
func mapJSONImportEntry(raw json.RawMessage) jsonImportResult {
	var e jsonImportEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return jsonImportResult{OK: false, Error: "entry parse: " + err.Error()}
	}
	region, ok := detectJSONImportRegion(&e)
	if !ok {
		// Refuse without guessing; still report the uid when recoverable.
		uid, _, _, _ := extractJSONImportTokens(&e)
		return jsonImportResult{UID: uid, OK: false, Error: "unknown region: cannot determine cn/intl from variant/avatar/client_auth/id"}
	}
	uid, access, refresh, nickname := extractJSONImportTokens(&e)
	rep := jsonImportResult{UID: uid, Region: region}
	switch {
	case strings.TrimSpace(uid) == "":
		rep.Error = "missing uid (user_id and client_auth user.id both empty)"
		return rep
	case strings.TrimSpace(access) == "":
		rep.Error = "missing accessToken (auth_user_info_raw.token and client_auth token both empty)"
		return rep
	case strings.TrimSpace(refresh) == "":
		rep.Error = "missing refreshToken (shared_refresh_token and auth_user_info_raw.refreshToken both empty)"
		return rep
	}
	tok := &deviceTokenResponse{Token: access, RefreshToken: refresh, UserID: uid}
	sa := buildStoredAuthFromDeviceToken(tok, &userInfoResponse{ID: uid, Name: nickname}, region)
	name, err := persistPanelLoginAuth(sa)
	if err != nil {
		rep.Error = "persist auth: " + err.Error()
		return rep
	}
	rep.OK = true
	rep.File = name
	return rep
}

// extractJSONImportTokens applies the TOKEN RULE: raw token preferred,
// client_auth fallback (region-matched key first, then key-sorted order);
// uid = user_id else user.id; nickname = display_name else user.name.
func extractJSONImportTokens(e *jsonImportEntry) (uid, access, refresh, nickname string) {
	var info jsonImportRawInfo
	if len(e.AuthUserInfoRaw) > 0 {
		_ = json.Unmarshal(e.AuthUserInfoRaw, &info)
	}
	access = strings.TrimSpace(info.Token)
	refresh = strings.TrimSpace(info.RefreshToken)
	if refresh == "" {
		refresh = strings.TrimSpace(info.RefreshTokenAlt)
	}
	uid = strings.TrimSpace(e.UserID)
	nickname = strings.TrimSpace(e.DisplayName)

	names, vals := jsonImportClientAuthSorted(e)
	// Prefer the region-matched key for the access fallback so a dump
	// carrying both families resolves deterministically.
	ordered := make([]*jsonImportClientAuth, 0, len(vals))
	region, regionOK := detectJSONImportRegion(e)
	for i, n := range names {
		k := strings.ToLower(n)
		if regionOK && ((region == regionCN && strings.Contains(k, "qoder_cn_app")) ||
			(region == regionIntl && strings.Contains(k, "qoder_app"))) {
			ordered = append(ordered, vals[i])
		}
	}
	for _, v := range vals {
		dup := false
		for _, o := range ordered {
			if o == v {
				dup = true
				break
			}
		}
		if !dup {
			ordered = append(ordered, v)
		}
	}
	for _, v := range ordered {
		if access == "" && strings.TrimSpace(v.Token) != "" {
			access = strings.TrimSpace(v.Token)
		}
		if uid == "" && strings.TrimSpace(v.User.ID) != "" {
			uid = strings.TrimSpace(v.User.ID)
		}
		if nickname == "" && strings.TrimSpace(v.User.Name) != "" {
			nickname = strings.TrimSpace(v.User.Name)
		}
	}
	// TOKEN RULE precedence: shared_refresh_token wins over raw refresh.
	refresh = strings.TrimSpace(e.SharedRefreshToken)
	if refresh == "" {
		refresh = strings.TrimSpace(info.RefreshToken)
	}
	if refresh == "" {
		refresh = strings.TrimSpace(info.RefreshTokenAlt)
	}
	return uid, access, refresh, nickname
}

// handleImportJSON runs the JSON path: body is one entry object or an array
// of entries. Per-entry errors never abort the batch. HTTP 400 iff nothing
// was imported; 200 otherwise (success=true only when every entry landed).
func handleImportJSON(body []byte) (int, map[string]any) {
	trimmed := strings.TrimSpace(string(body))
	var raws []json.RawMessage
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &raws); err != nil {
			return http.StatusBadRequest, map[string]any{"success": false, "error": "invalid JSON array: " + err.Error()}
		}
		if len(raws) == 0 {
			return http.StatusBadRequest, map[string]any{"success": false, "error": "empty JSON array: nothing to import"}
		}
	} else {
		var one json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &one); err != nil {
			return http.StatusBadRequest, map[string]any{"success": false, "error": "invalid JSON object: " + err.Error()}
		}
		raws = []json.RawMessage{one}
	}
	results := make([]jsonImportResult, 0, len(raws))
	imported := 0
	for _, r := range raws {
		rep := mapJSONImportEntry(r)
		if rep.OK {
			imported++
		}
		results = append(results, rep)
	}
	failed := len(results) - imported
	payload := map[string]any{
		"success":  failed == 0,
		"imported": imported,
		"failed":   failed,
		"results":  results,
	}
	if imported == 0 {
		return http.StatusBadRequest, payload
	}
	return http.StatusOK, payload
}

// handleImport is the POST <base>/import dispatcher. The trimmed body
// decides the path: '[' / '{' → JSON credential path; 'pt-' → raw PAT path;
// a {"pat": ...} wrapper object → PAT path (panel shape, with optional
// {region}). Anything else is a 400.
func handleImport(req pluginapi.ManagementRequest) (int, map[string]any) {
	trimmed := strings.TrimSpace(string(req.Body))
	if strings.HasPrefix(trimmed, "pt-") {
		region := ""
		if vals := req.Query["region"]; len(vals) > 0 {
			region = vals[0]
		}
		return handleImportPATRaw(trimmed, region)
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		// The panel PAT wrapper is also '{'-led — route it to PAT first.
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &probe); err == nil {
			if rawPAT, hasPAT := probe["pat"]; hasPAT {
				var pat string
				if err := json.Unmarshal(rawPAT, &pat); err == nil {
					return handleImportPAT(req)
				}
				// "pat" present but not a string → fall through to PAT
				// validation so the caller gets the canonical error.
				return handleImportPAT(req)
			}
		}
		return handleImportJSON(req.Body)
	}
	return http.StatusBadRequest, map[string]any{"success": false, "error": "empty import body: paste a PAT (pt-...) or exported JSON ([...] / {...})"}
}
