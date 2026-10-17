// credits_handler.go implements the management API endpoints that mutate or
// read account state: import credential, toggle check-in, claim trial, select
// active auth, and query credits for one account or all.
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// handleImportPAT accepts a raw PAT string (pt-...), exchanges it for a
// jobToken pair, fetches userinfo, and persists via the shared panel funnel
// (persistPanelLoginAuth → buildAuthFileJSON + hostAuthSaveJSONFn).
// This is the primary onboarding path for QoderWork — PATs are created on
// qoder.com.cn by the user and pasted into the panel.
// The body carries an optional {region} (cn|intl, default intl = the
// historical behavior); exchange + record land under that region. An
// invalid region is a 400 with nothing stored.
func handleImportPAT(req pluginapi.ManagementRequest) (int, map[string]any) {
	var body struct {
		PAT    string `json:"pat"`
		Region string `json:"region"`
	}
	_ = json.Unmarshal(req.Body, &body)
	return handleImportPATRaw(strings.TrimSpace(body.PAT), body.Region)
}

// handleImportPATRaw is handleImportPAT after body decoding: it also serves
// raw pasted PAT bodies (the /import dispatcher sniffs a leading pt-).
func handleImportPATRaw(pat, regionParam string) (int, map[string]any) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return http.StatusBadRequest, map[string]any{"success": false, "error": "missing pat field"}
	}
	if !strings.HasPrefix(pat, "pt-") {
		return http.StatusBadRequest, map[string]any{"success": false, "error": "PAT must start with pt-"}
	}
	region, err := parseImportRegion(regionParam, regionIntl)
	if err != nil {
		return http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()}
	}
	tok, err := exchangePATForJobToken(pat, region)
	if err != nil {
		return http.StatusOK, map[string]any{"success": false, "error": "jobToken exchange: " + err.Error()}
	}
	ui, _ := fetchUserInfo(tok.Token, region) // best-effort
	sa := buildStoredAuthFromJobToken(pat, tok, ui, region)

	name, err := persistPanelLoginAuth(sa)
	if err != nil {
		return http.StatusOK, map[string]any{"success": false, "error": "persist auth: " + err.Error()}
	}
	return http.StatusOK, map[string]any{
		"success":  true,
		"name":     name,
		"uid":      sa.Account.UID,
		"nickname": sa.Account.Nickname,
		"region":   region,
		"file":     name,
	}
}

func handleCheckinConfig(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.Unmarshal(req.Body, &body)
	checkinAutoMu.Lock()
	if body.Enabled != nil {
		// Runtime-only toggle: the CPA host exposes no plugin-config write
		// callback, so persisting would mean editing the host's config.yaml
		// from inside the plugin (fragile under docker volume mounts). The
		// value from config_yaml wins again on CPA restart.
		checkinAuto = *body.Enabled
	}
	cur := checkinAuto
	checkinAutoMu.Unlock()
	return map[string]any{"checkin_auto": cur, "persistent": false}
}

// handleSelectAuth sets the panel-selected account used for chat routing.
// Region is always CN for QoderWork.
func handleSelectAuth(req pluginapi.ManagementRequest) map[string]any {
	var body struct {
		AuthIndex string `json:"auth_index"`
	}
	_ = json.Unmarshal(req.Body, &body)
	authIndex := strings.TrimSpace(body.AuthIndex)
	if authIndex == "" {
		return map[string]any{"error": "auth_index is required", "active_auth": getActiveAuthID()}
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	for _, f := range files {
		if f.AuthIndex != authIndex {
			continue
		}
		if f.Disabled {
			return map[string]any{"error": "账号已禁用，无法选中", "auth_index": authIndex}
		}
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil {
			return map[string]any{"error": err.Error(), "auth_index": authIndex}
		}
		setActiveAuthID(f.ID)
		return map[string]any{
			"ok":          true,
			"active_auth": f.ID,
			"region":      authRegion(sa),
			"nickname":    sa.Account.Nickname,
			"uid":         sa.Account.UID,
		}
	}
	return map[string]any{"error": "account not found", "auth_index": authIndex}
}

// handleCreditsQuery returns real-time credits for one or all accounts.
// Pass ?auth_index=<idx> to query a single account; omit for all.
// Single-account mode returns full account info (nickname, region, credits,
// exhausted, trial_claimed) so the panel can update one card without
// reloading the entire dashboard.
func handleCreditsQuery(req pluginapi.ManagementRequest) map[string]any {
	authIndex := ""
	if vals := req.Query["auth_index"]; len(vals) > 0 {
		authIndex = strings.TrimSpace(vals[0])
	}
	files, err := hostAuthList()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	// Single-account: return one full account row (like dashboard entry).
	if authIndex != "" {
		for _, f := range files {
			if f.AuthIndex != authIndex {
				continue
			}
			sa, err := hostAuthGet(f.AuthIndex)
			if err != nil {
				return map[string]any{"accounts": []map[string]any{{
					"auth_index": authIndex, "error": "load auth: " + err.Error(),
				}}}
			}
			profileError := ""
			forceProfile := len(req.Query["refresh_profile"]) > 0 && req.Query["refresh_profile"][0] == "1"
			if err := completeAccountProfile(f.AuthIndex, sa, forceProfile); err != nil {
				profileError = err.Error()
			}
			cr, err := fetchUserResource(sa)
			acct := map[string]any{
				"auth_index":     authIndex,
				"nickname":       sa.Account.Nickname,
				"email":          sa.Account.Email,
				"profile_loaded": sa.Account.ProfileLoaded,
				"profile_error":  profileError,
				"uid":            sa.Account.UID,
				"region":         authRegion(sa),
				"name":           f.Name,
				"label":          f.Label,
				"disabled":       f.Disabled,
				"selected":       getActiveAuthID() == f.ID,
			}
			if err != nil {
				acct["error"] = err.Error()
			} else {
				acct["credits"] = cr
				acct["exhausted"] = isCreditsExhausted(cr)
				// Also fetch plan so the badge updates on lazy load.
				acct["plan"] = fetchPaymentType(sa)
				// Update cache so subsequent dashboard loads see fresh data.
				now := time.Now()
				if cr != nil {
					cr.FetchedAt = now.UTC().Format(time.RFC3339)
				}
				// Merge into existing cache entry (keep checkin if present).
				var prev *accountCacheEntry
				if v, ok := accountCache.Load(f.ID); ok {
					prev, _ = v.(*accountCacheEntry)
				}
				var ci *checkinSummary
				if prev != nil {
					ci = prev.checkin
				}
				plan, _ := acct["plan"].(string)
				accountCache.Store(f.ID, &accountCacheEntry{
					checkin: ci, credits: cr, plan: plan, fetched: now,
				})
				// Propagate the fresh credits into the host auth-file note so
				// CPA's native credential card matches this panel. Without it a
				// lazy /credits refresh updated the panel while the native card
				// stayed on a stale note.
				_ = syncAuthNote(f.AuthIndex, f.ID, sa, cr, f.Disabled)
			}
			return map[string]any{"accounts": []map[string]any{acct}}
		}
		return map[string]any{"error": "account not found"}
	}
	// All accounts: return simplified list.
	type acctCredits struct {
		AuthIndex     string          `json:"auth_index"`
		Nickname      string          `json:"nickname"`
		Email         string          `json:"email"`
		ProfileLoaded bool            `json:"profile_loaded"`
		ProfileError  string          `json:"profile_error,omitempty"`
		UID           string          `json:"uid"`
		Credits       *creditsSummary `json:"credits,omitempty"`
		Error         string          `json:"error,omitempty"`
	}
	var out []acctCredits
	for _, f := range files {
		sa, err := hostAuthGet(f.AuthIndex)
		if err != nil {
			out = append(out, acctCredits{AuthIndex: f.AuthIndex, Error: "load auth: " + err.Error()})
			continue
		}
		profileError := ""
		if err := completeAccountProfile(f.AuthIndex, sa, false); err != nil {
			profileError = err.Error()
		}
		cr, err := fetchUserResource(sa)
		ac := acctCredits{AuthIndex: f.AuthIndex, Nickname: sa.Account.Nickname, UID: sa.Account.UID,
			Email: sa.Account.Email, ProfileLoaded: sa.Account.ProfileLoaded, ProfileError: profileError}
		if err != nil {
			ac.Error = err.Error()
		} else {
			ac.Credits = cr
			// Throttled by lifecycleStateUnchanged inside syncAuthNote.
			_ = syncAuthNote(f.AuthIndex, f.ID, sa, cr, f.Disabled)
		}
		out = append(out, ac)
	}
	return map[string]any{"accounts": out}
}
