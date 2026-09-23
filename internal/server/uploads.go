package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// maxUploadBytes 上传文件大小上限：500MB。
const maxUploadBytes = 500 << 20

// errInvalidFileID 标记 file_id 非法（非 UUID，疑似路径注入）。
var errInvalidFileID = errors.New("非法 file_id")

// uploadsDir 返回上传文件根目录：dataDir/uploads。
func (s *Server) uploadsDir() string {
	return filepath.Join(s.svc.Config().DataDir, "uploads")
}

// uploadFile 处理 POST /api/uploads：multipart 字段 file 落盘为
// <dataDir>/uploads/<userID>/<uuid>-<净化原名><ext>（公网多用户分桶；部署前存量在根目录），
// 返回 {file_id}（即 uuid，不含扩展名）。上传端不做格式限制（格式合法性由 Tool 侧报错），
// 仅限制大小与要求扩展名。
func (s *Server) uploadFile(c *gin.Context) {
	p := principalFrom(c)
	// 前置截断：MaxBytesReader 含 10MB multipart 编码开销余量，
	// 超大请求体在 multipart 解析前即被拒绝，不再落临时文件。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes+10<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, CodeBadRequest, "上传失败: "+err.Error())
		return
	}
	if fh.Size > maxUploadBytes {
		fail(c, CodeBadRequest, "文件超过大小上限（500MB）")
		return
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if ext == "" {
		// 无扩展名落盘为 <uuid>（无点），解析端 Glob "<uuid>.*" 永不匹配，
		// file_id 必然不可用，直接拒绝（此分支在 MkdirAll 之前，不落盘）。
		fail(c, CodeBadRequest, "文件缺少扩展名（用于识别格式判断），请上传带扩展名的音频文件")
		return
	}
	dir := filepath.Join(s.uploadsDir(), p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		failErr(c, err)
		return
	}
	id := uuid.NewString()
	// 保留原始文件名（净化后拼在 uuid 后面）：下游分离/转换的产物命名要用它，
	// 否则用户下载产物只能拿到 uuid 名。Glob 按 "<uuid>-*" 前缀查找不受影响。
	orig := sanitizeUploadName(strings.TrimSuffix(fh.Filename, ext))
	dst := filepath.Join(dir, id+"-"+orig+ext)
	if err := c.SaveUploadedFile(fh, dst); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"file_id": id})
}

// sanitizeUploadName 原始文件名净化：保留中文等 Unicode 字母/数字与 ._ -() 空格，
// 其余折叠下划线并限长（超长截断到 64 rune），空名回落 "audio"。
func sanitizeUploadName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		keep := r == '.' || r == '-' || r == '_' || r == ' ' || r == '(' || r == ')' ||
			unicode.IsLetter(r) || unicode.IsNumber(r)
		if !keep {
			r = '_'
		}
		b.WriteRune(r)
		if b.Len() >= 64 {
			break
		}
	}
	out := strings.Trim(b.String(), " ._")
	if out == "" {
		out = "audio"
	}
	return out
}

// streamUpload 处理 GET /api/uploads/:id/stream：按 id 查找上传文件并以
// 二进制流返回（与 artifacts stream 同为二进制流端点，不套 JSON 包络，
// 404 用真实 HTTP 404）。可见范围与任务一致：本人分桶；admin 可见全部
// （含部署前落历史根目录的存量）。
func (s *Server) streamUpload(c *gin.Context) {
	p := principalFrom(c)
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(404, gin.H{"error": "上传文件不存在"})
		return
	}
	root := s.uploadsDir()
	// 新命名 <uuid>-<原名>.<ext> 优先，回落旧存量 <uuid>.<ext>
	patterns := []string{
		filepath.Join(root, p.ID, id+"-*"),
		filepath.Join(root, p.ID, id+".*"),
	}
	if p.IsAdmin() {
		patterns = append(patterns,
			filepath.Join(root, "*", id+"-*"), // 其他用户分桶
			filepath.Join(root, id+"-*"),      // 历史根目录
			filepath.Join(root, id+".*"),
		)
	}
	var matches []string
	for _, pat := range patterns {
		if m, gerr := filepath.Glob(pat); gerr == nil && len(m) > 0 {
			matches = m
			break
		}
	}
	if len(matches) == 0 {
		c.JSON(404, gin.H{"error": "上传文件不存在或已清理"})
		return
	}
	abs := filepath.Clean(matches[0])
	// jail 校验：路径必须封闭在 uploads 目录内（id 已是 UUID，此处兜底）。
	jail := filepath.Clean(root) + string(os.PathSeparator)
	if !strings.HasPrefix(abs+string(os.PathSeparator), jail) {
		c.JSON(404, gin.H{"error": "非法上传路径"})
		return
	}
	c.Header("Accept-Ranges", "bytes")
	// 同源直出二进制流的安全头：禁 MIME 嗅探 + 强制附件下载（XSS 防护面）。
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "attachment; filename=\""+filepath.Base(abs)+"\"")
	http.ServeFile(c.Writer, c.Request, abs)
}

// uploadSearchDirs 返回解析上传 file_id 时依次查找的目录：按任务所有者分桶
// （admin 重跑他人任务时，输入文件属于原所有者），admin 追加历史根目录——
// 部署前落根目录的存量文件视为 admin 所有；普通用户的历史任务（无主）本就不可见。
func (s *Server) uploadSearchDirs(ownerUserID string, p *Principal) []string {
	root := s.uploadsDir()
	dirs := []string{}
	if ownerUserID != "" {
		dirs = append(dirs, filepath.Join(root, ownerUserID))
	}
	if p.IsAdmin() {
		dirs = append(dirs, root)
	}
	return dirs
}

// fileIDsToFiles 将上传 file_id 解析为本地绝对路径，按提交顺序注入 Files：
// 第 1 个 key 为 "audio"（与既有单文件通道约定一致），第 2 个起为 "audio2"、
// "audio3"…（格式工厂音频拼接等多输入功能依赖该顺序；单文件工具忽略多余的）。
// 依次在 dirs 中按 <id><ext> Glob 查找（不落 DB，简单可靠）。
// 防御：id 必须是合法 UUID——id 会进 Glob 模式，非 UUID 一律拒绝防路径注入。
func fileIDsToFiles(dirs []string, fileIDs []string) (map[string]string, error) {
	if len(fileIDs) == 0 {
		return nil, nil
	}
	files := make(map[string]string, len(fileIDs))
	for i, id := range fileIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("%w: %s", errInvalidFileID, id)
		}
		var match string
		for _, dir := range dirs {
			matches, err := filepath.Glob(filepath.Join(dir, id+"-*"))
			if err != nil || len(matches) == 0 {
				// 旧存量文件是 <uuid>.<ext> 形态
				matches, err = filepath.Glob(filepath.Join(dir, id+".*"))
			}
			if err == nil && len(matches) > 0 {
				match = matches[0]
				break
			}
		}
		if match == "" {
			return nil, fmt.Errorf("上传文件不存在或已清理: %s", id)
		}
		key := "audio"
		if i > 0 {
			key = fmt.Sprintf("audio%d", i+1)
		}
		files[key] = match
	}
	return files, nil
}
