package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// These are the externally observable login parameters of the reference
// qoderwork/qoder-ai flows, independent of this plugin's config constants.
func TestReferenceDeviceLoginURL(t *testing.T) {
	oldDialect, oldIdentity, oldUMID := loadedLoginDialect(), machineIdentityOverride, umidHTTPDo
	t.Cleanup(func() {
		setLoginDialect(oldDialect)
		machineIdentityOverride, umidHTTPDo = oldIdentity, oldUMID
	})
	machineIdentityOverride = &machineIdentity{MachineID: "legacy-machine-must-not-be-used"}
	umidHTTPDo = func(*http.Request) (*hostHTTPResponse, error) {
		t.Fatal("starting authorization must not acquire UMID")
		return nil, nil
	}
	for _, tc := range []struct{ region, host, redirect string }{
		{"cn", "qoder.com.cn", "qoder-work-cn://"},
		{"intl", "qoder.com", "qoder-work://"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			for _, dialect := range []string{loginDialectCockpit, loginDialectDesktop} {
				setLoginDialect(dialect)
				out, err := startLoginWithRegion(nil, tc.region)
				if err != nil {
					t.Fatal(err)
				}
				rawURL, state, _ := decodeLoginStart(t, out)
				defer loginStates.Delete(state)
				u, err := url.Parse(rawURL)
				if err != nil {
					t.Fatal(err)
				}
				if u.Scheme != "https" || u.Host != tc.host || u.Path != "/device/selectAccounts" {
					t.Fatalf("unexpected authorization entry: %s", rawURL)
				}
				q := u.Query()
				if len(q) != 6 || q.Get("client_id") != "1c5e33e1-364d-4ce6-b02c-acaa81274a5c" || q.Get("redirect_uri") != tc.redirect || q.Get("challenge_method") != "S256" {
					t.Fatal("authorization parameters differ from the reference")
				}
				for _, field := range []string{"nonce", "machine_id"} {
					id, err := uuid.Parse(q.Get(field))
					if err != nil || id.String() != q.Get(field) || id.Version() != 4 {
						t.Fatalf("%s must be a dashed random UUID", field)
					}
				}
				if q.Get("nonce") == q.Get("machine_id") {
					t.Fatal("nonce and machine ID must be independent")
				}
				v, ok := loginStates.Load(state)
				if !ok {
					t.Fatal("login state missing")
				}
				lc := v.(*loginCtx)
				sum := sha256.Sum256([]byte(lc.verifier))
				if len(lc.verifier) != 64 || q.Get("challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || lc.nonce != q.Get("nonce") || lc.region != tc.region {
					t.Fatal("PKCE challenge and pending state do not match the authorization URL")
				}
			}
		})
	}
}

func TestReferenceDevicePollCompletesOnlyItsGrant(t *testing.T) {
	for _, tc := range []struct{ region, host, userAgent string }{
		{"cn", "openapi.qoder.com.cn", "QoderWork"},
		{"intl", "openapi.qoder.sh", "Qoder AI"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			out, err := startLoginWithRegion(nil, tc.region)
			if err != nil {
				t.Fatal(err)
			}
			_, state, _ := decodeLoginStart(t, out)
			defer loginStates.Delete(state)
			v, _ := loginStates.Load(state)
			lc := v.(*loginCtx)
			calls := 0
			stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Host != tc.host || r.URL.Path != "/api/v1/deviceToken/poll" {
					t.Fatal("login made an unexpected request instead of immediately returning the grant")
				}
				if r.URL.Query().Get("nonce") != lc.nonce || r.URL.Query().Get("verifier") != lc.verifier || r.URL.Query().Get("challenge_method") != "S256" || r.Header.Get("User-Agent") != tc.userAgent || r.Header.Get("Accept") != "application/json" {
					t.Fatal("device poll request differs from the reference")
				}
				return loginJSON(http.StatusOK, `{"token":"dt-reference-fixture","refresh_token":"drt-reference-fixture","user_id":"reference-fixture-user","expires_in":60000}`), nil
			})
			raw, _ := json.Marshal(pluginapi.AuthLoginPollRequest{State: state})
			result, err := handlePollLogin(raw)
			if err != nil {
				t.Fatal(err)
			}
			var env envelope
			if err := json.Unmarshal(result, &env); err != nil || !env.OK {
				t.Fatal("invalid poll envelope")
			}
			var poll pluginapi.AuthLoginPollResponse
			if err := json.Unmarshal(env.Result, &poll); err != nil {
				t.Fatal(err)
			}
			if poll.Status != pluginapi.AuthLoginStatusSuccess || poll.Auth.ID != "" || poll.Auth.FileName != "qoder-"+tc.region+"-reference-fixture-user.json" || calls != 1 {
				t.Fatal("grant must return once with a host-derived registration ID and region-specific filename")
			}
			var saved storedAuth
			if err := json.Unmarshal(poll.Auth.StorageJSON, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Auth.Region != tc.region || saved.Auth.AccessToken != "dt-reference-fixture" || saved.Auth.RefreshToken != "drt-reference-fixture" || saved.Account.UID != "reference-fixture-user" {
				t.Fatal("grant credentials or region were changed")
			}
			if _, exists := loginStates.Load(state); exists {
				t.Fatal("completed login state was not consumed")
			}
		})
	}
}
