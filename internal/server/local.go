package server

import "github.com/gin-gonic/gin"

// 本地推理就绪查询:一次判定引擎+模型依赖链,前端「本地」页签引导卡的数据源。

type missingItem struct {
	Type string `json:"type"` // engine | model
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) localReady(c *gin.Context) {
	var engineID, modelID string
	switch c.Query("tool") {
	case "tts":
		engineID = "audiocpp"
	case "asr":
		engineID = "sherpa-onnx"
		modelID = "sensevoice-int8"
	default:
		fail(c, CodeBadRequest, "参数错误:tool 仅支持 tts|asr")
		return
	}
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
	ensure("engine", engineID)
	if modelID != "" {
		ensure("model", modelID)
	}
	if c.Query("tool") == "tts" {
		// tts:任一已安装的 qwen3 模型即可;一个都没装才把 TTS 模型们列进 missing
		anyInstalled := false
		for _, v := range m.List() {
			if v.Kind == "tts" && m.Installed(v.Entry.ID) {
				anyInstalled = true
				break
			}
		}
		if !anyInstalled {
			for _, v := range m.List() {
				if v.Kind == "tts" {
					missing = append(missing, missingItem{Type: "model", ID: v.Entry.ID, Name: v.Name})
				}
			}
		}
	}
	ok(c, gin.H{"ready": len(missing) == 0, "missing": missing})
}
