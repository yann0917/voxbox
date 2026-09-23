package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSeparateCommand() *cobra.Command {
	var (
		scene   string
		format  string
		outDir  string
		jsonOut bool
		file    string
		engine  string
		sepType string
		addOpt1 string
		addOpt2 string
		addOpt3 string
		stems   string
	)
	cmd := &cobra.Command{
		Use:   "separate <url>",
		Short: "音频分离：公网 URL 或本地文件分离多轨音频（火山 MediaKit / MVSep / 格式工厂 / 转换猫四引擎）",
		Long: `从公网可访问的 URL（或 --file 本地文件）分离多轨音频。
引擎四选一（--engine）：
  mediakit（默认）  火山 AI MediaKit 人声/背景音分离：--scene audio|music|drama|narrate，--format aac|mp3|wav|m4a|flac；本地文件需对象存储中转
  mvsep            MVSep 音乐源分离（120+ 算法，免费账号每天 50 次）：--sep-type 必填，--format 0|1|2|3|4|5（0=MP3 5=FLAC）；本地文件直传无需对象存储
  gsgc             格式工厂在线版（免费、无需凭证）：--stems both|vocals|instrumental，产物自动转码标准 MP3 128k；本地文件直传无需对象存储
  zhuanhuanmao     转换猫线路（与格式工厂同后端的镜像站点，免费、无需凭证）：参数同 gsgc`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var url string
			if len(args) == 1 {
				url = args[0]
			}
			if file != "" && url != "" {
				return fmt.Errorf("--file 与 URL 参数只能提供其一")
			}
			var files map[string]string
			if file != "" {
				files = map[string]string{"audio": file}
			}
			switch engine {
			case "mediakit":
				params := map[string]any{"url": url, "scene": scene, "output_format": format}
				return runToolSync(c, "volcengine", "separate", params, files, outDir, jsonOut)
			case "mvsep":
				if sepType == "" {
					return fmt.Errorf("--engine mvsep 需要 --sep-type（算法 render_id，见 GET /api/mvsep/algorithms 或 Web 分离页）")
				}
				// mvsep 的 --format 语义是 0-5 枚举：用户未显式给 --format 时默认 0（MP3）
				if !c.Flags().Changed("format") {
					format = "0"
				}
				params := map[string]any{
					"url": url, "sep_type": sepType, "output_format": format,
					"add_opt1": addOpt1, "add_opt2": addOpt2, "add_opt3": addOpt3,
				}
				return runToolSync(c, "mvsep", "separate", params, files, outDir, jsonOut)
			case "gsgc", "cloudsep", "pcgeshi", "geshi":
				params := map[string]any{"url": url, "stems": stems}
				return runToolSync(c, "gsgc", "separate", params, files, outDir, jsonOut)
			case "zhuanhuanmao", "convertmao":
				params := map[string]any{"url": url, "stems": stems}
				return runToolSync(c, "zhuanhuanmao", "separate", params, files, outDir, jsonOut)
			default:
				return fmt.Errorf("暂不支持该分离引擎 %s（mediakit|mvsep|gsgc|zhuanhuanmao）", engine)
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&engine, "engine", "mediakit", "分离引擎: mediakit|mvsep|gsgc|zhuanhuanmao")
	f.StringVar(&scene, "scene", "audio", "分离场景（mediakit）: audio|music|drama|narrate")
	f.StringVar(&format, "format", "mp3", "输出格式（mediakit）: aac|mp3|wav|m4a|flac；（mvsep）: 0=MP3|1/2/3/4=WAV 各位深|5=FLAC；（gsgc/zhuanhuanmao）固定 MP3 128k 不适用")
	f.StringVar(&sepType, "sep-type", "", "MVSep 分离类型 render_id（mvsep 必填）")
	f.StringVar(&addOpt1, "add-opt1", "", "MVSep 附加选项 1（所选算法 algorithm_fields 定义）")
	f.StringVar(&addOpt2, "add-opt2", "", "MVSep 附加选项 2")
	f.StringVar(&addOpt3, "add-opt3", "", "MVSep 附加选项 3")
	f.StringVar(&stems, "stems", "both", "提取轨道（gsgc/zhuanhuanmao）: both 双轨|vocals 只提取人声|instrumental 只提取伴奏")
	f.StringVar(&outDir, "out-dir", "", "音轨输出目录（默认数据目录自动命名）")
	f.BoolVar(&jsonOut, "json", false, "stdout 输出机器可读 JSON")
	f.StringVar(&file, "file", "", "本地音频文件路径（mediakit 需配置对象存储中转；mvsep/gsgc/zhuanhuanmao 服务端直传，与 URL 参数互斥）")
	return cmd
}
