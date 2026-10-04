package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler 组装全部 HTTP 路由，只做注册不写业务逻辑；handler 按领域各归其文件：
// auth.go（登录/会话/token）、uploads.go、tasks.go（任务 CRUD/重跑）、speakers.go
// （说话人改名）、artifacts.go
// （产物流/下载与路径、越权防御）、settings.go（凭证/存储/连通性）、pronunciation.go、
// subtitles.go、search.go（转写全文搜索）、lookups.go（工具/音色/词典清单）、
// mvsep.go、minutes_export.go、local.go、models.go、voicelib.go、assistant.go、
// prompts.go（提示词库+AI 写作）、refine.go（录音笔记加工）、mediaenv.go（gsgcHealth）、
// hub.go（wsProgress）。
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
		api.GET("/search", s.searchTasks)
		api.GET("/voices", s.listVoices)
		api.GET("/dicts", s.listDicts)
		// 本地推理就绪查询:一次判定引擎+模型依赖链(「本地」页签引导卡数据源)
		api.GET("/local/ready", s.localReady)
		// WS 进度通道：登录后浏览器带 Cookie 升级；快照按用户过滤（admin 收全量）。
		api.GET("/ws", s.wsProgress)
		// 实时字幕双向 WS：控制消息=文本 JSON 帧、音频=二进制 PCM 帧（协议见 live.go 头注）。
		api.GET("/ws/live", s.wsLive)
	}

	uploads := api.Group("/uploads")
	{
		uploads.POST("", s.uploadFile)
		uploads.GET("/:id/stream", s.streamUpload)
	}

	tasks := api.Group("/tasks")
	{
		tasks.POST("", s.createTask)
		tasks.GET("", s.listTasks)
		tasks.GET("/:id", s.getTask)
		tasks.DELETE("/:id", s.deleteTask)
		tasks.POST("/:id/cancel", s.cancelTask)
		tasks.POST("/:id/rerun", s.rerunTask)
		// 说话人改名（录音笔记本地覆盖层）：写 Summary.speaker_names，详见 speakers.go
		tasks.PATCH("/:id/speakers", s.renameTaskSpeakers)
		// 标题/标签编辑：标题手改置 title_edited，详见 task_meta.go
		tasks.PATCH("/:id/meta", s.editTaskMeta)
	}

	artifacts := api.Group("/artifacts")
	{
		artifacts.GET("/:id/stream", s.streamArtifact)
		artifacts.GET("/:id/download", s.downloadArtifact)
	}

	// 设置读写分离：GET 返回的卡态不含 secret 明文（登录用户可见），写入/连通性测试仅 admin。
	settings := api.Group("/settings")
	{
		settings.GET("", s.getSettings)
	}
	settingsAdmin := api.Group("/settings", s.requireAdmin())
	{
		settingsAdmin.PUT("/providers/:name", s.putProviderSettings)
		settingsAdmin.PUT("/storage", s.putStorageSettings)
		settingsAdmin.PUT("/assistant", s.putAssistantSettings)
		settingsAdmin.PUT("/data-dir", s.putDataDirSettings)
		settingsAdmin.POST("/test-connection", s.testConnection)
	}

	// 发音词典（TTS 合成前读音替换）：读/试听登录即可，增删改沿设置页口径仅 admin
	pron := api.Group("/pronunciation")
	{
		pron.GET("", s.listPronunciation)
		pron.POST("/test", s.testPronunciation)
	}
	pronAdmin := api.Group("/pronunciation", s.requireAdmin())
	{
		pronAdmin.POST("", s.addPronunciation)
		pronAdmin.PUT("/:id", s.updatePronunciation)
		pronAdmin.DELETE("/:id", s.deletePronunciation)
	}

	// 字幕工坊（本地能力：纯 Go 解析/分句/导出，零上游 API 成本；翻译走三源引擎）
	subtitles := api.Group("/subtitles")
	{
		subtitles.POST("/prepare", s.prepareSubtitles)
		subtitles.POST("/export", s.exportSubtitles)
		subtitles.GET("/presets", s.listSubtitlePresets)
		subtitles.POST("/translate", s.translateSubtitles)
		subtitles.GET("/langs", s.listSubtitleLangs)
		// 本地小件：时间轴校准 / 中文去标点（详见 subtitles.go subtitleTools）
		subtitles.POST("/tools", s.subtitleTools)
	}

	minutes := api.Group("/minutes")
	{
		minutes.GET("/templates", s.listMinutesTemplates)
		minutes.GET("/:id/export", s.exportMinutes)
	}

	// MVSep（mvsep.com 音频源分离）：任务提交复用 /api/tasks（provider=mvsep,
	// tool=separate），这里只挂算法/账户/历史查询路由。
	mvsepRoutes := api.Group("/mvsep")
	{
		mvsepRoutes.GET("/algorithms", s.listMVSepAlgorithms)
		mvsepRoutes.GET("/status", s.mvsepStatus)
		mvsepRoutes.GET("/history", s.mvsepHistory)
		mvsepRoutes.GET("/separation", s.mvsepSeparationGet)
	}

	// 本地语音模型管理(无凭证,全部登录用户可读可操作)
	models := api.Group("/models")
	{
		models.GET("", s.listModels)
		models.POST("/open-dir", s.openModelsDir)
		models.POST("/:id/download", s.startModelDownload)
		models.POST("/:id/stop", s.stopModelDownload)
		models.DELETE("/:id", s.deleteModel)
	}

	// 音色库(参考音频:入库/列表/预览/改名/删除,磁盘即真相不落 DB)
	voiceLib := api.Group("/voice-library")
	{
		voiceLib.GET("", s.listVoiceLib)
		voiceLib.POST("", s.addVoiceLib)
		voiceLib.GET("/:id/stream", s.streamVoiceLib)
		voiceLib.PATCH("/:id", s.patchVoiceLib)
		voiceLib.DELETE("/:id", s.deleteVoiceLib)
	}

	// AI 助手（悬浮面板）：模型目录 + 流式对话（SSE）
	assistantRoutes := api.Group("/assistant")
	{
		assistantRoutes.GET("/models", s.assistantModels)
		assistantRoutes.POST("/chat", s.assistantChat)
	}

	// 录音笔记加工层：asr/minutes 成功任务的转写二次加工（纪要/待办/日程/自定义），
	// SSE 同助手协议，结果合并落任务 Summary.refined（详见 refine.go）
	api.POST("/refine", s.refine)

	// 提示词库（内置+用户自定义）：条目按登录用户隔离，AI 写作流式同助手协议
	promptRoutes := api.Group("/prompts")
	{
		promptRoutes.GET("", s.listPrompts)
		promptRoutes.POST("", s.createPrompt)
		promptRoutes.PUT("/:id", s.updatePrompt)
		promptRoutes.DELETE("/:id", s.deletePrompt)
		promptRoutes.POST("/apply", s.applyPrompt)
	}

	// 翻译术语表：AI 自动沉淀 + 设置页人工增删改查共用一表
	glossaryRoutes := api.Group("/glossary")
	{
		glossaryRoutes.GET("", s.listGlossary)
		glossaryRoutes.POST("", s.createGlossaryTerm)
		glossaryRoutes.PUT("/:id", s.updateGlossaryTerm)
		glossaryRoutes.DELETE("/:id", s.deleteGlossaryTerm)
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
