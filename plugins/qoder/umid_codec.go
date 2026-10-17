// Adapted from luode0320/cpa-workbuddy-plugin/qoder-ai (806c61f).
// Copyright (c) 2026 Sliverkiss (based on workbuddy by lovingfish).
// Licensed under the MIT license in LICENSE.
// umid_codec.go 实现 UMID 协议报文的组包加密、响应解密与 xxh64 设备码派生算法。
package main

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// umidRequestPacket 封装发往 repPc.json 的加密请求体与派生主机名。
type umidRequestPacket struct {
	BodyB64  string
	HostName string
}

// umidDeriveInput 定义从 machineToken 派生设备类型与设备码的输入参数。
type umidDeriveInput struct {
	MachineToken string
	VMCode       int
	Pct          int
}

// umidDerivedCodes 定义派生出的 machineType 与 machineCode。
type umidDerivedCodes struct {
	MachineType string
	MachineCode string
}

// umidSectionSpec 定义单个 cfcd 段的封装输入。
type umidSectionSpec struct {
	Tag    byte
	Blocks []byte
	Mask   []byte
}

// buildUMIDSection1B 基于种子哈希构建 Section 1 (0x1b) 的变换后分块与主机名。
// [参数] sum: 账号种子 SHA-256 摘要；now: 当前时间。
// [返回] 16 字节对齐变换后的分块切片与派生主机名。
// 最近修改时间：2026-10-11 15:05:00 拆分 Section 0x1b 构造辅助函数
func buildUMIDSection1B(sum [32]byte, now time.Time) ([]byte, string) {
	// 1. 基于摘要派生虚拟网卡 MAC、主机名与内存快照字段列表
	hexStr := hex.EncodeToString(sum[:])
	mac1 := "00163e" + hexStr[0:6]
	mac2 := "00163e" + hexStr[6:12]
	mac3 := "00163e" + hexStr[12:18]
	hostName := "qoder-" + hexStr[18:26]
	tsMS := strconv.FormatInt(now.UnixMilli(), 10)
	memTotal := 4096000 + int(binary.BigEndian.Uint16(sum[13:15]))*32
	memFree := 1200000 + int(binary.BigEndian.Uint16(sum[15:17]))*16
	memInfo := fmt.Sprintf(
		"MemTotal:        %d kB\nMemFree:         %d kB\nMemAvailable:    %d kB\nBuffers:           66568 kB\nCached:          1752368 kB\n",
		memTotal, memFree, memFree+1500000,
	)
	items := []string{
		"1000", tsMS, "0", "1", "4", "1.5.0",
		"Linux",
		fmt.Sprintf("Linux version 6.18.40.1-standard (root@%s) (gcc (GCC) 13.2.0) #1 SMP PREEMPT_DYNAMIC", hexStr[26:38]),
		"runtime-info-linux",
		"", "",
		mac1, mac1, "", mac1[:8], mac1,
		"", "", "",
		hostName,
		"", "",
		"00000000-0000-0000-0000-" + mac1,
		"00000000-0000-0000-0000-" + mac1,
		"", "", "", "",
		memInfo,
		"", "", "",
		fmt.Sprintf("%s    %s    %s    ", mac1, mac2, mac3),
		"", "", "", "", "", "", "", "", "", "",
	}
	// 2. 序列化长度前缀字符串并执行 PKCS#7 填充与块混淆
	var sec1Buf bytes.Buffer
	for _, it := range items {
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(it)))
		sec1Buf.Write(lenBuf[:])
		sec1Buf.WriteString(it)
	}
	padded1B := pkcs7Pad(sec1Buf.Bytes(), 16)
	trans1B := make([]byte, len(padded1B))
	for off := 0; off < len(padded1B); off += 16 {
		encUMIDBlock1B(trans1B[off:off+16], padded1B[off:off+16])
	}
	return trans1B, hostName
}

// buildUMIDRequestBody 根据种子构造发往 repPc.json 的 Base64 加密请求报文包。
// [参数] seedKey: 账号与盐值组合种子；now: 当前时间。
// [返回] 加密请求报文包指针与错误。
// 最近修改时间：2026-10-11 15:05:00 收敛返回值为结构体并控制函数行数
func buildUMIDRequestBody(seedKey string, now time.Time) (*umidRequestPacket, error) {
	// 1. 生成 Section 1 分块并与其余三个固定段拼接封装
	sum := sha256.Sum256([]byte("qoder-umid-" + seedKey))
	trans1B, hostName := buildUMIDSection1B(sum, now)
	s0 := wrapUMIDSection(umidSectionSpec{Tag: 0x1a, Blocks: umidConstBlock1A[:], Mask: sum[0:4]})
	s1 := wrapUMIDSection(umidSectionSpec{Tag: 0x1b, Blocks: trans1B, Mask: sum[4:8]})
	s2 := wrapUMIDSection(umidSectionSpec{Tag: 0x1c, Blocks: umidConstBlock1C[:], Mask: sum[8:12]})
	s3 := wrapUMIDSection(umidSectionSpec{Tag: 0x1d, Blocks: umidConstBlock1D[:], Mask: sum[12:16]})
	sections := bytes.Join([][]byte{s0, s1, s2, s3}, nil)
	// 2. 添加外层 XOR 头部并执行 zlib 压缩与 AES-128-CBC 加密
	var outerXK [4]byte
	if _, err := rand.Read(outerXK[:]); err != nil {
		copy(outerXK[:], sum[16:20])
	}
	totalLen := uint32(12 + len(sections))
	fullPlain := make([]byte, totalLen)
	copy(fullPlain[0:4], []byte{0xcf, 0xcd, 0x00, 0x2b})
	copy(fullPlain[4:8], outerXK[:])
	binary.BigEndian.PutUint32(fullPlain[8:12], totalLen)
	for i := 0; i < len(sections); i++ {
		fullPlain[12+i] = sections[i] ^ outerXK[i&3]
	}
	var zBuf bytes.Buffer
	zw := zlib.NewWriter(&zBuf)
	if _, err := zw.Write(fullPlain); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	const keyIdx = 7
	cipherBytes, err := aes128CBCEncrypt(zBuf.Bytes(), umidAESKeys[keyIdx][:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+len(cipherBytes))
	out[0] = byte(keyIdx + 1)
	copy(out[1:], cipherBytes)
	return &umidRequestPacket{
		BodyB64:  base64.StdEncoding.EncodeToString(out),
		HostName: hostName,
	}, nil
}

// decryptUMIDResponseDT 解密 UMID 响应中的 dt 字段并提取 eeid 令牌。
// [参数] dtB64: 响应 JSON 中的 dt Base64 字符串。
// [返回] 解密得到的 P1gA 机器令牌与错误。
// 最近修改时间：2026-10-11 15:05:00 新增 UMID 响应 dt 解密函数
func decryptUMIDResponseDT(dtB64 string) (string, error) {
	// 1. Base64 解码并校验首字节密钥索引与密文块对齐
	raw, err := base64.StdEncoding.DecodeString(dtB64)
	if err != nil {
		return "", fmt.Errorf("umid dt base64: %w", err)
	}
	if len(raw) < 17 {
		return "", fmt.Errorf("umid dt too short")
	}
	idx := int(raw[0]) - 1
	if idx < 0 || idx >= len(umidAESKeys) {
		return "", fmt.Errorf("umid key index out of range: %d", idx)
	}
	// 2. 执行 AES-128-CBC 解密并解析内部 eeid JSON
	plain, err := aes128CBCDecrypt(raw[1:], umidAESKeys[idx][:])
	if err != nil {
		return "", err
	}
	var inner struct {
		EEID string `json:"eeid"`
	}
	if err := json.Unmarshal(plain, &inner); err != nil {
		return "", fmt.Errorf("umid eeid parse: %w", err)
	}
	if inner.EEID == "" {
		return "", fmt.Errorf("umid empty eeid")
	}
	return inner.EEID, nil
}

// deriveUMIDCodes 按官方 runtime-info 的 xxh64 算法从 machineToken 派生 machineType 与 machineCode。
// [参数] in: 包含 machineToken、vmCode 与 pct 的派生输入结构。
// [返回] 包含 18 字符 machineType 与 machineCode 的派生结果结构。
// 最近修改时间：2026-10-11 15:05:00 收敛多参数为 umidDeriveInput 结构体
func deriveUMIDCodes(in umidDeriveInput) umidDerivedCodes {
	// 1. 对 machineToken 计算 xxh64 并在第 8 位插入两位 vmCode 得到 machineType
	h1 := fmt.Sprintf("%016x", xxh64Sum([]byte(in.MachineToken)))
	machineType := fmt.Sprintf("%s%02d%s", h1[:8], in.VMCode, h1[8:])
	// 2. 对 machineType 计算 xxh64 并在第 8 位插入修正后的百分比得到 machineCode
	p := in.Pct
	if p >= 51 {
		p--
	}
	h2 := fmt.Sprintf("%016x", xxh64Sum([]byte(machineType)))
	machineCode := fmt.Sprintf("%s%02d%s", h2[:8], p, h2[8:])
	return umidDerivedCodes{
		MachineType: machineType,
		MachineCode: machineCode,
	}
}
