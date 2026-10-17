// Adapted from luode0320/cpa-workbuddy-plugin/qoder-ai (806c61f).
// Copyright (c) 2026 Sliverkiss (based on workbuddy by lovingfish).
// Licensed under the MIT license in LICENSE.
// umid.go 实现国际版 Qoder 客户端活动与签到接口所需的按账号独立 UMID 设备指纹获取、派生与缓存。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	endpointUMIDRepPc = "http://us-pum.alibabachengdun.com/repPc.json?e=1000&pn=runtime-info-linux&os=8&sv=1.5.0&pv=NA&pt=1"
	umidUserAgent     = "Mozilla/5.0 (Windows; U; Windows NT 5.1; zh-CN; rv:1.9.2.3) Gecko/20100401 Firefox/3.6.3 SG/1.0.0"
	qoderClientVer    = "0.4.3"
	umidCacheTTL      = 12 * time.Hour

	xxhPrime1 uint64 = 11400714785074694791
	xxhPrime2 uint64 = 14029467366897019727
	xxhPrime3 uint64 = 1609587929392839161
	xxhPrime4 uint64 = 9650029242287828579
	xxhPrime5 uint64 = 2870177450012600261
)

// umidIdentity 承载单个账号请求国际版活动接口所需的完整设备标识头。
type umidIdentity struct {
	MachineID       string
	MachineHostname string
	MachineOS       string
	MachineToken    string
	MachineType     string
	MachineCode     string
}

type umidCacheEntry struct {
	identity  umidIdentity
	salt      int
	fetchedAt time.Time
}

var umidCache sync.Map // uid -> *umidCacheEntry

// Serialize issuance and rotation for the same account, so a concurrent cold
// read cannot overwrite a newly rotated identity with the initial seed.
var umidAccountLocks sync.Map // uid -> *sync.Mutex

func accountUMIDLock(uid string) *sync.Mutex {
	lock, _ := umidAccountLocks.LoadOrStore(uid, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// Tests replace only this network boundary; production uses the host transport.
var umidHTTPDo = hostHTTPDo

var umidAESKeys = [10][16]byte{
	{'I', 'z', 'y', 'l', 'U', 'K', '0', 'F', '>', 'm', 'L', '8', '*', '8', 'r', '*'},
	{'u', 'j', 'q', 'h', 'o', ':', 'Z', '>', '2', '}', 'C', ')', 'i', 'R', 'F', 'U'},
	{'<', 'p', 'R', 'X', 'G', ';', '$', 'M', 's', '<', '<', 'H', 'T', 'G', '#', 'M'},
	{'.', 'G', 'B', '$', 'p', ')', 'X', 'Y', '5', '5', '_', 'M', 'u', 'O', '~', ')'},
	{'t', 'i', 'q', '+', 'J', ',', '!', 'w', ')', 'X', '@', '&', '2', 'v', '9', 'T'},
	{'L', 'E', 'd', 'K', '.', 't', 'S', 'x', ']', 's', 'a', 'Z', 'b', '!', 'j', 'i'},
	{'p', 'o', '?', '+', ',', '<', 'B', 'S', '$', '<', '=', '4', 'J', 'u', 'G', '='},
	{'t', 'Q', 'E', 'k', '}', 'L', '*', '=', '^', 'f', 'O', 'V', '9', 'Y', 'S', '3'},
	{'8', '+', 'M', '1', 'm', 'e', '~', 'u', 'L', '$', '8', 'c', 'i', 'Y', '(', '$'},
	{':', 'H', '(', 'I', '+', '$', '+', ',', '4', '@', 'A', 'q', 'Z', 'g', '0', '+'},
}

var umidConstBlock1A = [20]byte{0x97, 0x6f, 0x65, 0x30, 0x46, 0x3a, 0xbf, 0x1c, 0xae, 0x10, 0x52, 0x87, 0xd3, 0x85, 0x24, 0xce, 0x60, 0xcf, 0x9f, 0xea}
var umidConstBlock1C = [20]byte{0x37, 0xb6, 0xa3, 0x6e, 0x52, 0xcb, 0x51, 0x37, 0xa9, 0x65, 0x2a, 0xd9, 0x56, 0xd0, 0x56, 0xaf, 0xe8, 0x6b, 0x47, 0xdd}
var umidConstBlock1D = [16]byte{0x55, 0x53, 0x47, 0xdd, 0xeb, 0x68, 0x6b, 0xa6, 0x52, 0xca, 0x51, 0x37, 0x61, 0x6d, 0x29, 0xda}
var umidConst1BWords = [4]uint32{0x12997b2f, 0x894cbd97, 0xc4a65ecb, 0xe2532f65}

// resolveAccountUMID 获取或生成指定账号的独立 UMID 设备标识。
// [参数] sa: 目标账号的认证信息。
// [返回] 账号绑定的设备标识结构指针与错误。
// 最近修改时间：2026-10-11 15:05:00 新增按账号独立生成并缓存 UMID 设备标识
func resolveAccountUMID(sa *storedAuth) (*umidIdentity, error) {
	// 1. 提取账号唯一键并检查有效缓存
	uid := accountUMIDKey(sa)
	lock := accountUMIDLock(uid)
	lock.Lock()
	defer lock.Unlock()
	if v, ok := umidCache.Load(uid); ok {
		if entry, ok2 := v.(*umidCacheEntry); ok2 && time.Since(entry.fetchedAt) < umidCacheTTL && entry.identity.MachineToken != "" {
			cp := entry.identity
			return &cp, nil
		}
	}
	// 2. 缓存未命中或已过期时向 UMID 服务申请新令牌
	return refreshAccountUMID(uid, 0)
}

// rotateAccountUMID follows the reference's incrementing account seed. The
// caller only requests rotation after SAME_PERSON_ALREADY_CLAIMED. A failed
// issuance leaves the last complete identity intact and must not be retried
// as a claim with that old identity.
func rotateAccountUMID(sa *storedAuth) (*umidIdentity, error) {
	uid := accountUMIDKey(sa)
	lock := accountUMIDLock(uid)
	lock.Lock()
	defer lock.Unlock()
	nextSalt := 1
	if v, ok := umidCache.Load(uid); ok {
		if entry, ok := v.(*umidCacheEntry); ok {
			nextSalt = entry.salt + 1
		}
	}
	return refreshAccountUMID(uid, nextSalt)
}

// accountUMIDKey 提取用于隔离账号设备指纹的稳定键值。
// [参数] sa: 目标账号的认证信息。
// [返回] 非空账号键字符串。
// 最近修改时间：2026-10-11 15:05:00 新增账号设备指纹键提取
func accountUMIDKey(sa *storedAuth) string {
	// 1. 优先使用账号 UID，缺失时回退到 AccessToken 摘要
	if sa != nil {
		if uid := strings.TrimSpace(sa.Account.UID); uid != "" {
			return uid
		}
		if tok := strings.TrimSpace(sa.Auth.AccessToken); tok != "" {
			sum := sha256.Sum256([]byte(tok))
			return hex.EncodeToString(sum[:8])
		}
	}
	return "default-qoder-account"
}

// refreshAccountUMID 使用参考实现的账号种子向 UMID 服务申请令牌并写入缓存。
// 调用方持有该账号的 accountUMIDLock。
// [参数] uid: 账号唯一标识；salt: 设备身份轮次。
// [返回] 新生成的设备标识结构指针与错误。
// 最近修改时间：2026-10-11 15:05:00 新增 UMID 远端申请与本地派生闭环
func refreshAccountUMID(uid string, salt int) (*umidIdentity, error) {
	// 1. 构造加密上报报文并请求 UMID 接口
	seedKey := fmt.Sprintf("%s#%d", uid, salt)
	pkt, err := buildUMIDRequestBody(seedKey, time.Now())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointUMIDRepPc, strings.NewReader(pkt.BodyB64))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", umidUserAgent)
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	resp, err := umidHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("umid http %d", resp.StatusCode)
	}
	// 2. 解析并解密响应中的 dt 字段提取 eeid 令牌
	var payload struct {
		DT string `json:"dt"`
		EC int    `json:"ec"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return nil, fmt.Errorf("umid json: %w", err)
	}
	if payload.EC != 200 || payload.DT == "" {
		return nil, fmt.Errorf("umid ec=%d", payload.EC)
	}
	token, err := decryptUMIDResponseDT(payload.DT)
	if err != nil {
		return nil, err
	}
	// 3. 派生配套的 machineType 与 machineCode 并更新缓存
	codes := deriveUMIDCodes(umidDeriveInput{MachineToken: token, VMCode: 91, Pct: 0})
	id := umidIdentity{
		MachineID:       uuid.NewSHA1(uuid.NameSpaceDNS, []byte(seedKey)).String(),
		MachineHostname: pkt.HostName,
		MachineOS:       "x86_64_linux",
		MachineToken:    token,
		MachineType:     codes.MachineType,
		MachineCode:     codes.MachineCode,
	}
	umidCache.Store(uid, &umidCacheEntry{
		identity:  id,
		salt:      salt,
		fetchedAt: time.Now(),
	})
	return &id, nil
}
