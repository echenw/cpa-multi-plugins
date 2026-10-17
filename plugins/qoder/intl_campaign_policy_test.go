package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func fixtureClaimableCampaign() *campaign {
	return &campaign{
		CampaignID: "fixture-daily", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE",
		Benefit: &campaignBen{Kind: "CREDITS", Amount: 100},
	}
}

func TestIntlClaimSamePersonRetriesAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name          string
		blockedClaims int
		wantClaims    int
		wantSuccess   bool
	}{
		{"second-attempt-granted", 1, 2, true},
		{"third-attempt-granted", 2, 3, true},
		{"still-blocked", 3, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sa := fixtureIntlAuth(t)
			sa.Auth.RefreshToken = "drt-reference-fixture"
			originalAuth := sa.Auth
			issued, claims := 0, 0
			stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
				issued++
				return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
			})
			stubLoginTransport(t, func(req *http.Request) (*http.Response, error) {
				claims++
				wantID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("%s#%d", sa.Account.UID, claims-1))).String()
				if req.Method != http.MethodPost || req.URL.Path != "/sash/api/v1/me/campaigns/fixture-daily/claim" || req.Header.Get("Cosy-MachineId") != wantID {
					t.Fatal("claim retry did not use the next account-scoped UMID identity")
				}
				if claims <= tc.blockedClaims {
					return loginJSON(http.StatusOK, `{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`), nil
				}
				return loginJSON(http.StatusOK, `{"status":"CLAIMED","benefit":{"kind":"CREDITS","amount":100}}`), nil
			})
			res, err := claimCampaignByID(sa, fixtureClaimableCampaign())
			if err != nil || res["success"] != tc.wantSuccess || claims != tc.wantClaims || issued != tc.wantClaims {
				t.Fatalf("claim result=%v err=%v claims=%d identities=%d", res, err, claims, issued)
			}
			if !tc.wantSuccess && (res["result"] != "BLOCKED" || res["failure_code"] != "SAME_PERSON_ALREADY_CLAIMED") {
				t.Fatal("exhausted retries must preserve the upstream rejection")
			}
			if !reflect.DeepEqual(sa.Auth, originalAuth) {
				t.Fatal("activity identity rotation changed the saved OAuth grant")
			}
			cached, err := resolveAccountUMID(sa)
			wantID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("%s#%d", sa.Account.UID, claims-1))).String()
			if err != nil || cached.MachineID != wantID || issued != tc.wantClaims {
				t.Fatal("subsequent requests did not reuse the successfully rotated identity")
			}
		})
	}
}

func TestIntlClaimDoesNotRetryOtherFailuresOrReplay(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		transport  error
		wantResult string
	}{
		{"timeout", 0, "", context.DeadlineExceeded, ""},
		{"unauthorized", http.StatusUnauthorized, `{}`, nil, ""},
		{"rate-limit", http.StatusTooManyRequests, `{}`, nil, ""},
		{"upstream-error", http.StatusServiceUnavailable, `{}`, nil, ""},
		{"redirect", http.StatusFound, `{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`, nil, ""},
		{"already", http.StatusConflict, `{"errorCode":"ALREADY_CLAIMED"}`, nil, "ALREADY_CLAIMED"},
		{"replayed", http.StatusOK, `{"status":"CLAIMED","replayed":true}`, nil, "ALREADY_CLAIMED"},
		{"other-block", http.StatusOK, `{"status":"BLOCKED","failureCode":"RISK_BLOCKED"}`, nil, "BLOCKED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sa := fixtureIntlAuth(t)
			issued, claims := 0, 0
			stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
				issued++
				return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
			})
			stubLoginTransport(t, func(*http.Request) (*http.Response, error) {
				claims++
				if tc.transport != nil {
					return nil, tc.transport
				}
				return loginJSON(tc.status, tc.body), nil
			})
			res, err := claimCampaignByID(sa, fixtureClaimableCampaign())
			if err != nil || res["success"] == true || claims != 1 || issued != 1 {
				t.Fatalf("non-retryable result=%v err=%v claims=%d identities=%d", res, err, claims, issued)
			}
			if tc.wantResult != "" && res["result"] != tc.wantResult {
				t.Fatalf("result=%v, want %s", res["result"], tc.wantResult)
			}
		})
	}
}

func TestIntlClaimStopsWhenUMIDRotationFails(t *testing.T) {
	sa := fixtureIntlAuth(t)
	sa.Auth.RefreshToken = "drt-reference-fixture"
	originalAuth := sa.Auth
	issued, claims := 0, 0
	issuanceErr := errors.New("fixture UMID http 403")
	stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
		issued++
		if issued > 1 {
			return nil, issuanceErr
		}
		return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
	})
	stubLoginTransport(t, func(*http.Request) (*http.Response, error) {
		claims++
		return loginJSON(http.StatusOK, `{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`), nil
	})
	_, err := claimCampaignByID(sa, fixtureClaimableCampaign())
	var identityErr *campaignIdentityError
	if !errors.As(err, &identityErr) || !errors.Is(err, issuanceErr) || err.Error() != campaignIdentityFailureMessage || issued != 2 || claims != 1 {
		t.Fatalf("rotation failure was hidden or retried: err=%v claims=%d identities=%d", err, claims, issued)
	}
	if isAuthRejectedError(err) || !reflect.DeepEqual(sa.Auth, originalAuth) {
		t.Fatal("UMID collector failure was mistaken for an invalid OAuth grant")
	}
	cached, err := resolveAccountUMID(sa)
	if err != nil || cached.MachineID != uuid.NewSHA1(uuid.NameSpaceDNS, []byte(sa.Account.UID+"#0")).String() || issued != 2 {
		t.Fatal("failed rotation replaced the last complete cached identity")
	}
}

func TestIntlClaimsOnlyCurrentExplicitActivities(t *testing.T) {
	sa := fixtureIntlAuth(t)
	issued := 0
	stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
		issued++
		return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
	})
	now := time.Now().Unix()
	rows := []campaign{
		{CampaignID: "coupon", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "REDEMPTION_CODE", Amount: 1}},
		{CampaignID: "daily", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "CREDITS", Amount: 100}},
		{CampaignID: "details", ActionType: "VIEW_DETAILS", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "CREDITS", Amount: 1000}},
		{CampaignID: "expired", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", EndAt: now - 60},
		{CampaignID: "future", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", StartAt: now + 3600},
		{CampaignID: "missing-action", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "CREDITS", Amount: 300}},
		{CampaignID: "already", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMED"},
	}
	list, _ := json.Marshal(campaignStatusResponse{ShowCampaign: true, Campaigns: rows})
	var claimed []string
	stubLoginTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == "/sash/api/v1/me/campaigns" {
			return loginJSON(http.StatusOK, string(list)), nil
		}
		if req.Method != http.MethodPost {
			t.Fatal("check-in tried a details/reward/session lookup")
		}
		switch req.URL.Path {
		case "/sash/api/v1/me/campaigns/daily/claim":
			claimed = append(claimed, "daily")
			return loginJSON(http.StatusOK, `{"status":"CLAIMED","benefit":{"kind":"CREDITS","amount":100}}`), nil
		case "/sash/api/v1/me/campaigns/coupon/claim":
			claimed = append(claimed, "coupon")
			return loginJSON(http.StatusOK, `{"status":"CLAIMED","benefit":{"kind":"REDEMPTION_CODE"},"redemptionCode":"fixture-coupon"}`), nil
		default:
			t.Fatal("check-in attempted an activity not explicitly claimable in the current list")
			return nil, nil
		}
	})
	res, err := performCampaignCheckin(sa)
	if err != nil || res["success"] != true || res["rewardCredits"] != float64(100) || !reflect.DeepEqual(claimed, []string{"daily", "coupon"}) || issued != 1 {
		t.Fatalf("multi-claim result=%v err=%v claimed=%v identities=%d", res, err, claimed, issued)
	}
	if codes, _ := res["redemption_codes"].([]string); !reflect.DeepEqual(codes, []string{"fixture-coupon"}) {
		t.Fatal("explicitly claimable coupon was lost when filtering activities")
	}
}

func TestIntlNoClaimableActivityNeverProbesHiddenRoundsOrDetails(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []campaign
	}{
		{"empty", nil},
		{"details", []campaign{{CampaignID: "details", ActionType: "VIEW_DETAILS", ClaimStatus: "CLAIMABLE"}}},
		{"missing-action", []campaign{{CampaignID: "missing-action", ClaimStatus: "CLAIMABLE", Benefit: &campaignBen{Kind: "CREDITS", Amount: 100}}}},
		{"expired-claimed", []campaign{{CampaignID: "old", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMED", EndAt: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sa := fixtureIntlAuth(t)
			previousMemo := roundMemo
			entry := campaignRoundEntry{CampaignID: "remembered-other-account", SeenAt: time.Now()}
			roundMemo = &campaignRoundMemo{
				perAccount: map[string]campaignRoundEntry{regionIntl + ":" + sa.Account.UID: entry},
				perRegion:  map[string]campaignRoundEntry{regionIntl: entry},
			}
			t.Cleanup(func() { roundMemo = previousMemo })
			stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
				return &hostHTTPResponse{StatusCode: http.StatusOK, Body: []byte(testUMIDIssuedPayload)}, nil
			})
			list, _ := json.Marshal(campaignStatusResponse{ShowCampaign: true, Campaigns: tc.rows})
			requests := 0
			stubLoginTransport(t, func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Method != http.MethodGet || req.URL.Path != "/sash/api/v1/me/campaigns" {
					t.Fatal("non-claimable list reached the hidden-round/details fallback")
				}
				return loginJSON(http.StatusOK, string(list)), nil
			})
			res, err := performCampaignCheckin(sa)
			if err != nil || res["result"] != "NOTHING_CLAIMABLE" || requests != 1 {
				t.Fatalf("empty claim result=%v err=%v requests=%d", res, err, requests)
			}
			ci := campaignCheckinSummary(&campaignStatusResponse{Campaigns: tc.rows}, regionIntl)
			if ci.TodayCheckedIn || ci.DailyCredit != 0 {
				t.Fatal("non-claimable/expired row leaked into the current check-in summary")
			}
		})
	}
}

func TestCNClaimSamePersonDoesNotRotateUMID(t *testing.T) {
	original := machineIdentityOverride
	machineIdentityOverride = &machineIdentity{Source: "derived"}
	t.Cleanup(func() { machineIdentityOverride = original })
	stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
		t.Fatal("CN activity must not acquire or rotate international UMID")
		return nil, nil
	})
	claims := 0
	newBillingServer(t, regionCN, map[string]func(*http.Request) (int, string){
		"/sash/api/v1/me/campaigns/fixture-daily/claim": func(*http.Request) (int, string) {
			claims++
			return http.StatusOK, `{"status":"BLOCKED","failureCode":"SAME_PERSON_ALREADY_CLAIMED"}`
		},
	})
	res, err := claimCampaignByID(cnAuth(), fixtureClaimableCampaign())
	if err != nil || res["result"] != "BLOCKED" || claims != 1 {
		t.Fatalf("CN claim behavior changed: result=%v err=%v claims=%d", res, err, claims)
	}
}

func TestIntlInvalidActivityCannotReachClaimTransport(t *testing.T) {
	sa := fixtureIntlAuth(t)
	stubUMID(t, func(*http.Request) (*hostHTTPResponse, error) {
		t.Fatal("an invalid activity must not acquire UMID")
		return nil, nil
	})
	stubLoginTransport(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("an invalid activity must not make a claim request")
		return nil, nil
	})
	for _, row := range []*campaign{
		nil,
		{CampaignID: "details", ActionType: "VIEW_DETAILS", ClaimStatus: "CLAIMABLE"},
		{CampaignID: "old", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMABLE", EndAt: 1},
		{CampaignID: "done", ActionType: "CLAIM_BENEFIT", ClaimStatus: "CLAIMED"},
	} {
		res, err := claimCampaignByID(sa, row)
		if err != nil || res["result"] != "NOTHING_CLAIMABLE" {
			t.Fatal("invalid activity did not end as a no-op")
		}
	}
}

func TestIntlIdentityErrorPreservesReadableMessage(t *testing.T) {
	err := &campaignIdentityError{cause: errors.New("fixture upstream unauthorized http 403")}
	if err.Error() != "活动身份获取失败，暂时无法查询或领取积分，请稍后重试。" || strings.Contains(err.Error(), "403") || isAuthRejectedError(err) {
		t.Fatal("identity error leaked an upstream detail or was treated as an OAuth rejection")
	}
}
