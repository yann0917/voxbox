package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/voicelib"
)

// maxVoiceUploadBytes 参考音频原始文件大小上限：20MB。
const maxVoiceUploadBytes = 20 << 20

// failVoice 音色库错误映射：ErrNotFound→6（资源不存在），其余→2（name 缺失、
// 文件太短/超限、转码失败、ffmpeg 缺失——对调用方都是提交内容或环境不可用）。
func failVoice(c *gin.Context, err error) {
	if errors.Is(err, voicelib.ErrNotFound) {
		fail(c, CodeNotFound, err.Error())
		return
	}
	fail(c, CodeBadRequest, err.Error())
}

// listVoiceLib GET /api/voice-library：全量音色（创建时间倒序）。
func (s *Server) listVoiceLib(c *gin.Context) {
	ok(c, gin.H{"items": s.svc.VoiceLibrary().List()})
}

// addVoiceLib POST /api/voice-library（multipart: name + file）：原始文件落临时文件
// → Library.Add 转码入库（defer 删临时文件）。20MB 上限在解析期即截断：MaxBytesReader
// 必须挂在 PostForm/FormFile（二者触发 ParseMultipartForm 无界解析，>32MB 部分落 /tmp）
// 之前，超大请求体在解析期被掐断报错，不留永久临时文件；fh.Size 校验兜底
// （正文未超读限但文件超过 20MB）。
func (s *Server) addVoiceLib(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxVoiceUploadBytes+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, CodeBadRequest, "上传失败: "+err.Error())
		return
	}
	if fh.Size > maxVoiceUploadBytes {
		fail(c, CodeBadRequest, "文件超过大小上限（20MB）")
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		fail(c, CodeBadRequest, "参数错误：name 必填")
		return
	}
	// 扩展名只保留安全形态（.字母数字 ≤8 位）作 ffmpeg 输入格式提示，其余丢弃
	//（常见容器 ffmpeg 按内容探测即可）；怪异形态会污染 CreateTemp 模式（路径分隔符注入）。
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if len(ext) > 9 {
		ext = ""
	}
	for _, r := range ext {
		if r != '.' && !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'z') {
			ext = ""
			break
		}
	}
	tmp, err := os.CreateTemp("", "voxbox-voice-*"+ext)
	if err != nil {
		failErr(c, err)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// 手工落盘而非 c.SaveUploadedFile：后者会无条件 Chmod 目标目录（0o750），
	// 目标在系统临时目录时等于 chmod 共享临时根，既越权又脆弱。
	src, err := fh.Open()
	if err != nil {
		failErr(c, err)
		return
	}
	defer src.Close()
	if _, err := io.Copy(tmp, src); err != nil {
		failErr(c, err)
		return
	}
	if err := tmp.Close(); err != nil {
		failErr(c, err)
		return
	}
	v, err := s.svc.VoiceLibrary().Add(c.Request.Context(), name, tmpPath)
	if err != nil {
		failVoice(c, err)
		return
	}
	ok(c, v)
}

// streamVoiceLib GET /api/voice-library/:id/stream：成品 wav 二进制流（与产物
// stream 同款：Accept-Ranges + http.ServeFile，不套 JSON 包络，404 用真实状态码）。
func (s *Server) streamVoiceLib(c *gin.Context) {
	abs, err := s.svc.VoiceLibrary().Path(c.Param("id"))
	if err != nil {
		c.JSON(404, gin.H{"error": "音色不存在"})
		return
	}
	c.Header("Accept-Ranges", "bytes")
	http.ServeFile(c.Writer, c.Request, abs)
}

type patchVoiceReq struct {
	Name string `json:"name"`
}

// patchVoiceLib PATCH /api/voice-library/:id：改名（body {name}）。
func (s *Server) patchVoiceLib(c *gin.Context) {
	var req patchVoiceReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		fail(c, CodeBadRequest, "参数错误：name 必填")
		return
	}
	if err := s.svc.VoiceLibrary().Rename(c.Param("id"), req.Name); err != nil {
		failVoice(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

// deleteVoiceLib DELETE /api/voice-library/:id：删除音色目录。
func (s *Server) deleteVoiceLib(c *gin.Context) {
	if err := s.svc.VoiceLibrary().Delete(c.Param("id")); err != nil {
		failVoice(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}
