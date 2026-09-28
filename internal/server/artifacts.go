package server

// 产物流式播放与下载：二进制流端点，不套 JSON 包络，按真实 HTTP 语义返回。
// 附带产物路径解析（data 目录 jail 防御）与任务/产物越权判定。

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/store"
)

func (s *Server) streamArtifact(c *gin.Context) {
	a, err := s.svc.DB().GetArtifact(c.Param("id"))
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "产物不存在"})
		return
	}
	if !canAccessArtifact(a.UserID, principalFrom(c)) {
		c.JSON(404, gin.H{"error": "产物不存在"})
		return
	}
	abs, err := s.artifactAbsPath(a.Path)
	if err != nil {
		c.JSON(404, gin.H{"error": "产物文件缺失"})
		return
	}
	c.Header("Accept-Ranges", "bytes")
	http.ServeFile(c.Writer, c.Request, abs)
}

func (s *Server) downloadArtifact(c *gin.Context) {
	a, err := s.svc.DB().GetArtifact(c.Param("id"))
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "产物不存在"})
		return
	}
	if !canAccessArtifact(a.UserID, principalFrom(c)) {
		c.JSON(404, gin.H{"error": "产物不存在"})
		return
	}
	abs, err := s.artifactAbsPath(a.Path)
	if err != nil {
		c.JSON(404, gin.H{"error": "产物文件缺失"})
		return
	}
	c.FileAttachment(abs, a.Filename)
}

// artifactAbsPath 将产物相对路径解析到 data 目录下，防止路径穿越。
// Task 7 审查修正：CLI --out 重定向时产物路径可为绝对路径，直接使用；
// 相对路径才拼接到 data 目录。
// Task 9 审查修正：相对路径必须封闭在 data 目录内，`../` 逃逸一律拒绝。
func (s *Server) artifactAbsPath(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		// _out 契约：CLI 显式指定的绝对路径产物
		if _, err := os.Stat(rel); err != nil {
			return "", err
		}
		return rel, nil
	}
	abs := filepath.Join(s.svc.Config().DataDir, rel)
	dataRoot := filepath.Clean(s.svc.Config().DataDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(abs)+string(os.PathSeparator), dataRoot) {
		return "", fmt.Errorf("非法产物路径: %s", rel)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// canAccessTask 无主（空 UserID）任务=部署前本地存量，仅 admin 可见；其余本人或 admin。
func canAccessTask(ownerUserID string, p *Principal) bool {
	if ownerUserID == "" {
		return p.IsAdmin()
	}
	return ownerUserID == p.ID || p.IsAdmin()
}

func canAccessArtifact(ownerUserID string, p *Principal) bool { return canAccessTask(ownerUserID, p) }
