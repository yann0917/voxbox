// Package qingmusic 是 QingMusic 客户端的 Go 对接实现。
//
// 逆向来源：QingMusic v1.3.9 (arm)，assets/index.android.bundle（Hermes v96）
// 与 classes4.dex 的 com.qingmusic.changqing.SecureKwApiModule。
// 所有密钥、参数、签名算法均从二进制中提取，未做猜测。
package qingmusic

import (
	"crypto/aes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- 通用工具

func md5HexLower(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sha1HexLower(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pkcs7Pad 对应 Java 的 "AES/ECB/PKCS5Padding"（AES 分组 16 字节时 PKCS5 == PKCS7）。
func pkcs7Pad(b []byte, blockSize int) []byte {
	n := blockSize - len(b)%blockSize
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

// ---------------------------------------------------------------- 网易云 eapi

const (
	eapiKey = "e82ckenh8dichen8" // 16 字节 → AES-128
	eapiSep = "-36cd479b6b5-"
	// eapiURL 是 eapi 签名里用的“逻辑路径”，不是完整 URL。
	eapiURL = "/api/cloudsearch/pc"
)

// EapiEncrypt 复刻 SecureKwApiModule.eapiEncrypt(url, json)：
//
//	digest = md5hex("nobody" + url + "use" + json + "md5forencrypt")
//	data   = url + "-36cd479b6b5-" + json + "-36cd479b6b5-" + digest
//	out    = upperHex(AES-128-ECB(key="e82ckenh8dichen8", PKCS7(data)))
//
// 返回值直接作为表单字段 params 的值（Content-Type: application/x-www-form-urlencoded）。
func EapiEncrypt(url, payload string) string {
	digest := md5HexLower("nobody" + url + "use" + payload + "md5forencrypt")
	data := url + eapiSep + payload + eapiSep + digest

	block, err := aes.NewCipher([]byte(eapiKey))
	if err != nil {
		panic("qingmusic: aes init failed: " + err.Error())
	}
	padded := pkcs7Pad([]byte(data), block.BlockSize())

	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += block.BlockSize() {
		block.Encrypt(out[i:i+block.BlockSize()], padded[i:i+block.BlockSize()])
	}
	return strings.ToUpper(hex.EncodeToString(out))
}

// ---------------------------------------------------------------- QQ 音乐 zzc 签名

// 三个数组来自 SecureKwApiModule.<init> 里的 fill-array-data，逐字节还原。
var (
	txPart1Idx = []int{23, 14, 6, 36, 16, 40, 7, 19}
	txPart2Idx = []int{16, 1, 32, 12, 19, 27, 8, 5}
	txScramble = []int{
		89, 39, 179, 150, 218,
		82, 58, 252, 177, 52,
		186, 123, 120, 64, 242,
		133, 143, 161, 121, 179,
	}
)

// TxSign 复刻 SecureKwApiModule.createTxSearchSign(payload)：
//
//	hex    = lowerHex(SHA1(payload))
//	part1  = 拼接 hex 在 txPart1Idx 处的【单个字符】（越界取空串）
//	part2  = 拼接 hex 在 txPart2Idx 处的【单个字符】
//	buf[i] = byte(parseInt(hex[2i:2i+2], 16) ^ txScramble[i])
//	b64    = base64.StdEncoding(buf) 去掉 '/', '+', '='
//	sign   = lower("zzc" + part1 + b64 + part2)
//
// 用法：sign 拼到 URL 上 → https://u.y.qq.com/cgi-bin/musics.fcg?sign=<sign>，
// POST body 就是参与签名的同一个 payload 字符串。
func TxSign(payload string) string {
	h := sha1HexLower(payload)

	pick := func(idx []int) string {
		var b strings.Builder
		for _, i := range idx {
			if i >= 0 && i < len(h) {
				b.WriteByte(h[i])
			}
		}
		return b.String()
	}

	buf := make([]byte, len(txScramble))
	for i, s := range txScramble {
		v, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		if err != nil {
			continue
		}
		buf[i] = byte(int(v) ^ s)
	}

	b64 := base64.StdEncoding.EncodeToString(buf)
	b64 = strings.NewReplacer("/", "", "+", "", "=", "").Replace(b64)

	return strings.ToLower("zzc" + pick(txPart1Idx) + b64 + pick(txPart2Idx))
}

// ---------------------------------------------------------------- 咪咕签名

// 咪咕盐，来自 createMgSignature 里的字符串常量（分段拼接还原）。
const (
	mgSalt     = "6cdc72a439cef99a3418d2a78aa28c73" + "yyapp2" + "d16148780a1dcc7408e06336b98cfd50" + "963B7AA0D21511ED807EE5846EC87D20"
	mgDeviceID = "963B7AA0D21511ED807EE5846EC87D20"
	mgChannel  = "0146921"
	mgUIVer    = "A_music_3.6.1"
	mgUA       = "Mozilla/5.0 (Linux; U; Android 11.0.0; zh-cn; MI 11 Build/OPR1.170623.032) " +
		"AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30"
)

// MgSign 复刻 createMgSignature：md5hex(timestamp + 盐 + keyword)。
// 返回的 sign 与上面的 mgDeviceID 一起放进请求头。
func MgSign(timestamp, keyword string) string {
	return md5HexLower(timestamp + mgSalt + keyword)
}

// ---------------------------------------------------------------- 各平台固定 UA

const (
	uaChromeLinux = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/60.0.3112.90 Safari/537.36"
	uaWindows = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	uaQQMusic = "QQMusic 14090508(android 12)"
)
