// 播客 v3 WS 帧协议（火山引擎 podcasttts，官方文档 6561/1668014 逐字节核实）。
//
// 帧格式：4 字节头 + 可选字段，大端。byte0=0x11（协议版本 v1 + header size 1）；
// byte1=消息类型(高 4 位)+flags(低 4 位)；byte2=序列化(高 4 位)+压缩(低 4 位)；byte3=0x00。
//   - 上行 StartSession/FinishConnection：byte1=0x94（Full-client request + with event number），
//     byte2=0x10（JSON + 无压缩）；帧体 = event(4B) + session_id_len(4B) + session_id + payload_len(4B) + payload。
//   - 下行文本帧 byte1=0x94、音频帧 byte1=0xB4，帧体同上（音频 payload 为原始音频字节）。
//   - 错误帧 byte1=0xF0，帧体 = 错误码(4B) + 消息长度(4B) + 消息 JSON。
package volcengine

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// 播客 v3 协议 event code。
const (
	EventStartSession       = 100 // 上行：开始会话
	EventFinishConnection   = 2   // 上行：关闭连接
	EventSessionStarted     = 150 // 下行：会话已建立
	EventSessionFinished    = 152 // 下行：会话结束（合成完成）
	EventUsageResponse      = 154 // 下行：用量上报
	EventRoundStart         = 360 // 下行：轮次开始（round_id=-1 开头音乐、9999 结尾音频）
	EventRoundResponse      = 361 // 下行：轮次音频分片
	EventRoundEnd           = 362 // 下行：轮次结束
	EventPodcastEnd         = 363 // 下行：播客结束（携带 audio_url 等 meta_info）
	EventConnectionFinished = 52  // 下行：连接关闭确认
)

// 消息类型（byte1 高 4 位）、flags 与序列化/压缩（byte2）取值。
const (
	msgTypeFullClientRequest = 0x9 // byte1 = 0x94
	msgTypeAudioOnlyResponse = 0xB // byte1 = 0xB4
	msgTypeErrorResponse     = 0xF // byte1 = 0xF0
	flagWithEventNumber      = 0b0100
	serializationJSON        = 0b0001
	compressionGzip          = 0b0001
)

// PodFrame 为解析后的播客 v3 下行帧。
type PodFrame struct {
	Event     int // 150/360/361/362/154/363/152/52；错误帧时 0
	SessionID string
	Audio     []byte // 仅 361 音频帧
	Payload   []byte // 文本 JSON（原样，未解析）
	ErrCode   int64  // 仅错误帧
	ErrMsg    string // 仅错误帧（message JSON 中的 message 字段或原文）
}

// BuildStartSession 构造上行 StartSession 帧（event 100，JSON payload，无压缩）。
func BuildStartSession(sessionID string, payloadJSON []byte) []byte {
	return buildClientRequest(EventStartSession, sessionID, payloadJSON)
}

// BuildFinishConnection 构造上行 FinishConnection 帧（event 2，空 payload）。
func BuildFinishConnection(sessionID string) []byte {
	return buildClientRequest(EventFinishConnection, sessionID, nil)
}

// buildClientRequest 构造上行帧：4 字节头（0x94 + JSON 无压缩）+ event + session_id + payload。
func buildClientRequest(event int, sessionID string, payload []byte) []byte {
	frame := make([]byte, 0, 4+4+4+len(sessionID)+4+len(payload))
	frame = append(frame, 0x11, msgTypeFullClientRequest<<4|flagWithEventNumber, serializationJSON<<4, 0x00)
	frame = binary.BigEndian.AppendUint32(frame, uint32(event))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(sessionID)))
	frame = append(frame, sessionID...)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	return frame
}

// frameReader 对帧体的带边界检查读取器。
type frameReader struct {
	data []byte
	off  int
}

func (r *frameReader) u32(name string) (uint32, error) {
	if r.off+4 > len(r.data) {
		return 0, fmt.Errorf("%s 不完整：帧体剩余 %d 字节，需要 4 字节", name, len(r.data)-r.off)
	}
	v := binary.BigEndian.Uint32(r.data[r.off : r.off+4])
	r.off += 4
	return v, nil
}

func (r *frameReader) take(name string, n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.data) {
		return nil, fmt.Errorf("%s 长度 %d 超出帧体实际剩余 %d 字节", name, n, len(r.data)-r.off)
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b, nil
}

// ParsePodFrame 解析播客 v3 下行帧，按 byte1 分流：0xB4 音频 / 0x94 文本 / 0xF0 错误。
func ParsePodFrame(data []byte) (PodFrame, error) {
	if len(data) < 4 {
		return PodFrame{}, fmt.Errorf("帧头不完整：%d 字节，至少需要 4 字节", len(data))
	}
	headerSize := int(data[0]&0x0F) * 4
	if headerSize == 0 || headerSize > len(data) {
		return PodFrame{}, fmt.Errorf("帧头长度 %d 非法（帧总长 %d 字节）", headerSize, len(data))
	}
	msgType := data[1] >> 4
	flags := data[1] & 0x0F
	serialization := data[2] >> 4
	compression := data[2] & 0x0F

	r := &frameReader{data: data[headerSize:]}
	switch msgType {
	case msgTypeErrorResponse: // 错误帧：错误码(4B) + 消息长度(4B) + 消息 JSON
		code, err := r.u32("错误码")
		if err != nil {
			return PodFrame{}, err
		}
		msgLen, err := r.u32("错误消息长度")
		if err != nil {
			return PodFrame{}, err
		}
		msg, err := r.take("错误消息", int(msgLen))
		if err != nil {
			return PodFrame{}, err
		}
		if compression == compressionGzip {
			if msg, err = gunzip(msg); err != nil {
				return PodFrame{}, fmt.Errorf("解压错误消息失败: %w", err)
			}
		}
		return PodFrame{ErrCode: int64(code), ErrMsg: extractErrMessage(msg)}, nil

	case msgTypeFullClientRequest, msgTypeAudioOnlyResponse: // 文本帧 / 音频帧
		if flags != flagWithEventNumber {
			return PodFrame{}, fmt.Errorf("未知帧 flags: %#x", flags)
		}
		event, err := r.u32("event")
		if err != nil {
			return PodFrame{}, err
		}
		sidLen, err := r.u32("session_id 长度")
		if err != nil {
			return PodFrame{}, err
		}
		sid, err := r.take("session_id", int(sidLen))
		if err != nil {
			return PodFrame{}, err
		}
		payloadLen, err := r.u32("payload 长度")
		if err != nil {
			return PodFrame{}, err
		}
		payload, err := r.take("payload", int(payloadLen))
		if err != nil {
			return PodFrame{}, err
		}
		f := PodFrame{Event: int(event), SessionID: string(sid)}
		if msgType == msgTypeAudioOnlyResponse {
			// 音频帧 payload 为原始音频字节（无压缩），即使带 compression 位也原样返回。
			f.Audio = payload
			return f, nil
		}
		// 文本帧 payload 为 JSON；示例帧均无压缩，gzip 为防御性支持。
		if compression == compressionGzip {
			if payload, err = gunzip(payload); err != nil {
				return PodFrame{}, fmt.Errorf("解压文本帧 payload 失败: %w", err)
			}
		}
		if serialization != serializationJSON {
			return PodFrame{}, fmt.Errorf("未知文本帧序列化类型: %#x", serialization)
		}
		f.Payload = payload
		return f, nil

	default:
		return PodFrame{}, fmt.Errorf("未知帧类型: %#x", data[1])
	}
}

// extractErrMessage 从错误消息 JSON 提取 "message" 字段；解析失败或为空时返回原文。
func extractErrMessage(msg []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(msg, &m); err == nil && m.Message != "" {
		return m.Message
	}
	return string(msg)
}

// gunzip 解压 gzip 数据。
func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
