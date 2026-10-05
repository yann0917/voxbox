package server

// 只读清单端点：工具面、各通道音色、命名词典——工具页表单与选择器的数据源。

import (
	"github.com/gin-gonic/gin"
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider/local"
	"github.com/yann0917/voxbox/internal/provider/minimax"
	"github.com/yann0917/voxbox/internal/provider/openrouter"
	"github.com/yann0917/voxbox/internal/provider/qianwen"
	"github.com/yann0917/voxbox/internal/provider/volcengine"
	"github.com/yann0917/voxbox/internal/provider/zhipu"
)

func (s *Server) listTools(c *gin.Context) {
	out := []toolDTO{}
	for _, t := range s.svc.Registry().List() {
		tool, _ := s.svc.Registry().Get(t.Provider, t.Name)
		out = append(out, toolDTO{Meta: t, ParamSpecs: tool.ParamSpecs()})
	}
	ok(c, out)
}

// listVoices 音色列表：?provider=qianwen 返回千问非实时音色（含官方试听 URL 与模型支持矩阵），
// ?provider=zhipu 运行时拉取智谱音色（官方 + 复刻，含试听 URL；未配置凭证回落官方静态表），
// 缺省为火山引擎音色（场景/语种/方言筛选字段）。
func (s *Server) listVoices(c *gin.Context) {
	switch c.Query("provider") {
	case "qianwen":
		ok(c, gin.H{"voices": qianwen.Voices()})
		return
	case "zhipu":
		if key := s.svc.Config().Zhipu.APIKey; key != "" {
			voices, err := zhipu.NewVoiceClient(key, zhipu.BaseURL).List(c.Request.Context(), "", "")
			if err == nil && len(voices) > 0 {
				ok(c, gin.H{"voices": voices})
				return
			}
			// 配置了 key 但拉取失败：报错暴露凭证/网络问题，避免静默降级掩盖配置错误
			failErr(c, err)
			return
		}
		ok(c, gin.H{"voices": zhipu.OfficialVoices})
		return
	case "local":
		// family=kokoro 返回内置音色库(103 个,含 voices.bin sid);缺省 qwen3 九人
		if c.Query("family") == "kokoro" {
			ok(c, gin.H{"voices": local.KokoroVoices()})
			return
		}
		ok(c, gin.H{"voices": local.CustomVoices()})
		return
	case "openrouter":
		// OpenRouter 无音色列表端点，静态枚举（编译期常量表，与工具 ParamSpecs 同源）
		ok(c, gin.H{"voices": openrouter.Voices()})
		return
	case "minimax":
		// MiniMax：有 key 拉运行时接口（系统+复刻+文生音色），无 key 回落静态系统音色表
		if key := s.svc.Config().Minimax.APIKey; key != "" {
			voices, err := minimax.NewVoiceClient(key, minimax.BaseURL).List(c.Request.Context())
			if err == nil && len(voices) > 0 {
				ok(c, gin.H{"voices": voices})
				return
			}
			// 配置了 key 但拉取失败：报错暴露凭证/网络问题，避免静默降级掩盖配置错误
			failErr(c, err)
			return
		}
		ok(c, gin.H{"voices": minimax.Voices()})
		return
	}
	ok(c, gin.H{"voices": volcengine.Voices()})
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
