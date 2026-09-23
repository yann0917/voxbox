# 格式工厂与本地音视频处理

格式工厂能力 = z.pcgeshi.com 云端直连（站点私有协议，匿名免费、零本地依赖）：

| 入口 | 路由 | Provider | 执行位置 | 依赖 |
|---|---|---|---|---|
| 格式工厂 | `/gsgc` | `gsgc` | z.pcgeshi.com 云端 | 无 |
| 人声分离页 · 格式工厂 Tab | `/separate` | `gsgc.separate` | z.pcgeshi.com 云端（同协议同工具） | 无 |
| 人声分离页 · 转换猫 Tab | `/separate` | `zhuanhuanmao.separate` | www.zhuanhuanmao.com 云端（仅分离） | 无 |

## 转换猫镜像线路（provider=zhuanhuanmao）

转换猫（www.zhuanhuanmao.com）与格式工厂在线版是**同一后端的两块牌子**（同 TOS 桶
`cloud-converter`、同一把 AK、同一套任务接口；上传前缀分别为 `convertmao-web`/`gsgc-guanwang-web`）。
2026-09-19 起作为独立 provider `zhuanhuanmao` 注册，**只开人声分离**——供分离页第二 Tab
（排在格式工厂之后）与 CLI/MCP `engine=zhuanhuanmao` 使用，两线路互为免费备用。

关键差异（2026-09-19 自转换猫前端 chunk 逆向 + `TestZhuanhuanmaoSeparatePayload` 回归守护）：
`create_task` 的分离参数两站形态不同——格式工厂 `stem` 为数组 `["instrumental","vocals"]`
且必带 `model:"103"`；转换猫 `stem` 为字符串 `"instrumental_vocals"/"vocals"/"instrumental"`
且**不带 model**。各线路按自家前端形态提交（`site_tools.go` 的 `separateStemsTransform` /
`zhmStemsTransform`），不可混用。其余协议（上传/轮询/产物、MP3 128k 转码后处理）完全一致。

## 格式工厂（站点云端直连，provider=gsgc）

工具族走**同一套站点协议**（`internal/provider/gsgc/client.go`）：

```
GET  /api/fetch_upload_url?fileName=   → 预签名直传地址（格式工厂自己的 TOS 桶 gsgc-guanwang-web）
PUT  <upload_url>                      → 文件直传 TOS
POST /api/create_task                  → {task_type, input_path_id:[…], …按功能参数}
POST /api/tasks/batchGet               → {status: running→completed, progress, queue_tasks}
GET  /api/fetch_download_url?task_id=  → 产物直链（预签名 ≈1h）
```

11 个功能同表同工具（`site_tools.go` 的 siteFuncs，无特殊化）；人声分离仅多两个参数级约定：
`stems` 枚举（both/vocals/instrumental）转换为官方顺序的 stem 数组（实测乱序或缺 model 时上游
静默退化成只出人声单轨）、`model` 缺省 103。task_type 映射（2026-09-18 逆向自站点前端）：

| 工具注册名 | 站点 task_type | 参数键 |
|---|---|---|
| `gsgc.video-format-convert` | `video_converter` | output_format / video_codec / video_resolution / video_bitrate / video_framerate / audio_bitrate |
| `gsgc.video-compression` | `video_compress` | compress_rate(1-99) / video_compress_mode |
| `gsgc.video-extract-audio` | `audio_converter` | output_format / audio_bitrate / audio_samplerate / audio_channel |
| `gsgc.video-volume-adjust` | `video_process` | volume |
| `gsgc.video-speed` | `video_process` | speed |
| `gsgc.audio-format-convert` | `audio_converter` | output_format / audio_bitrate / audio_samplerate / audio_channel |
| `gsgc.audio-compression` | `audio_compress` | compress_rate(1-99) |
| `gsgc.audio-denoise` | `audio_denoise` | model（留空走站点默认） |
| `gsgc.image-format-convert` | `image_converter` | output_format / image_resolution / image_resolution_mode |
| `gsgc.image-compression` | `image_compress` | compress_rate(1-99) / image_compress_mode |
| `gsgc.separate` | `audio_separate` | stem（由 stems 枚举转换）/ model（缺省 103） |

- 全部匿名可用（2026-09-18 实测 audio_converter 全链路秒级完成）；上游为站点私有接口，
  改版/限流风险自担，`GET /api/gsgc/health` 探测可达性。
- 任务均为单文件输入；产物 info 对非分离任务为 null，落盘名从直链兜底。
- 注意：`audio_converter` 同时服务「视频转音频」与「音频格式转换」两个功能（与站点一致）。

## 说明

- 全族匿名可用、无需凭证，线路与模型编号硬编码在 `internal/provider/gsgc/client.go`（无配置键）。
- 站点私有接口，存在限流或改版风险，定位为**免费备用通道**（无结果缓存，同曲重跑会再次请求上游）。
- 分离产物为双轨；站点原生 WAV 在保存前自动转码标准 MP3 128k（见上）。其余功能产物格式随请求。
- **关于站点宣称的「输入什么格式输出什么格式」**：分离 create_task 请求里没有任何
  格式参数（服务端无从得知目标格式），实测 mp3 进 wav 出——服务端固定输出 WAV。
  网页端的"跟随输入格式"只是**下载改名**：把 WAV 数据命名成 `<输入基名>_<轨道>.<输入扩展名>`
  另存（前端代码 `r.download = M_e.type+"."+z` 证实），内容并未转码。
- **voxbox 的后处理**：分离产物落盘前 ffprobe 检查编码/码率，非 MP3 一律转成
  **标准 MP3 128k**（与项目 standard 默认档一致；ffmpeg -codec:a libmp3lame）。
  ffmpeg/ffprobe 缺失或转码失败时保留 WAV 降级（summary.warnings 提示），
  meta 记录 `transcoded` 与源码率 `source_bitrate`。仅 audio_separate 后处理，
  其余站点功能产物原样保存。
- 历史决策记录见 docs/plans/2026-09-18-cloudsep-online-channels.md。

## 代码结构

```
internal/provider/gsgc/
  client.go        站点协议客户端（fetch_upload_url/直传 TOS/create_task/batchGet/fetch_download_url）
  site_tools.go    10 个站点功能的通用工具（siteFunc 表：kind→task_type→参数键）
```

> 历史说明：本能力源自对 pcgeshi-go（格式工厂在线版 + APK 逆向复刻）的调研与迁移。
> 原计划的「本地 ffmpeg 引擎」（internal/pkg/gsgc：ops/ffmpeg/media/runner）曾以
> localav 之名接入，后随「只保留站点云端方案」的决策整体移除；pcg serve HTTP
> 对接方案同样未采用。图片功能在站点云端可用（image_converter/image_compress）。
