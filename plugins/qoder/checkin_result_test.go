package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func manualCheckinFixture(t *testing.T, sa *storedAuth) (pluginapi.ManagementRequest, map[string][]byte) {
	t.Helper()
	raw, err := json.Marshal(sa)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"fixture-account": raw}
	t.Cleanup(stubPersistSeams(t, files))
	t.Cleanup(func() { checkinLocks.Delete("fixture-account") })
	return pluginapi.ManagementRequest{Body: []byte(`{"auth_index":"fixture-account"}`)}, files
}

func TestManualCheckinReportsClaimFailuresAsFailures(t *testing.T) {
	for _, scenario := range []string{"transport", "http-error", "invalid-json", "umid-rotation"} {
		t.Run(scenario, func(t *testing.T) {
			sa := fixtureIntlAuth(t)
			sa.Auth.RefreshToken = "drt-manual-fixture"
			req, files := manualCheckinFixture(t, sa)
			original := string(files["fixture-account"])
			issued, claims := 0, 0
			stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
				issued++
				if scenario == "umid-rotation" && issued > 1 {
					return nil, errors.New("fictional collector unavailable")
				}
				return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
			})
			list, _ := json.Marshal(campaignStatusResponse{Campaigns: []campaign{*fixtureClaimableCampaign()}})
			stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/sash/api/v1/me/campaigns":
					return loginJSON(http.StatusOK, string(list)), nil
				case "/api/v1/me", "/api/v2/quota/usage":
					return loginJSON(http.StatusOK, `{}`), nil
				case "/sash/api/v1/me/campaigns/fixture-daily/claim":
					claims++
					switch scenario {
					case "transport":
						return nil, context.DeadlineExceeded
					case "http-error":
						return loginJSON(http.StatusServiceUnavailable, `{}`), nil
					case "invalid-json":
						return loginJSON(http.StatusOK, `not-json`), nil
					default:
						return loginJSON(http.StatusOK, `{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`), nil
					}
				default:
					t.Fatalf("unexpected external call: %s", r.URL.Path)
					return nil, nil
				}
			})
			out := handleManualCheckin(req)
			results := out["results"].([]map[string]any)
			summary := out["summary"].(map[string]any)
			if len(results) != 1 || results[0]["error"] == nil || results[0]["skipped"] == true || results[0]["success"] == true || summary["fail"] != 1 || summary["already"] != 0 || summary["skipped"] != 0 || claims != 1 {
				t.Fatalf("claim failure was lost or counted as signed in: result=%v summary=%v claims=%d", results, summary, claims)
			}
			if scenario == "umid-rotation" && !strings.Contains(results[0]["error"].(string), campaignIdentityFailureMessage) {
				t.Fatal("manual endpoint lost the readable identity error")
			}
			if string(files["fixture-account"]) != original || len(capturedSaves) != 0 {
				t.Fatal("campaign failure changed the saved OAuth grant")
			}
		})
	}
}

func TestIntlAggregatePreservesIdentityErrorType(t *testing.T) {
	sa := fixtureIntlAuth(t)
	issued := 0
	stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
		issued++
		if issued > 1 {
			return nil, errors.New("fictional collector unavailable")
		}
		return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
	})
	list, _ := json.Marshal(campaignStatusResponse{Campaigns: []campaign{*fixtureClaimableCampaign()}})
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/sash/api/v1/me/campaigns" {
			return loginJSON(http.StatusOK, string(list)), nil
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/fixture-daily/claim") {
			return loginJSON(http.StatusOK, `{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`), nil
		}
		t.Fatal("unexpected request during aggregate failure")
		return nil, nil
	})
	result, err := performCampaignCheckin(sa)
	var identityErr *campaignIdentityError
	if result != nil || !errors.As(err, &identityErr) || isAuthRejectedError(err) {
		t.Fatal("aggregation replaced the typed identity failure with an eligibility result")
	}
}

func TestManualCheckinPreservesRewardsAndPartialResults(t *testing.T) {
	for _, scenario := range []string{"credits-and-coupons", "coupons-only", "coupon-pending", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			sa := fixtureIntlAuth(t)
			req, _ := manualCheckinFixture(t, sa)
			stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
				return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
			})
			rows := []campaign{{CampaignID: "coupon-one", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "REDEMPTION_CODE"}}}
			wantCodes := []string{"FICTIONAL-ONE"}
			if scenario == "credits-and-coupons" || scenario == "partial" {
				rows = append(rows, *fixtureClaimableCampaign())
			}
			if scenario == "credits-and-coupons" || scenario == "coupons-only" {
				rows = append(rows, campaign{CampaignID: "coupon-two", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "REDEMPTION_CODE"}})
				wantCodes = append(wantCodes, "FICTIONAL-TWO")
			}
			list, _ := json.Marshal(campaignStatusResponse{Campaigns: rows})
			stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/sash/api/v1/me/campaigns":
					return loginJSON(http.StatusOK, string(list)), nil
				case "/api/v1/me", "/api/v2/quota/usage":
					return loginJSON(http.StatusOK, `{}`), nil
				case "/sash/api/v1/me/campaigns/fixture-daily/claim":
					return loginJSON(http.StatusOK, `{"status":"CLAIMED","benefit":{"kind":"CREDITS","amount":100}}`), nil
				case "/sash/api/v1/me/campaigns/coupon-one/claim":
					if scenario == "partial" {
						return nil, context.DeadlineExceeded
					}
					if scenario == "coupon-pending" {
						return loginJSON(http.StatusOK, `{"status":"CLAIMED","benefit":{"kind":"REDEMPTION_CODE"}}`), nil
					}
					return loginJSON(http.StatusOK, `{"status":"CLAIMED","redemptionCode":"FICTIONAL-ONE"}`), nil
				case "/sash/api/v1/me/campaigns/coupon-two/claim":
					return loginJSON(http.StatusOK, `{"status":"CLAIMED","redemptionCode":"FICTIONAL-TWO"}`), nil
				default:
					t.Fatalf("unexpected external call: %s", r.URL.Path)
					return nil, nil
				}
			})
			out := handleManualCheckin(req)
			result := out["results"].([]map[string]any)[0]
			summary := out["summary"].(map[string]any)
			if result["success"] != true || result["skipped"] == true || result["error"] != nil {
				t.Fatalf("a confirmed reward was discarded: %v", result)
			}
			if scenario == "partial" {
				claimErrors, _ := result["claim_errors"].([]string)
				if result["reward_credits"] != int64(100) || result["partial"] != true || len(claimErrors) != 1 || summary["partial"] != 1 || summary["success"] != 0 || summary["already"] != 0 {
					t.Fatalf("partial grants must retain credits and errors separately: result=%v summary=%v", result, summary)
				}
			} else if scenario == "coupon-pending" {
				if !strings.Contains(result["message"].(string), "兑换码发放中") || result["reward_credits"] != int64(0) || summary["success"] != 1 {
					t.Fatal("a confirmed pending coupon must stay a successful non-credit grant")
				}
			} else if codes, _ := result["redemption_codes"].([]string); !reflect.DeepEqual(codes, wantCodes) || result["redemption_code"] != wantCodes[0] {
				t.Fatalf("the manual endpoint dropped redemption codes: %v", result)
			}
		})
	}
}

func TestManualCheckinSeparatesAlreadyClaimedAndSkipped(t *testing.T) {
	sa := fixtureIntlAuth(t)
	other := fixtureIntlAuth(t)
	a, _ := json.Marshal(sa)
	b, _ := json.Marshal(other)
	files := map[string][]byte{"already-fixture": a, "empty-fixture": b}
	t.Cleanup(stubPersistSeams(t, files))
	stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
		return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
	})
	t.Cleanup(func() {
		checkinLocks.Delete("already-fixture")
		checkinLocks.Delete("empty-fixture")
	})
	// Distinguish these fictional accounts at the wire boundary.
	other.Auth.AccessToken = "dt-empty-fixture"
	files["empty-fixture"], _ = json.Marshal(other)
	stubLoginTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/sash/api/v1/me/campaigns" {
			if r.Header.Get("Authorization") == "Bearer dt-empty-fixture" {
				return loginJSON(http.StatusOK, `{"campaigns":[]}`), nil
			}
			return loginJSON(http.StatusOK, campaignList("CLAIMED")), nil
		}
		if r.URL.Path == "/api/v1/me" || r.URL.Path == "/api/v2/quota/usage" {
			return loginJSON(http.StatusOK, `{}`), nil
		}
		t.Fatal("already-claimed and empty accounts must not issue a claim")
		return nil, nil
	})
	out := handleManualCheckin(pluginapi.ManagementRequest{Body: []byte(`{}`)})
	summary := out["summary"].(map[string]any)
	if summary["total"] != 2 || summary["already"] != 1 || summary["skipped"] != 1 || summary["success"] != 0 || summary["partial"] != 0 || summary["fail"] != 0 {
		t.Fatalf("normal skips must not count as already claimed: %v", summary)
	}
}
