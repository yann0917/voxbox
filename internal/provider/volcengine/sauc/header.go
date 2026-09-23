// 火山引擎官方 sauc_go demo 协议实现（来源：https://www.volcengine.com/docs/6561/2628951，sauc_go.zip protocol 包）。
// 本项目做了以下最小改造：
//   - 包名 protocol → sauc；
//   - 删除 env/config.toml 版 NewAuthHeader()（其依赖 config.go 的 ApiKey/AppKey/AccessKey/ResourceID 环境读取），
//     新增 NewAuthHeaderFrom(apiKey, appKey, accessKey, resourceID string)：apiKey 非空时用 X-Api-Key（新版控制台鉴权），
//     否则用 X-Api-App-Key + X-Api-Access-Key（老版控制台鉴权）；始终携带 X-Api-Resource-Id 与 X-Api-Request-Id（uuid）。
package sauc

import (
	"bytes"
	"net/http"

	"github.com/google/uuid"
)

type AsrRequestHeader struct {
	messageType              MessageType
	messageTypeSpecificFlags MessageTypeSpecificFlags
	serializationType        SerializationType
	compressionType          CompressionType
	reservedData             []byte
}

func (h *AsrRequestHeader) toBytes() []byte {
	header := bytes.NewBuffer([]byte{})
	header.WriteByte(byte(PROTOCOL_VERSION<<4 | 1))
	header.WriteByte(byte(h.messageType<<4) | byte(h.messageTypeSpecificFlags))
	header.WriteByte(byte(h.serializationType<<4) | byte(h.compressionType))
	header.Write(h.reservedData)
	return header.Bytes()
}

func (h *AsrRequestHeader) WithMessageType(messageType MessageType) *AsrRequestHeader {
	h.messageType = messageType
	return h
}

func (h *AsrRequestHeader) WithMessageTypeSpecificFlags(messageTypeSpecificFlags MessageTypeSpecificFlags) *AsrRequestHeader {
	h.messageTypeSpecificFlags = messageTypeSpecificFlags
	return h
}

func (h *AsrRequestHeader) WithSerializationType(serializationType SerializationType) *AsrRequestHeader {
	h.serializationType = serializationType
	return h
}

func (h *AsrRequestHeader) WithCompressionType(compressionType CompressionType) *AsrRequestHeader {
	h.compressionType = compressionType
	return h
}

func (h *AsrRequestHeader) WithReservedData(reservedData []byte) *AsrRequestHeader {
	h.reservedData = reservedData
	return h
}

func DefaultHeader() *AsrRequestHeader {
	return &AsrRequestHeader{
		messageType:              CLIENT_FULL_REQUEST,
		messageTypeSpecificFlags: POS_SEQUENCE,
		serializationType:        JSON,
		compressionType:          GZIP,
		reservedData:             []byte{0x00},
	}
}

// NewAuthHeaderFrom 按参数注入构造鉴权 Header（替代官方从 env/config.toml 读取）。
func NewAuthHeaderFrom(apiKey, appKey, accessKey, resourceID string) http.Header {
	reqid := uuid.New().String()
	header := http.Header{}

	header.Add("X-Api-Resource-Id", resourceID)
	header.Add("X-Api-Request-Id", reqid)

	// 新版控制台鉴权: X-Api-Key；老版控制台鉴权: X-Api-App-Key + X-Api-Access-Key
	if apiKey != "" {
		header.Add("X-Api-Key", apiKey)
	} else {
		header.Add("X-Api-Access-Key", accessKey)
		header.Add("X-Api-App-Key", appKey)
	}
	return header
}
