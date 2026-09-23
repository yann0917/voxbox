// 火山引擎官方 sauc_go demo 协议实现（来源：https://www.volcengine.com/docs/6561/2628951，sauc_go.zip protocol 包）。
// 本项目做了以下最小改造：
//   - 包名 protocol → sauc；
//   - 序列化由 bytedance/sonic 改为标准库 encoding/json（避免为本包引入额外依赖）；
//   - AudioMeta 增加 Language 字段（`json:"language,omitempty"`，协议文档 6561/1354869 支持 audio.language，官方结构体未收录）；
//   - 修正 UserMeta.Platform struct tag 末尾多余空格（官方源码笔误，无语义影响）。
package sauc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
)

type UserMeta struct {
	Uid        string `json:"uid,omitempty"`
	Did        string `json:"did,omitempty"`
	Platform   string `json:"platform,omitempty"`
	SDKVersion string `json:"sdk_version,omitempty"`
	APPVersion string `json:"app_version,omitempty"`
}

type AudioMeta struct {
	Format   string `json:"format,omitempty"`
	Codec    string `json:"codec,omitempty"`
	Rate     int    `json:"rate,omitempty"`
	Bits     int    `json:"bits,omitempty"`
	Channel  int    `json:"channel,omitempty"`
	Language string `json:"language,omitempty"`
}

type CorpusMeta struct {
	BoostingTableName string `json:"boosting_table_name,omitempty"`
	CorrectTableName  string `json:"correct_table_name,omitempty"`
	Context           string `json:"context,omitempty"`
}

type RequestMeta struct {
	ModelName       string     `json:"model_name,omitempty"`
	EnableITN       bool       `json:"enable_itn,omitempty"`
	EnablePUNC      bool       `json:"enable_punc,omitempty"`
	EnableDDC       bool       `json:"enable_ddc,omitempty"`
	ShowUtterances  bool       `json:"show_utterances"`
	EnableNonstream bool       `json:"enable_nonstream"`
	Corpus          CorpusMeta `json:"corpus,omitempty"`
}

type AsrRequestPayload struct {
	User    UserMeta    `json:"user"`
	Audio   AudioMeta   `json:"audio"`
	Request RequestMeta `json:"request"`
}

func NewFullClientRequest(payload AsrRequestPayload) []byte {
	var request bytes.Buffer
	request.Write(DefaultHeader().WithMessageTypeSpecificFlags(POS_SEQUENCE).toBytes())

	payloadArr, _ := json.Marshal(payload)
	payloadArr = GzipCompress(payloadArr)
	payloadSize := len(payloadArr)
	payloadSizeArr := make([]byte, 4)
	binary.BigEndian.PutUint32(payloadSizeArr, uint32(payloadSize))
	_ = binary.Write(&request, binary.BigEndian, int32(1))
	request.Write(payloadSizeArr)
	request.Write(payloadArr)
	return request.Bytes()
}

func NewAudioOnlyRequest(seq int, segment []byte) []byte {
	var request bytes.Buffer
	header := DefaultHeader()
	if seq < 0 {
		header.WithMessageTypeSpecificFlags(NEG_WITH_SEQUENCE)
	} else {
		header.WithMessageTypeSpecificFlags(POS_SEQUENCE)
	}
	header.WithMessageType(CLIENT_AUDIO_ONLY_REQUEST)
	request.Write(header.toBytes())

	// write seq
	_ = binary.Write(&request, binary.BigEndian, int32(seq))
	// write payload size
	payload := GzipCompress(segment)
	_ = binary.Write(&request, binary.BigEndian, int32(len(payload)))
	// write payload
	request.Write(payload)
	return request.Bytes()
}
