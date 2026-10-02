package server

import "github.com/gin-gonic/gin"

// 本地推理就绪查询:一次判定引擎+模型依赖链,前端「本地」页签引导卡的数据源。

type missingItem struct {
	Type string `json:"type"` // engine | model
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) localReady(c *gin.Context) {
	m := s.svc.LocalModels()
	missing := []missingItem{} // ready=true 时序列化为 [] 而非 null,前端省判空
	ensure := func(kind, id string) {
		e, ok := m.GetEntry(id)
		if !ok {
			return // 目录无此条目(理论上不发生):不进 missing
		}
		if !m.Installed(id) {
			missing = append(missing, missingItem{Type: kind, ID: id, Name: e.Name})
		}
	}
	var ready bool
	switch c.Query("tool") {
	case "tts":
		// 依赖链按目录条目推导(kokoro→sherpa-onnx,qwen3/index→audiocpp),不再硬编码
		// 单一引擎。可用性 = 任一「引擎+模型」成对齐备;missing 是引导清单(缺的引擎
		// 全列出,一个模型都没装时再列全部模型),与 ready 相互独立——如 linux/arm64
		// 无 audiocpp 资产,missing 恒含它,但 kokoro+sherpa 装齐即 ready。
		seenEngines := map[string]bool{}
		anyModelInstalled := false
		for _, v := range m.List() {
			if v.Entry.Kind != "tts" {
				continue
			}
			if !seenEngines[v.Entry.RequiresEngine] {
				seenEngines[v.Entry.RequiresEngine] = true
				ensure("engine", v.Entry.RequiresEngine)
			}
			if m.Installed(v.Entry.ID) {
				anyModelInstalled = true
				if m.Installed(v.Entry.RequiresEngine) {
					ready = true
				}
			}
		}
		if !anyModelInstalled {
			for _, v := range m.List() {
				if v.Kind == "tts" {
					missing = append(missing, missingItem{Type: "model", ID: v.Entry.ID, Name: v.Name})
				}
			}
		}
	case "asr":
		// 依赖链按目录条目推导(与 tts 同机制,不再硬编码单一引擎):任一「引擎+模型」
		// 成对齐备即可用。sensevoice→sherpa-onnx 与 r2t2→audiocpp 并列,具体提交哪个
		// 模型由工具 Run 的 params.model 路由(缺省 sensevoice 向后兼容)。
		seenEngines := map[string]bool{}
		anyModelInstalled := false
		for _, v := range m.List() {
			if v.Entry.Kind != "asr" {
				continue
			}
			if !seenEngines[v.Entry.RequiresEngine] {
				seenEngines[v.Entry.RequiresEngine] = true
				ensure("engine", v.Entry.RequiresEngine)
			}
			if m.Installed(v.Entry.ID) {
				anyModelInstalled = true
				if m.Installed(v.Entry.RequiresEngine) {
					ready = true
				}
			}
		}
		if !anyModelInstalled {
			for _, v := range m.List() {
				if v.Kind == "asr" {
					missing = append(missing, missingItem{Type: "model", ID: v.Entry.ID, Name: v.Name})
				}
			}
		}
	default:
		fail(c, CodeBadRequest, "参数错误:tool 仅支持 tts|asr")
		return
	}
	ok(c, gin.H{"ready": ready, "missing": missing})
}
