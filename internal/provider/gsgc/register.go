package gsgc

import (
	"github.com/yann0917/voxbox/internal/config"
	"github.com/yann0917/voxbox/internal/provider"
)

// RegisterAll 将格式工厂工具集注册进 registry（启动路径）。11 个功能同表同工具、
// 全部走同一套站点协议（fetch_upload_url → 直传站点 TOS → create_task →
// batchGet → fetch_download_url）：人声分离与视频/音频/图片转换压缩均为
// siteFuncs 表的一项，无特殊化。匿名可用、无需凭证。
//
// 另注册转换猫线路（zhuanhuanmao，同后端镜像站点）：只开人声分离——
// 供分离页「转换猫」Tab 与 CLI/MCP engine=zhuanhuanmao 使用；分离参数形态
// 两站不同（stem 字符串 vs 数组+model），见 zhmSeparateFunc。
func RegisterAll(reg *provider.Registry, cfg config.Config, dataDir string) error {
	for _, fn := range siteFuncs {
		if err := reg.Register(newSiteTool(lineGSGC, fn, dataDir)); err != nil {
			return err
		}
	}
	return reg.Register(newSiteTool(lineZHM, zhmSeparateFunc, dataDir))
}
