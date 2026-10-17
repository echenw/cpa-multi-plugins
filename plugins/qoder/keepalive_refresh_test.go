package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Exercise the saved keepalive record and the host refresh response with the
// same fictional device response. Only host storage and HTTP are replaced.
func TestDeviceKeepaliveAndHostRefreshAgreeOnExpiry(t *testing.T) {
	resetUsageNoteTestState(t)
	t.Cleanup(func() { resetUsageNoteTestState(t) })
	for _, region := range []string{regionCN, regionIntl} {
		for _, field := range []string{"token", "device_token"} {
			t.Run(region+"/"+field, func(t *testing.T) {
				original := []byte(fmt.Sprintf(`{"type":"qoder","disabled":true,"priority":7,"auth":{"accessToken":"dt-old-fixture","refreshToken":"drt-old-fixture","personalToken":"pt-preserved-fixture","expiresAt":1790000000,"region":%q},"account":{"uid":"refresh-fixture"}}`, region))
				files := map[string][]byte{"refresh-fixture": original}
				t.Cleanup(stubPersistSeams(t, files))
				calls := 0
				stubLoginTransport(t, func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != http.MethodPost || req.URL.String() != upstreamBaseForRegion(region)+"/api/v1/deviceToken/refresh" {
						t.Fatal("device credentials were refreshed through the wrong family or region")
					}
					return loginJSON(http.StatusOK, fmt.Sprintf(`{%q:"dt-new-fixture","refresh_token":"drt-new-fixture","expires_at":"2030-01-02T03:04:05Z"}`, field)), nil
				})
				status, err := refreshOneAuth("refresh-fixture", "refresh-fixture")
				if err != nil || status != "refreshed" || len(capturedSaves) != 1 {
					t.Fatalf("keepalive did not save one successful refresh: status=%s err=%v saves=%d", status, err, len(capturedSaves))
				}
				saved, err := parseStored(files["refresh-fixture"])
				if err != nil {
					t.Fatal(err)
				}
				expiry := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC).Unix()
				if saved.Auth.ExpiresAt != expiry || saved.Auth.AccessToken != "dt-new-fixture" || saved.Auth.RefreshToken != "drt-new-fixture" || saved.Auth.PersonalToken != "pt-preserved-fixture" {
					t.Fatal("keepalive lost the device expiry, rotating pair, or independent PAT")
				}
				var doc map[string]any
				if err := json.Unmarshal(files["refresh-fixture"], &doc); err != nil || doc["disabled"] != true || doc["priority"] != float64(7) {
					t.Fatal("keepalive changed operator metadata")
				}
				raw, _ := json.Marshal(pluginapi.AuthRefreshRequest{StorageJSON: original})
				out, err := handleRefreshAuth(raw)
				if err != nil {
					t.Fatal(err)
				}
				var env envelope
				var response pluginapi.AuthRefreshResponse
				if err := json.Unmarshal(out, &env); err != nil || !env.OK {
					t.Fatal("host refresh returned an invalid envelope")
				}
				if err := json.Unmarshal(env.Result, &response); err != nil {
					t.Fatal(err)
				}
				hostSaved, err := parseStored(response.Auth.StorageJSON)
				if err != nil || hostSaved.Auth != saved.Auth || calls != 2 || len(capturedSaves) != 1 {
					t.Fatal("host refresh and keepalive disagree, or host refresh double-saved")
				}
			})
		}
	}
}

func TestDeviceKeepaliveDoesNotSaveIncompleteRefresh(t *testing.T) {
	original := []byte(`{"auth":{"accessToken":"dt-old-fixture","refreshToken":"drt-old-fixture","region":"cn"},"account":{"uid":"incomplete-fixture"}}`)
	files := map[string][]byte{"incomplete-fixture": original}
	t.Cleanup(stubPersistSeams(t, files))
	stubLoginTransport(t, func(*http.Request) (*http.Response, error) {
		return loginJSON(http.StatusOK, `{"token":"dt-incomplete-fixture","expires_at":"2030-01-02T03:04:05Z"}`), nil
	})
	status, err := refreshOneAuth("incomplete-fixture", "incomplete-fixture")
	if err == nil || status != "failed" || len(capturedSaves) != 0 || string(files["incomplete-fixture"]) != string(original) {
		t.Fatal("an incomplete device refresh must not replace the saved grant")
	}
}
