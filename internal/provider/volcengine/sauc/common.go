// 火山引擎官方 sauc_go demo 协议实现（来源：https://www.volcengine.com/docs/6561/2628951，sauc_go.zip protocol 包）。
// 本项目做了以下最小改造：
//   - 包名 protocol → sauc；
//   - 删除 ConvertWavWithPath（依赖 ffmpeg 且会删除源音频文件，禁止收录）；
//   - GzipDecompress 中 ioutil.ReadAll 改为 io.ReadAll（ioutil 已废弃），并移除随之不再使用的 import
//     （io/ioutil、os、os/exec、strconv、time）。
//   - 其余（常量/GzipCompress/GzipDecompress/JudgeWav/WavHeader/ReadWavInfo/DefaultSampleRate）官方原样。
package sauc

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

const DefaultSampleRate = 16000

type ProtocolVersion byte
type MessageType byte
type MessageTypeSpecificFlags byte
type SerializationType byte
type CompressionType byte

const (
	PROTOCOL_VERSION = ProtocolVersion(0b0001)

	// Message Type:
	CLIENT_FULL_REQUEST       = MessageType(0b0001)
	CLIENT_AUDIO_ONLY_REQUEST = MessageType(0b0010)
	SERVER_FULL_RESPONSE      = MessageType(0b1001)
	SERVER_ERROR_RESPONSE     = MessageType(0b1111)

	// Message Type Specific Flags
	NO_SEQUENCE       = MessageTypeSpecificFlags(0b0000) // no check sequence
	POS_SEQUENCE      = MessageTypeSpecificFlags(0b0001)
	NEG_SEQUENCE      = MessageTypeSpecificFlags(0b0010)
	NEG_WITH_SEQUENCE = MessageTypeSpecificFlags(0b0011)

	// Message Serialization
	NO_SERIALIZATION = SerializationType(0b0000)
	JSON             = SerializationType(0b0001)

	// Message Compression
	GZIP = CompressionType(0b0001)
)

func GzipCompress(input []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(input)
	w.Close()
	return b.Bytes()
}

func GzipDecompress(input []byte) []byte {
	b := bytes.NewBuffer(input)
	r, _ := gzip.NewReader(b)
	out, _ := io.ReadAll(r)
	r.Close()
	return out
}

// JudgeWav 用于判断字节数组是否为有效的 WAV 文件
func JudgeWav(data []byte) bool {
	if len(data) < 44 {
		return false
	}
	if string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return true
	}
	return false
}

type WavHeader struct {
	ChunkID       [4]byte
	ChunkSize     uint32
	Format        [4]byte
	Subchunk1ID   [4]byte
	Subchunk1Size uint32
	AudioFormat   uint16
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	Subchunk2ID   [4]byte
	Subchunk2Size uint32
}

func ReadWavInfo(data []byte) (int, int, int, int, []byte, error) {
	reader := bytes.NewReader(data)
	var header WavHeader

	if err := binary.Read(reader, binary.LittleEndian, &header); err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("failed to read WAV header: %v", err)
	}

	nchannels := int(header.NumChannels)
	sampwidth := int(header.BitsPerSample / 8)
	framerate := int(header.SampleRate)
	nframes := int(header.Subchunk2Size) / (nchannels * sampwidth)

	waveBytes := make([]byte, header.Subchunk2Size)
	if _, err := io.ReadFull(reader, waveBytes); err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("failed to read WAV data: %v", err)
	}

	return nchannels, sampwidth, framerate, nframes, waveBytes, nil
}
