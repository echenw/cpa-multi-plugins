package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// completeAccountProfile runs after credential persistence, on the panel's
// account-details request. A successful empty email is still a loaded profile
// (some CN accounts have no email); a manual refresh can fetch it again.
func completeAccountProfile(authIndex string, sa *storedAuth, force bool) error {
	if sa.Account.ProfileLoaded && !force {
		return nil
	}
	if strings.TrimSpace(sa.Account.UID) == "" {
		return fmt.Errorf("账号缺少用户 ID，无法校验资料归属")
	}
	ui, err := fetchUserInfo(sa.Auth.AccessToken, authRegion(sa))
	if err != nil {
		return fmt.Errorf("获取账号资料失败: %w", err)
	}
	// Re-read after the network call: merge identity into the current document,
	// preserving tokens, disabled state and operator metadata.
	mu := checkinLockFor(authIndex)
	mu.Lock()
	defer mu.Unlock()
	phys, err := hostAuthGetPhysicalFn(authIndex)
	if err != nil {
		return fmt.Errorf("读取账号资料存储失败: %w", err)
	}
	raw, current, err := mergeQoderAccountProfile(phys.JSON, sa, ui)
	if err != nil {
		return err
	}
	name := phys.Name
	if name == "" {
		name = authFileNameFor(current)
	}
	if err := hostAuthSaveJSONFn(name, raw); err != nil {
		return fmt.Errorf("保存账号资料失败: %w", err)
	}
	*sa = *current
	return nil
}

func mergeQoderAccountProfile(raw []byte, expected *storedAuth, ui *userInfoResponse) ([]byte, *storedAuth, error) {
	if ui == nil || strings.TrimSpace(ui.ID) == "" || strings.TrimSpace(ui.ID) != expected.Account.UID {
		return nil, nil, fmt.Errorf("账号资料的用户 ID 与授权账号不一致")
	}
	doc, err := normalizeQoderAuthDoc(raw)
	if err != nil {
		return nil, nil, err
	}
	current, err := parseStored(raw)
	if err != nil {
		return nil, nil, err
	}
	if current.Account.UID != expected.Account.UID || authRegion(current) != authRegion(expected) ||
		current.Auth.AccessToken != expected.Auth.AccessToken {
		return nil, nil, fmt.Errorf("授权状态已更新，请刷新后重试账号资料")
	}
	account := doc
	_, nested := doc["auth"]
	if nested {
		account = nil
		if err := json.Unmarshal(doc["account"], &account); err != nil || account == nil {
			return nil, nil, fmt.Errorf("无效的账号资料存储")
		}
	}
	if name := strings.TrimSpace(ui.Name); name != "" {
		current.Account.Nickname = name
		account["nickname"], _ = json.Marshal(name)
	}
	// An absent upstream email does not erase one already recorded.
	if email := userInfoEmail(ui); email != "" {
		current.Account.Email = email
	}
	if current.Account.Email != "" {
		email, _ := json.Marshal(current.Account.Email)
		account["email"] = email
		doc["email"] = email // host credential list also consumes this field
	}
	current.Account.ProfileLoaded = true
	account["profile_loaded"] = json.RawMessage("true")
	if nested {
		doc["account"], err = json.Marshal(account)
		if err != nil {
			return nil, nil, err
		}
	}
	merged, err := json.Marshal(doc)
	return merged, current, err
}
