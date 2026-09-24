package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/mvsep"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/store"
	"github.com/yann0917/voxbox/internal/subtitle"
	"github.com/yann0917/voxbox/internal/task"
)

func (s *Server) Handler() http.Handler {
	r := gin.Default()
	gin.SetMode(gin.ReleaseMode)
	// 公开端点：健康检查（探活不需要身份）。其余 /api 全部要求已登录。
	r.GET("/api/health", func(c *gin.Context) { ok(c, gin.H{"status": "ok"}) })

	// 登录门：login 自身公开（带失败限速）；me/password/token 需要会话。
	auth := r.Group("/api/auth")
	{
		auth.POST("/login", s.login)
		auth.POST("/logout", s.requireAuth(), s.logout)
		auth.GET("/me", s.requireAuth(), s.me)
		auth.POST("/password", s.requireAuth(), s.changePassword)
		auth.POST("/token/rotate", s.requireAuth(), s.rotateToken)
	}

	api := r.Group("/api", s.requireAuth())
	{
		api.GET("/tools", s.listTools)
		api.GET("/gsgc/health", s.gsgcHealth)
		api.POST("/uploads", s.uploadFile)
		api.GET("/uploads/:id/stream", s.streamUpload)
		api.POST("/tasks", s.createTask)
		api.GET("/tasks", s.listTasks)
		api.GET("/tasks/:id", s.getTask)
		api.DELETE("/tasks/:id", s.deleteTask)
		api.POST("/tasks/:id/cancel", s.cancelTask)
		api.POST("/tasks/:id/rerun", s.rerunTask)
		api.GET("/artifacts/:id/stream", s.streamArtifact)
		api.GET("/artifacts/:id/download", s.downloadArtifact)
		// 设置读写分离：GET 返回的卡态不含 secret 明文（登录用户可见），写入/连通性测试仅 admin。
		api.GET("/settings", s.getSettings)
		api.PUT("/settings/providers/:name", s.requireAdmin(), s.putProviderSettings)
		api.PUT("/settings/storage", s.requireAdmin(), s.putStorageSettings)
		api.POST("/settings/test-connection", s.requireAdmin(), s.testConnection)
		api.GET("/voices", s.listVoices)
		api.GET("/dicts", s.listDicts)
		api.GET("/search", s.searchTasks)
		api.POST("/subtitles/prepare", s.prepareSubtitles)
		api.POST("/subtitles/export", s.exportSubtitles)
		api.GET("/subtitles/presets", s.listSubtitlePresets)
		api.GET("/minutes/templates", s.listMinutesTemplates)
		api.GET("/minutes/:id/export", s.exportMinutes)
		api.GET("/mvsep/algorithms", s.listMVSepAlgorithms)
		api.GET("/mvsep/status", s.mvsepStatus)
		api.GET("/mvsep/history", s.mvsepHistory)
		api.GET("/mvsep/separation", s.mvsepSeparationGet)
		// WS 进度通道：登录后浏览器带 Cookie 升级；快照按用户过滤（admin 收全量）。
		api.GET("/ws", s.wsProgress)
	}
	// MCP Streamable HTTP：独立组只受 mcpAuth 门控（仅 Bearer API token，客户端不携带
	// Cookie），不与 requireAuth 叠加——否则无凭证请求会先被 Cookie 门拦下，401 响应体
	// 变成业务包络而非 JSON-RPC 错误。GET(SSE)/POST/DELETE 全由 handler 处理，不套 JSON 包络。
	if s.mcpHandler != nil {
		mcp := r.Group("/api", s.mcpAuth())
		mcp.Any("/mcp", gin.WrapH(s.mcpHandler))
	}
	return r
}

// wsProgress WebSocket 升级入口：requireAuth 已注入身份，快照闭包按用户收窄。
func (s *Server) wsProgress(c *gin.Context) {
	p := principalFrom(c)
	uid := p.ID
	if p.IsAdmin() {
		uid = "" // admin 快照收全量（含无主历史任务）
	}
	s.hub.serveWS(c.Writer, c.Request, func() []byte { return s.snapshotJSONFor(uid) })
}

func (s *Server) listTools(c *gin.Context) {
	out := []toolDTO{}
	for _, t := range s.svc.Registry().List() {
		tool, _ := s.svc.Registry().Get(t.Provider, t.Name)
		out = append(out, toolDTO{Meta: t, ParamSpecs: tool.ParamSpecs()})
	}
	ok(c, out)
}

type createTaskReq struct {
	Provider       string         `json:"provider"`
	Tool           string         `json:"tool"`
	Params         map[string]any `json:"params"`
	FileIDs        []string       `json:"file_ids"`        // /api/uploads 返回的上传文件 id
	ArtifactInput  string         `json:"artifact_input"`  // 已有产物 id（跨工具联动：如分离人声轨送 ASR）
	ArtifactInputs []string       `json:"artifact_inputs"` // 多产物输入按序 → audio/audio2…（mix：[伴奏id, 人声id]）
}

func (s *Server) createTask(c *gin.Context) {
	p := principalFrom(c)
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Provider == "" || req.Tool == "" {
		fail(c, CodeBadRequest, "参数错误：provider/tool 必填")
		return
	}
	// _out 是 CLI 内部约定（仅 cmd/voxbox 显式设置产物输出路径），
	// Web 用户不可通过 params 透传，否则 TTS Tool 会把产物写到服务器任意路径。
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	delete(req.Params, "_out")
	// 产物/文件输入通道三选一：artifact_input（单产物）、artifact_inputs（多产物按序）、
	// file_ids（上传文件）——任何两个同传都拒绝。互斥在产物存在性校验之前执行。
	channels := 0
	if req.ArtifactInput != "" {
		channels++
	}
	if len(req.ArtifactInputs) > 0 {
		channels++
	}
	if len(req.FileIDs) > 0 {
		channels++
	}
	if channels > 1 {
		fail(c, CodeBadRequest, "artifact_input、artifact_inputs、file_ids 只能提供其一")
		return
	}
	// artifact_input 单值归一为长度 1 的 artifact_inputs，与数组共用同一条解析链：
	// GetArtifact + 越权同报不存在（不泄露他人产物存在性）+ artifactAbsPath（IsAbs/jail 防御）。
	artifactIDs := req.ArtifactInputs
	if req.ArtifactInput != "" {
		artifactIDs = []string{req.ArtifactInput}
	}
	var files map[string]string
	switch {
	case len(artifactIDs) > 0:
		f, err := s.resolveArtifactInputs(artifactIDs, p)
		if err != nil {
			switch {
			case errors.Is(err, errArtifactNotFound):
				fail(c, CodeNotFound, "产物不存在")
			case errors.Is(err, errArtifactMissing):
				fail(c, CodeNotFound, "产物文件缺失")
			default:
				failErr(c, err)
			}
			return
		}
		files = f
	case len(req.FileIDs) > 0:
		// file_ids → 上传文件绝对路径（key 固定 "audio"），交给 Engine 走本地文件通道。
		f, err := fileIDsToFiles(s.uploadSearchDirs(p.ID, p), req.FileIDs)
		if err != nil {
			if errors.Is(err, errInvalidFileID) {
				fail(c, CodeBadRequest, err.Error())
			} else {
				fail(c, CodeNotFound, err.Error())
			}
			return
		}
		files = f
	}
	id, err := s.svc.Engine().SubmitUserRef(p.ID, req.Provider, req.Tool, req.Params, files, &task.InputRef{
		FileIDs:        req.FileIDs,
		ArtifactInput:  req.ArtifactInput,
		ArtifactInputs: req.ArtifactInputs,
	})
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"task_id": id})
}

func (s *Server) listTasks(c *gin.Context) {
	p := principalFrom(c)
	uid := p.ID
	if p.IsAdmin() {
		uid = "" // admin 全量可见（含无主历史任务）
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	items, total, err := s.svc.DB().ListTasks(c.Query("provider"), nil, size, (page-1)*size, uid)
	if err != nil {
		failErr(c, err)
		return
	}
	dtos := make([]taskDTO, 0, len(items))
	for _, t := range items {
		dtos = append(dtos, toTaskDTO(t))
	}
	ok(c, gin.H{"items": dtos, "total": total})
}

func (s *Server) getTask(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	p := principalFrom(c)
	if !canAccessTask(t.UserID, p) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	arts, err := s.svc.DB().ListArtifacts(t.ID)
	if err != nil {
		failErr(c, err)
		return
	}
	adtos := make([]artifactDTO, 0, len(arts))
	for _, a := range arts {
		adtos = append(adtos, toArtifactDTO(a))
	}
	ok(c, gin.H{"task": toTaskDTO(*t), "artifacts": adtos})
}

func (s *Server) deleteTask(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if err := s.svc.DB().DeleteTask(c.Param("id")); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

func (s *Server) cancelTask(c *gin.Context) {
	t, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	if !canAccessTask(t.UserID, principalFrom(c)) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if err := s.svc.Engine().Cancel(c.Param("id")); err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}

// rerunTask 克隆原任务重新提交：params 原样回传（引擎重新校验），输入引用按
// Task.Input 重新解析——上传文件/产物可能已被清理，缺失时在提交前明确报错。
func (s *Server) rerunTask(c *gin.Context) {
	old, err := s.svc.DB().GetTask(c.Param("id"))
	if err == store.ErrNotFound {
		fail(c, CodeNotFound, "任务不存在")
		return
	} else if err != nil {
		failErr(c, err)
		return
	}
	p := principalFrom(c)
	if !canAccessTask(old.UserID, p) {
		fail(c, CodeNotFound, "任务不存在")
		return
	}
	if _, ok := s.svc.Registry().Get(old.Provider, old.Tool); !ok {
		fail(c, CodeBadRequest, fmt.Sprintf("工具 %s.%s 不可用（凭证未配置或已下线），无法重跑", old.Provider, old.Tool))
		return
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(old.Params), &params); err != nil {
		fail(c, CodeBadRequest, "原任务参数已损坏，无法重跑")
		return
	}
	if params == nil {
		params = map[string]any{}
	}
	delete(params, "_out")
	var ref task.InputRef
	if old.Input != "" {
		_ = json.Unmarshal([]byte(old.Input), &ref)
	}
	var files map[string]string
	switch {
	case ref.ArtifactInput != "" || len(ref.ArtifactInputs) > 0:
		// 单值归一为长度 1 的数组，与多产物共用解析链（p 为当前重跑者：本人或 admin，
		// 产物归属校验据此放行；与上方 file_ids 按原任务所有者目录解析的所有者语义各自独立）。
		ids := ref.ArtifactInputs
		if ref.ArtifactInput != "" {
			ids = []string{ref.ArtifactInput}
		}
		f, err := s.resolveArtifactInputs(ids, p)
		if err != nil {
			switch {
			case errors.Is(err, errArtifactNotFound):
				fail(c, CodeNotFound, "原输入产物已被删除，无法重跑")
			case errors.Is(err, errArtifactMissing):
				fail(c, CodeNotFound, "原输入产物文件缺失，无法重跑")
			default:
				failErr(c, err)
			}
			return
		}
		files = f
	case len(ref.FileIDs) > 0:
		// 按原任务所有者的目录解析（admin 重跑他人任务时输入文件属于原所有者）
		f, err := fileIDsToFiles(s.uploadSearchDirs(old.UserID, p), ref.FileIDs)
		if err != nil {
			fail(c, CodeNotFound, "原上传文件已不存在，无法重跑："+err.Error())
			return
		}
		files = f
	}
	id, err := s.svc.Engine().SubmitUserRef(old.UserID, old.Provider, old.Tool, params, files, &ref)
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"task_id": id})
}

// resolveArtifactInputs 产物 id 列表 → 本地绝对路径，create（artifact_input/artifact_inputs）
// 与 rerun 两条链共用：逐个 GetArtifact（不存在与越权同报 not found，不泄露他人产物存在性）、
// artifactAbsPath（IsAbs/jail 防御）。key 约定与 fileIDsToFiles 一致：第 1 个 "audio"，
// 第 2 个起 "audio2"、"audio3"…（单产物通道即长度 1 的特例，mix 等多输入工具自校验个数）。
// 校验失败返回 errArtifactNotFound/errArtifactMissing 哨兵由调用方翻译各自文案；
// 其余 DB 错误原样上抛（failErr）。
var (
	errArtifactNotFound = errors.New("产物不存在")
	errArtifactMissing  = errors.New("产物文件缺失")
)

func (s *Server) resolveArtifactInputs(ids []string, p *Principal) (map[string]string, error) {
	files := make(map[string]string, len(ids))
	for i, id := range ids {
		a, err := s.svc.DB().GetArtifact(id)
		if err == store.ErrNotFound {
			return nil, fmt.Errorf("%w: %s", errArtifactNotFound, id)
		} else if err != nil {
			return nil, err
		}
		if !canAccessArtifact(a.UserID, p) {
			return nil, fmt.Errorf("%w: %s", errArtifactNotFound, id)
		}
		abs, err := s.artifactAbsPath(a.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", errArtifactMissing, id)
		}
		key := "audio"
		if i > 0 {
			key = fmt.Sprintf("audio%d", i+1)
		}
		files[key] = abs
	}
	return files, nil
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

// 注意：stream/download 是二进制流端点，不套 JSON 包络，按真实 HTTP 语义返回。

// canAccessTask 无主（空 UserID）任务=部署前本地存量，仅 admin 可见；其余本人或 admin。
func canAccessTask(ownerUserID string, p *Principal) bool {
	if ownerUserID == "" {
		return p.IsAdmin()
	}
	return ownerUserID == p.ID || p.IsAdmin()
}

func canAccessArtifact(ownerUserID string, p *Principal) bool { return canAccessTask(ownerUserID, p) }

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

func (s *Server) getSettings(c *gin.Context) {
	cfg := s.svc.Config()
	st := cfg.Storage
	ok(c, gin.H{
		"providers": s.svc.ProviderStates(cfg),
		// secret_key 不回传（回传 has_secret_key 供设置页展示「已配置」）。
		"storage": gin.H{
			"provider":       st.Provider,
			"endpoint":       st.Endpoint,
			"region":         st.Region,
			"bucket":         st.Bucket,
			"access_key":     st.AccessKey,
			"has_secret_key": st.SecretKey != "",
			"prefix":         st.Prefix,
			"enabled":        s.svc.StorageClient() != nil,
			// 各存储类型独立配置段：设置页切换存储类型时按段换显已存值，互不覆盖。
			"channels": storageChannelsPayload(cfg.StorageChannels),
		},
		"data_dir": cfg.DataDir,
	})
}

// storageChannelsPayload 各存储类型的独立配置（secret 只回传 has_secret_key）。
func storageChannelsPayload(channels map[string]config.StorageConfig) gin.H {
	out := gin.H{}
	for _, name := range slices.Sorted(maps.Keys(channels)) {
		ch := channels[name]
		out[name] = gin.H{
			"endpoint":       ch.Endpoint,
			"region":         ch.Region,
			"bucket":         ch.Bucket,
			"access_key":     ch.AccessKey,
			"has_secret_key": ch.SecretKey != "",
			"prefix":         ch.Prefix,
		}
	}
	return out
}

type putProviderSettingsReq struct {
	Fields map[string]string `json:"fields"`
}

type putStorageReq struct {
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"` // 留空=不修改
	Prefix    string `json:"prefix"`
}

// putProviderSettings 按卡保存凭证：声明校验 + 落盘 + 热重注册在 SaveProviderFields 内一体完成。
func (s *Server) putProviderSettings(c *gin.Context) {
	var req putProviderSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.SaveProviderFields(c.Param("name"), req.Fields); err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"ok": true, "note": "凭证已保存并即时生效"})
}

// putStorageSettings 对象存储独立保存端点（body 与旧 PUT /api/settings 的 storage 分支一致）。
func (s *Server) putStorageSettings(c *gin.Context) {
	var req putStorageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.SaveStorage(config.StorageConfig{
		Provider:  req.Provider,
		Endpoint:  req.Endpoint,
		Region:    req.Region,
		Bucket:    req.Bucket,
		AccessKey: req.AccessKey,
		SecretKey: req.SecretKey,
		Prefix:    req.Prefix,
	}); err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"ok": true, "note": "存储配置已保存"})
}

// providerTest 卡探活结果（test-connection 的 results 数组元素）。
type providerTest struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) testConnection(c *gin.Context) {
	// 按卡动态探测：volcengine 极短合成、mediakit 鉴权探测、mvsep token+免费额度、
	// qianwen/xiaomi 极短合成；storage 桶探活（HeadBucket 不计费）。
	tests := []struct {
		name string
		fn   func() (string, bool)
	}{
		{"volcengine", s.svc.TestSpeechConnection},
		{"mediakit", s.svc.TestMediaKitConnection},
		{"mvsep", s.svc.TestMVSepConnection},
		{"qianwen", s.svc.TestQianwenConnection},
		{"xiaomi", s.svc.TestXiaomiConnection},
	}
	results := make([]providerTest, 0, len(tests))
	for _, tt := range tests {
		msg, okv := tt.fn()
		results = append(results, providerTest{Name: tt.name, OK: okv, Message: msg})
	}
	stMsg, stOK := s.svc.TestStorageConnection()
	ok(c, gin.H{
		"results": results,
		"storage": gin.H{"ok": stOK, "message": stMsg},
	})
}

// ---- MVSep（mvsep.com 音频源分离）：算法/账户/历史查询路由 ----
// 任务提交复用 /api/tasks（provider=mvsep, tool=separate），不设独立提交端点。

// listMVSepAlgorithms 算法列表：进程内缓存 1 小时（上游限频 60/分钟），
// ?refresh=1 绕过缓存强拉。
func (s *Server) listMVSepAlgorithms(c *gin.Context) {
	algos, err := s.svc.MVSepAlgorithms(c.Request.Context(), c.Query("refresh") == "1")
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, algos)
}

// mvsepStatus 账户信息 + 站点队列/每日免费额度（分离页头部展示）。
func (s *Server) mvsepStatus(c *gin.Context) {
	u, qs, err := s.svc.MVSepUserQueue(c.Request.Context())
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"user": u, "queue": qs})
}

// mvsepHistory MVSep 云端分离历史（?start=&limit=，默认 0/10，上游上限 20）。
func (s *Server) mvsepHistory(c *gin.Context) {
	start, _ := strconv.Atoi(c.DefaultQuery("start", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	items, err := s.svc.MVSepHistory(c.Request.Context(), start, limit)
	if err != nil {
		failErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

// mvsepSeparationGet 按 hash 直查云端任务状态/结果（免鉴权上游接口的只读代理）：
// 供分离页从云端历史补拉结果（本服务任务超时/清理后仍可取回产物直链）。
func (s *Server) mvsepSeparationGet(c *gin.Context) {
	hash := strings.TrimSpace(c.Query("hash"))
	if hash == "" {
		fail(c, CodeBadRequest, "参数错误：hash 必填")
		return
	}
	cfg := s.svc.Config()
	status, res, err := mvsep.New(cfg.MVSep.APIToken, cfg.MVSep.BaseURL).Get(c.Request.Context(), hash)
	if err != nil {
		// failed/not_found 属任务终态而非传输错误：以状态+错误返回 200，前端按终态展示
		ok(c, gin.H{"status": status, "error": err.Error()})
		return
	}
	ok(c, gin.H{"status": status, "result": res})
}

// listVoices 音色列表：?provider=qianwen 返回千问非实时音色（含官方试听 URL 与模型支持矩阵），
// 缺省为火山引擎音色（场景/语种/方言筛选字段）。
func (s *Server) listVoices(c *gin.Context) {
	if c.Query("provider") == "qianwen" {
		ok(c, gin.H{"voices": qianwen.Voices()})
		return
	}
	ok(c, gin.H{"voices": volcengine.Voices()})
}

// ---- 字幕工坊（本地能力：纯 Go 解析/分句/导出，零上游 API 成本）----

// listSubtitlePresets 字幕样式预设：单一事实来源（internal/subtitle.Presets），
// 前端预览与导出参数都以此为准，避免两处颜色定义漂移。
func (s *Server) listSubtitlePresets(c *gin.Context) {
	ok(c, subtitle.Presets)
}

// searchTasks 转写全文搜索：LIKE 粗筛候选任务，解析 summary 后按分句/总结文本
// 精确命中提取片段（含时间戳，前端可跳到对应同步回放位置）。
func (s *Server) searchTasks(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		fail(c, CodeBadRequest, "参数错误：q 必填")
		return
	}
	tasks, err := s.svc.DB().SearchSucceededSummaries(q, 50, searchScopeUserID(principalFrom(c)))
	if err != nil {
		failErr(c, err)
		return
	}
	lower := strings.ToLower(q)
	type matchSeg struct {
		Text    string `json:"text"`
		StartMS int64  `json:"start_ms"`
		EndMS   int64  `json:"end_ms"`
	}
	type summaryJSON struct {
		Segments     []matchSeg `json:"segments"`
		SummaryText  string     `json:"summary_text"`
		MinutesTitle string     `json:"minutes_title"`
	}
	items := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		// 标题命中（如分离任务的「歌名 - 歌手」）排首位：无时间戳的整行匹配
		var matches []matchSeg
		if strings.Contains(strings.ToLower(t.Title), lower) {
			matches = append(matches, matchSeg{Text: t.Title})
		}
		var sv summaryJSON
		if json.Unmarshal([]byte(t.Summary), &sv) == nil {
			for _, seg := range sv.Segments {
				if strings.Contains(strings.ToLower(seg.Text), lower) {
					matches = append(matches, seg)
					if len(matches) == 5 {
						break
					}
				}
			}
			if len(matches) == 0 {
				// LIKE 粗筛命中但分句未命中：查总结文本/标题（如妙记 summary_text），
				// 都不中则本次为键名误命中，丢弃
				hay := sv.SummaryText
				if hay == "" {
					hay = sv.MinutesTitle
				}
				if bi := strings.Index(strings.ToLower(hay), lower); bi >= 0 {
					// 按 rune 提取上下文窗口（本项目语种下 ToLower 不改变 rune 数）
					runes := []rune(hay)
					rOff := len([]rune(strings.ToLower(hay)[:bi]))
					start, end := rOff-30, rOff+len([]rune(q))+60
					if start < 0 {
						start = 0
					}
					if end > len(runes) {
						end = len(runes)
					}
					matches = append(matches, matchSeg{Text: string(runes[start:end])})
				}
			}
		}
		if len(matches) == 0 {
			continue
		}
		items = append(items, gin.H{
			"task_id":    t.ID,
			"tool":       t.Tool,
			"title":      t.Title,
			"created_at": t.CreatedAt.Format("2006-01-02 15:04:05"),
			"matches":    matches,
		})
	}
	ok(c, gin.H{"items": items, "total": len(items)})
}

type prepareSubtitlesReq struct {
	Mode       string `json:"mode"` // srt：解析 SRT；text：文稿分句草稿
	Content    string `json:"content"`
	DurationMS int64  `json:"duration_ms"` // text 模式的草稿总时长，按字数比例分配
}

func (s *Server) prepareSubtitles(c *gin.Context) {
	var req prepareSubtitlesReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		fail(c, CodeBadRequest, "参数错误：content 必填")
		return
	}
	var segs []subtitle.Segment
	switch req.Mode {
	case "srt":
		var err error
		if segs, err = subtitle.ParseSRT(req.Content); err != nil {
			fail(c, CodeBadRequest, err.Error())
			return
		}
	case "text":
		segs = subtitle.DraftSegments(req.Content, req.DurationMS)
	default:
		fail(c, CodeBadRequest, "mode 仅支持 srt | text")
		return
	}
	if len(segs) == 0 {
		fail(c, CodeBadRequest, "没有解析到可用内容")
		return
	}
	ok(c, gin.H{"segments": segs})
}

type exportSubtitlesReq struct {
	Segments []subtitle.Segment `json:"segments"`
	Format   string             `json:"format"` // srt | ass
	Style    struct {
		Name     string `json:"name"`
		FontSize int    `json:"font_size"`
		MarginV  int    `json:"margin_v"`
		Karaoke  *bool  `json:"karaoke"`
	} `json:"style"`
}

func (s *Server) exportSubtitles(c *gin.Context) {
	var req exportSubtitlesReq
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Segments) == 0 {
		fail(c, CodeBadRequest, "参数错误：segments 不能为空")
		return
	}
	var data []byte
	ct := "text/plain; charset=utf-8"
	filename := "subtitles"
	switch strings.ToLower(req.Format) {
	case "srt":
		data = subtitle.BuildSRT(req.Segments)
		filename += ".srt"
	case "ass":
		style := subtitle.PresetByName(req.Style.Name)
		if req.Style.FontSize > 0 {
			style.FontSize = req.Style.FontSize
		}
		if req.Style.MarginV > 0 {
			style.MarginV = req.Style.MarginV
		}
		if req.Style.Karaoke != nil {
			style.Karaoke = *req.Style.Karaoke
		}
		data = subtitle.BuildASS(req.Segments, style)
		ct = "text/x-ssa; charset=utf-8"
		filename += ".ass"
	default:
		fail(c, CodeBadRequest, "format 仅支持 srt | ass")
		return
	}
	if len(data) == 0 {
		fail(c, CodeBadRequest, "没有可导出的字幕内容")
		return
	}
	// 二进制流端点惯例：不套 JSON 包络，真实文件语义
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, ct, data)
}

// listDicts 命名词典清单（config.yaml dicts 段）：供工具页「从词典填入」。
// 热词/术语非敏感，原样返回；管理走 CLI（voxbox dict add/rm）。
func (s *Server) listDicts(c *gin.Context) {
	dicts, err := config.Dicts()
	if err != nil {
		failErr(c, err)
		return
	}
	out := make([]gin.H, 0, len(dicts))
	for _, d := range dicts {
		out = append(out, gin.H{"name": d.Name, "hotwords": d.Hotwords, "terms": d.Terms})
	}
	ok(c, out)
}
