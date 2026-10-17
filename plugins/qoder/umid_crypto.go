// Adapted from luode0320/cpa-workbuddy-plugin/qoder-ai (806c61f).
// Copyright (c) 2026 Sliverkiss (based on workbuddy by lovingfish).
// Licensed under the MIT license in LICENSE.
// umid_crypto.go 提供 UMID 协议内部使用的分块混淆、PKCS#7 填充、AES-128-CBC 与 xxh64 纯函数。
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"math/bits"
)

// encUMIDBlock1B 对 16 字节分块执行 Section 0x1b 的字级循环移位与掩码异或。
// [参数] dst: 16 字节输出切片；src: 16 字节输入切片。
// [返回] 无。
// 最近修改时间：2026-10-11 14:56:00 新增 Section 0x1b 分块编码函数
func encUMIDBlock1B(dst, src []byte) {
	// 1. 按小端读取 4 个 uint32 并执行跨字移位混淆与常量异或
	var w [4]uint32
	for i := 0; i < 4; i++ {
		w[i] = binary.LittleEndian.Uint32(src[i*4 : (i+1)*4])
	}
	x1 := [4]uint32{w[2], w[3], w[0], w[1]}
	var comb [4]uint32
	for i := 0; i < 4; i++ {
		comb[i] = (w[i] << 17) | (x1[i] >> 15)
	}
	shuf := [4]uint32{comb[1], comb[2], comb[3], comb[0]}
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(dst[i*4:(i+1)*4], shuf[i]^umidConst1BWords[i])
	}
}

// wrapUMIDSection 将变换后的分块附加 4 字节掩码并封装为 cfcd 段结构。
// [参数] spec: 包含段标签、变换分块与 4 字节掩码的规格结构。
// [返回] 封装后的段字节切片。
// 最近修改时间：2026-10-11 15:05:00 收敛段封装参数为 umidSectionSpec 结构体
func wrapUMIDSection(spec umidSectionSpec) []byte {
	// 1. 构造段头并对载荷执行偏移掩码异或
	slen := uint32(len(spec.Blocks) + 11)
	out := make([]byte, slen)
	out[0] = 0xcf
	out[1] = 0xcd
	binary.BigEndian.PutUint32(out[2:6], slen)
	out[6] = spec.Tag
	copy(out[7:11], spec.Mask[:4])
	for i := 0; i < len(spec.Blocks); i++ {
		out[11+i] = spec.Blocks[i] ^ spec.Mask[(i+2)&3]
	}
	return out
}

// pkcs7Pad 对字节切片执行 PKCS#7 块对齐填充。
// [参数] data: 原始字节切片；blockSize: 块大小。
// [返回] 填充后的字节切片。
// 最近修改时间：2026-10-11 14:56:00 新增 PKCS#7 填充辅助函数
func pkcs7Pad(data []byte, blockSize int) []byte {
	// 1. 计算补充字节数并追加至末尾
	pad := blockSize - (len(data) % blockSize)
	return append(append([]byte(nil), data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// aes128CBCEncrypt 使用同值 Key 与 IV 执行 AES-128-CBC 加密。
// [参数] plain: 明文字节；key: 16 字节密钥（兼作 IV）。
// [返回] 密文字节切片与错误。
// 最近修改时间：2026-10-11 14:56:00 新增 UMID AES-128-CBC 加密函数
func aes128CBCEncrypt(plain []byte, key []byte) ([]byte, error) {
	// 1. 创建密码块并对 PKCS#7 填充后的明文执行 CBC 加密
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plain, aes.BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(out, padded)
	return out, nil
}

// aes128CBCDecrypt 使用同值 Key 与 IV 执行 AES-128-CBC 解密并剥离 PKCS#7 填充。
// [参数] ct: 密文字节；key: 16 字节密钥（兼作 IV）。
// [返回] 明文字节切片与错误。
// 最近修改时间：2026-10-11 14:56:00 新增 UMID AES-128-CBC 解密函数
func aes128CBCDecrypt(ct []byte, key []byte) ([]byte, error) {
	// 1. 校验密文长度并执行 CBC 解密与 PKCS#7 去填充
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("cipher length %d not aligned", len(ct))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, key).CryptBlocks(out, ct)
	pad := int(out[len(out)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(out) {
		return nil, fmt.Errorf("invalid pkcs7 pad %d", pad)
	}
	for i := len(out) - pad; i < len(out); i++ {
		if int(out[i]) != pad {
			return nil, fmt.Errorf("corrupted pkcs7 pad")
		}
	}
	return out[:len(out)-pad], nil
}

// xxh64Sum 计算 seed=0 的标准 XXH64 哈希值。
// [参数] data: 待哈希字节切片。
// [返回] 64 位无符号哈希结果。
// 最近修改时间：2026-10-11 14:56:00 新增标准 XXH64 哈希计算函数
func xxh64Sum(data []byte) uint64 {
	// 1. 处理 32 字节并行通道主循环
	n := len(data)
	idx := 0
	var h uint64
	if n >= 32 {
		var seed uint64
		v1 := seed + xxhPrime1 + xxhPrime2
		v2 := seed + xxhPrime2
		v3 := seed
		v4 := seed - xxhPrime1
		for idx <= n-32 {
			v1 = bits.RotateLeft64(v1+binary.LittleEndian.Uint64(data[idx:idx+8])*xxhPrime2, 31) * xxhPrime1
			v2 = bits.RotateLeft64(v2+binary.LittleEndian.Uint64(data[idx+8:idx+16])*xxhPrime2, 31) * xxhPrime1
			v3 = bits.RotateLeft64(v3+binary.LittleEndian.Uint64(data[idx+16:idx+24])*xxhPrime2, 31) * xxhPrime1
			v4 = bits.RotateLeft64(v4+binary.LittleEndian.Uint64(data[idx+24:idx+32])*xxhPrime2, 31) * xxhPrime1
			idx += 32
		}
		h = bits.RotateLeft64(v1, 1) + bits.RotateLeft64(v2, 7) + bits.RotateLeft64(v3, 12) + bits.RotateLeft64(v4, 18)
		for _, v := range [4]uint64{v1, v2, v3, v4} {
			k := bits.RotateLeft64(v*xxhPrime2, 31) * xxhPrime1
			h = (h^k)*xxhPrime1 + xxhPrime4
		}
	} else {
		h = xxhPrime5
	}
	// 2. 处理尾部剩余字节并执行雪崩混合
	h += uint64(n)
	for idx <= n-8 {
		k1 := bits.RotateLeft64(binary.LittleEndian.Uint64(data[idx:idx+8])*xxhPrime2, 31) * xxhPrime1
		h = bits.RotateLeft64(h^k1, 27)*xxhPrime1 + xxhPrime4
		idx += 8
	}
	if idx <= n-4 {
		h = bits.RotateLeft64(h^uint64(binary.LittleEndian.Uint32(data[idx:idx+4]))*xxhPrime1, 23)*xxhPrime2 + xxhPrime3
		idx += 4
	}
	for idx < n {
		h = bits.RotateLeft64(h^uint64(data[idx])*xxhPrime5, 11) * xxhPrime1
		idx++
	}
	h ^= h >> 33
	h *= xxhPrime2
	h ^= h >> 29
	h *= xxhPrime3
	h ^= h >> 32
	return h
}
