package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestQoderProfilePreservesCredentialsAndOperatorFields(t *testing.T) {
	for _, nested := range []bool{false, true} {
		doc := map[string]any{
			"type": "qoder", "disabled": true, "priority": 8, "note": "operator note",
			"custom": json.RawMessage(`{"id":9007199254740993}`),
		}
		tokens := map[string]any{"accessToken": "dt-fixture", "refreshToken": "drt-current",
			"personalToken": "pt-fixture", "domain": "qoder.com", "expiresAt": 99}
		account := map[string]any{"uid": "fixture-user", "nickname": "u12345678", "enterpriseId": "team", "custom": "keep"}
		if nested {
			doc["auth"], doc["account"] = tokens, account
		} else {
			for k, v := range tokens {
				doc[k] = v
			}
			for k, v := range account {
				doc[k] = v
			}
		}
		raw, _ := json.Marshal(doc)
		sa, err := parseStored(raw)
		if err != nil {
			t.Fatal(err)
		}
		updated, current, err := mergeQoderAccountProfile(raw, sa,
			&userInfoResponse{ID: "fixture-user", Name: "Fixture", Email: " fixture@example.invalid "})
		if err != nil {
			t.Fatal(err)
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(raw, &before)
		_ = json.Unmarshal(updated, &after)
		for key, value := range before {
			if key == "account" || (!nested && key == "nickname") {
				continue
			}
			if string(after[key]) != string(value) {
				t.Fatalf("nested=%v profile changed %s", nested, key)
			}
		}
		saved, err := parseStored(updated)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Account.Email != "fixture@example.invalid" || !saved.Account.ProfileLoaded || saved.Account.Nickname != "Fixture" ||
			current.Account != saved.Account || string(after["email"]) != `"fixture@example.invalid"` {
			t.Fatal("profile was not recorded in storage and host metadata")
		}
		if nested {
			var a map[string]json.RawMessage
			_ = json.Unmarshal(after["account"], &a)
			if string(a["custom"]) != `"keep"` || string(a["enterpriseId"]) != `"team"` {
				t.Fatal("profile erased existing account fields")
			}
		}
		ad := toAuthData(saved)
		if ad.Metadata["email"] != "fixture@example.invalid" || !strings.HasPrefix(ad.Label, "fixture@example.invalid") {
			t.Fatal("host credential card does not use the saved email")
		}
	}
}

func TestQoderProfileRejectsWrongUserAndStaleAuthorization(t *testing.T) {
	raw := []byte(`{"auth":{"accessToken":"dt-fixture","domain":"qoder.com"},"account":{"uid":"fixture-user"}}`)
	for _, tc := range []struct{ name, userID, access, region string }{
		{"different user", "other-user", "dt-fixture", "intl"},
		{"missing user", "", "dt-fixture", "intl"},
		{"rotated authorization", "fixture-user", "dt-old", "intl"},
		{"different region", "fixture-user", "dt-fixture", "cn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := &storedAuth{Auth: storedTokens{AccessToken: tc.access, Region: tc.region}, Account: storedAccount{UID: "fixture-user"}}
			updated, _, err := mergeQoderAccountProfile(raw, expected,
				&userInfoResponse{ID: tc.userID, Email: "wrong@example.invalid"})
			if err == nil || updated != nil {
				t.Fatal("unsafe profile update was accepted")
			}
		})
	}
}

func TestQoderEmptyProfileEmailDoesNotEraseKnownIdentity(t *testing.T) {
	raw := []byte(`{"auth":{"accessToken":"dt-fixture","domain":"qoder.com"},"account":{"uid":"fixture-user","nickname":"Fixture","email":"known@example.invalid"}}`)
	sa, err := parseStored(raw)
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := mergeQoderAccountProfile(raw, sa, &userInfoResponse{ID: "fixture-user"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := parseStored(updated)
	if err != nil || saved.Account.Email != "known@example.invalid" || saved.Account.Nickname != "Fixture" || !saved.Account.ProfileLoaded {
		t.Fatal("empty upstream fields erased the known identity")
	}
}

func TestQoderProfileCompletionUsesRegionalIdentityAndPersists(t *testing.T) {
	for _, tc := range []struct{ region, host, email string }{
		{"cn", "openapi.qoder.com.cn", ""},
		{"intl", "openapi.qoder.sh", "fixture@example.invalid"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			sa := buildStoredAuthFromDeviceToken(&deviceTokenResponse{Token: "dt-fixture", UserID: "fixture-user"}, nil, tc.region)
			physical, err := buildAuthFileJSON(sa, true, "keep note", nil)
			if err != nil {
				t.Fatal(err)
			}
			oldGet, oldSave := hostAuthGetPhysicalFn, hostAuthSaveJSONFn
			t.Cleanup(func() { hostAuthGetPhysicalFn, hostAuthSaveJSONFn = oldGet, oldSave })
			hostAuthGetPhysicalFn = func(index string) (*hostAuthPhysical, error) {
				if index != "fixture-index" {
					t.Fatal("looked up another account")
				}
				return &hostAuthPhysical{Name: authFileNameFor(sa), JSON: physical}, nil
			}
			hostAuthSaveJSONFn = func(name string, raw []byte) error {
				if name != authFileNameFor(sa) {
					t.Fatal("renamed account while completing identity")
				}
				physical = raw
				return nil
			}
			requests := 0
			stubLoginTransport(t, func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Host != tc.host || req.URL.Path != "/api/v1/userinfo" || req.Header.Get("Authorization") != "Bearer dt-fixture" {
					t.Fatal("userinfo did not use the authorized region and credential")
				}
				body, _ := json.Marshal(map[string]string{"id": "fixture-user", "name": "Fixture", "email": tc.email})
				return loginJSON(http.StatusOK, string(body)), nil
			})
			if err := completeAccountProfile("fixture-index", sa, false); err != nil {
				t.Fatal(err)
			}
			saved, err := parseStored(physical)
			if err != nil || saved.Account.Email != tc.email || saved.Account.Nickname != "Fixture" || !saved.Account.ProfileLoaded {
				t.Fatal("completed identity was not saved")
			}
			if err := completeAccountProfile("fixture-index", sa, false); err != nil || requests != 1 {
				t.Fatal("already loaded profile should not be fetched repeatedly")
			}
			if err := completeAccountProfile("fixture-index", sa, true); err != nil || requests != 2 {
				t.Fatal("manual refresh did not retry profile lookup")
			}
		})
	}
}

func TestQoderProfileFailureDoesNotSaveOrMarkLoaded(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"upstream rejected", `{}`, http.StatusUnauthorized},
		{"another account", `{"id":"other-user","email":"other@example.invalid"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sa := buildStoredAuthFromDeviceToken(&deviceTokenResponse{Token: "dt-fixture", UserID: "fixture-user"}, nil, regionIntl)
			raw, _ := buildAuthFileJSON(sa, false, "keep", nil)
			oldGet, oldSave := hostAuthGetPhysicalFn, hostAuthSaveJSONFn
			t.Cleanup(func() { hostAuthGetPhysicalFn, hostAuthSaveJSONFn = oldGet, oldSave })
			hostAuthGetPhysicalFn = func(string) (*hostAuthPhysical, error) {
				return &hostAuthPhysical{Name: authFileNameFor(sa), JSON: raw}, nil
			}
			hostAuthSaveJSONFn = func(string, []byte) error {
				t.Fatal("failed profile request must not write the credential")
				return nil
			}
			stubLoginTransport(t, func(*http.Request) (*http.Response, error) {
				return loginJSON(tc.status, tc.body), nil
			})
			if err := completeAccountProfile("fixture-index", sa, false); err == nil || sa.Account.ProfileLoaded || sa.Account.Email != "" {
				t.Fatal("failed lookup was reported as a completed profile")
			}
		})
	}
}
