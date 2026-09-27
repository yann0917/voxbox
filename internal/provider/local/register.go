// RegisterAll 本地推理工具注册:与 audiotool 同为无凭证本地能力,注册一次不参与热更新。
package local

import (
	"errors"

	"github.com/yann0917/voxbox/internal/localmodel"
	"github.com/yann0917/voxbox/internal/localruntime"
	"github.com/yann0917/voxbox/internal/provider"
	"github.com/yann0917/voxbox/internal/voicelib"
)

// AllTools 返回工具实例(seam 参数供测试注入假实现;voices 音色库可 nil,仅 voice_id 克隆消费)。
func AllTools(dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime, voices *voicelib.Library) []provider.Tool {
	return []provider.Tool{
		newTTSTool(dataDir, models, tts, voices),
		newASRTool(dataDir, models),
	}
}

// RegisterAll 启动路径注册。
func RegisterAll(reg *provider.Registry, dataDir string, models *localmodel.Manager, tts *localruntime.TTSRuntime, voices *voicelib.Library) error {
	var errs []error
	for _, t := range AllTools(dataDir, models, tts, voices) {
		if err := reg.Register(t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
