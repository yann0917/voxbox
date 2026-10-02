package server

// 设置端点：凭证卡读写、对象存储配置、全量连通性测试。
// 读写分离由路由层保证：GET 登录即可（卡态不含 secret 明文），写入/测试仅 admin。

import (
	"maps"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/config"
)

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
		// AI 默认大模型（悬浮助手与生成/润色共用），空=自动回落。
		"assistant": gin.H{"default_model": cfg.Assistant.DefaultModel},
		"data_dir":  cfg.DataDir,
	})
}

type putAssistantReq struct {
	DefaultModel string `json:"default_model"`
}

// putAssistantSettings 保存「AI 默认大模型」（空串=恢复自动回落）。
func (s *Server) putAssistantSettings(c *gin.Context) {
	var req putAssistantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	if err := s.svc.SaveAssistantDefault(req.DefaultModel); err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"ok": true, "note": "默认大模型已保存并即时生效"})
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

type putDataDirReq struct {
	Dir string `json:"dir"`
}

// putDataDirSettings 保存数据保存位置（config.yaml data_dir），重启生效：监听与
// SQLite 连接是启动期属性，运行期不换，响应带 restart_required=true 供前端提示。
func (s *Server) putDataDirSettings(c *gin.Context) {
	var req putDataDirReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, CodeBadRequest, "参数错误")
		return
	}
	dir, err := s.svc.SaveDataDir(req.Dir)
	if err != nil {
		fail(c, CodeBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"dir": dir, "restart_required": true})
}

// providerTest 卡探活结果（test-connection 的 results 数组元素）。
type providerTest struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) testConnection(c *gin.Context) {
	// 按卡动态探测：volcengine 极短合成、mediakit 鉴权探测、mvsep token+免费额度、
	// qianwen/xiaomi/zhipu 极短合成；storage 桶探活（HeadBucket 不计费）。
	tests := []struct {
		name string
		fn   func() (string, bool)
	}{
		{"volcengine", s.svc.TestSpeechConnection},
		{"mediakit", s.svc.TestMediaKitConnection},
		{"mvsep", s.svc.TestMVSepConnection},
		{"qianwen", s.svc.TestQianwenConnection},
		{"xiaomi", s.svc.TestXiaomiConnection},
		{"zhipu", s.svc.TestZhipuConnection},
		{"openrouter", s.svc.TestOpenRouterConnection},
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
