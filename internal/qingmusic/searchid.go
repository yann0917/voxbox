package qingmusic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// TxSearchID 复刻 App 的 createTxSearchId()：QQ 搜索请求体里的 searchid——
// 大写 UUID（去横线）+ 5 位随机数。从 device.go 摘出：toolbox 只需要搜索/解析链路，
// 不迁移 MMKV 替代存储与设备号体系。
func TxSearchID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "0000000000000000000000000000000000000"
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant RFC4122
	n, err := rand.Int(rand.Reader, big.NewInt(100000))
	if err != nil {
		n = big.NewInt(0)
	}
	return strings.ToUpper(hex.EncodeToString(buf)) + fmt.Sprintf("%05d", n.Int64())
}
