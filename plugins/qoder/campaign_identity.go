package main

import "net/http"

const campaignIdentityFailureMessage = "活动身份获取失败，暂时无法查询或领取积分，请稍后重试。"

// Activity identity failures do not change the account's saved OAuth grant.
// Keep the cause available to callers without exposing it in the UI message.
type campaignIdentityError struct{ cause error }

func (e *campaignIdentityError) Error() string { return campaignIdentityFailureMessage }
func (e *campaignIdentityError) Unwrap() error { return e.cause }

// internationalCampaignHeaders uses the reference plugin's account-scoped
// UMID on both campaign reads and claims. Login itself never acquires UMID.
// A failed issuance is an explicit error, rather than an empty campaign list
// produced by silently switching to simulated or missing device headers.
func internationalCampaignHeaders(req *http.Request, sa *storedAuth) error {
	id, err := resolveAccountUMID(sa)
	if err != nil {
		return &campaignIdentityError{cause: err}
	}
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cosy-ClientType", billingClientType)
	req.Header.Set("Cosy-Version", qoderClientVer)
	req.Header.Set("User-Agent", "Qoder")
	req.Header.Set("Cosy-MachineOS", id.MachineOS)
	req.Header.Set("Cosy-MachineHostname", id.MachineHostname)
	req.Header.Set("Cosy-MachineId", id.MachineID)
	req.Header.Set("Cosy-MachineToken", id.MachineToken)
	req.Header.Set("Cosy-MachineCode", id.MachineCode)
	req.Header.Set("Cosy-MachineType", id.MachineType)
	return nil
}
