package volcengine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// bridgeObjectName：音乐链路的落盘名（歌名 - 歌手）必须原样保留到对象 key 里
// （不拼随机串），上传通道的 uuid 磁盘名保持原样。
func TestBridgeObjectName(t *testing.T) {
	t.Run("歌名文件名原样保留", func(t *testing.T) {
		got := bridgeObjectName("/data/music/稻香 - 周杰伦.flac")
		if got != "稻香 - 周杰伦.flac" {
			t.Fatalf("object name = %q, want 原样（不拼随机串）", got)
		}
	})
	t.Run("上传通道uuid名保持原样", func(t *testing.T) {
		id := uuid.NewString()
		if got := bridgeObjectName(filepath.Join(t.TempDir(), id+".mp3")); got != id+".mp3" {
			t.Fatalf("upload name = %q, want bare uuid", got)
		}
	})
	t.Run("路径分隔符与控制字符折叠", func(t *testing.T) {
		got := bridgeObjectName("/x/a/b\x01c.mp3")
		if got != "b_c.mp3" || strings.ContainsAny(got, "/\\\x01") {
			t.Fatalf("object name = %q, want b_c.mp3 (目录与控制字符不进对象名)", got)
		}
	})
}
