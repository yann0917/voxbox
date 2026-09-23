# 设置页云端/本地重构 + Provider 元数据注册表 + 千问平台接入 · 设计文档

日期：2026-09-24
状态：已与用户确认设计方向，待实施

## 1. 背景与目标

voxbox 当前设置页是平铺卡片（火山语音 / MediaKit / MVSep / 对象存储 / 通知 / 存储位置 / API Token），`PUT /api/settings` 收平铺凭证字段，`Service.SaveCredentials` 已有 6 个位置参数，每加一家厂商都要改端点、service、前端三处。本地能力（audiotool / gsgc）与云端服务在设置里没有区分。

目标：

1. 设置页重构为「云端服务 / 本地环境」两个分区，云端各厂商凭证卡由后端 **Provider 元数据注册表** 动态驱动，后续新增厂商只需在自己的 provider 包里声明元数据。
2. 新接入千问平台（qianwen）云端服务：TTS 非流式（`qwen3-tts-flash`）+ ASR 文件转写（filetrans），凭证 `qianwen.api_key`。
3. TTS / 语音识别功能页增加引擎选择（火山引擎 / 千问）。

### 已确认的设计决策

- provider 命名：**`qianwen`**（注册表键、任务提交 `provider` 字段、CLI `--engine` 值统一用它）。
- 千问 **不提供 `base_url` 覆写**，固定 `https://maas.qianwenaiapi.com`。
- **无兼容期**：本次连设置页一起重写，旧 `PUT /api/settings` 的凭证平铺分支直接移除（前后端同二进制发布，无版本偏差风险；`webdist` 内是同一份前端的构建产物，不构成独立调用方）。存储保存迁到独立端点。

### 非目标（本次不做）

- 千问流式/实时 TTS（WebSocket）、CosyVoice 家族、语音克隆、声音设计、SSML。
- 千问 ASR 同步 flash 模式（≤5min base64 直传）与实时流式识别。
- 对象存储新增通道类型（腾讯 COS 等）。
- `/api/voices` 端点扩展——千问音色以工具 ParamSpec 枚举提供，不加新端点。
- CLI/MCP 之外的新客户端。

## 2. 现状摘要

- 工具抽象：`internal/provider` 的 `Tool` 接口 + `Registry`（key = `provider.tool`），凭证热加载走 `Registry.Replace`。
- Provider 包：`volcengine`（tts / tts_long / tts_stream / asr / podcast / translate / minutes / separate，语音凭证 + MediaKit 凭证）、`mvsep`（separate）、`audiotool`（12 个本地 ffmpeg 工具，无凭证）、`gsgc`（站点协议工具，匿名）。
- 配置：`~/.voxbox/config.yaml`，`volc.speech.*`、`volc.mediakit.api_key`、`mvsep.*`、`storage.provider` + `storage.providers.<名>.*`（多通道独立段）。
- 设置 API：`GET /api/settings`（脱敏读）、`PUT /api/settings`（凭证平铺 + 可选 storage 段）、`POST /api/settings/test-connection`（volcengine 顶层 + mediakit/mvsep/storage 分段）。
- 前端：SettingsPage 平铺卡；TTSPage 三面板（同步/长文本/流式）、ASRPage 均硬编码 `provider: "volcengine"`。
- ASR 产物形态：txt + srt（句级时间戳），字幕/妙记下游消费。
- 对象存储桥：URL-only 上游工具经 `TaskInput.Storage` 把本地文件转存并取预签名 URL（volcengine storage_bridge 模式）。
- 测试：`go test ./...`；前端无单测基建，走 typecheck/build + 手工验收。

## 3. Provider 元数据注册表

### 3.1 类型（`internal/provider` 新增，如 `providersettings.go`）

```go
type ProviderKind string

const (
    KindCloud ProviderKind = "cloud" // 云端服务：需凭证，设置页云端组渲染凭证卡
    KindLocal ProviderKind = "local" // 本地能力：无凭证，设置页本地组只读展示
)

type FieldKind string // "text" | "secret" | "select"

type CredentialField struct {
    Key         string        // API 载荷键（PUT body 里的字段名），如 "api_key"
    Label       string
    Kind        FieldKind     // text=明文输入；secret=敏感（回显 has_<key>，保存留空=不改）；select=下拉
    ConfigKey   string        // yaml 落盘键，如 "qianwen.api_key"
    Options     []ParamOption // select 用（如 MVSep 接入线路）
    Required    bool          // configured 判定参与项
    Placeholder string
    Hint        string
}

type ProviderInfo struct {
    Name        string // 注册表/任务提交键："volcengine" | "mediakit" | "mvsep" | "qianwen" | "audiotool" | "gsgc"
    Title       string // 卡片标题，如 "火山引擎 · 语音合成/识别/播客"
    Description string // 卡片说明（凭证获取指引等）
    Kind        ProviderKind
    Fields      []CredentialField // KindLocal 恒空
    Order       int               // 云端组内卡片排序
}
```

### 3.2 声明与装配

- 各 provider 包暴露 `ProviderCard() ProviderInfo`：
  - `volcengine`：app_id（text，`volc.speech.app_id`）/ access_token（secret，`volc.speech.access_token`）/ api_key（secret，`volc.speech.api_key`），文案沿用现卡片（播客必须 APP ID+Token 等说明）。
  - `mediakit`：独立卡，api_key（secret，落盘仍 `volc.mediakit.api_key`）。
  - `mvsep`：api_token（secret）+ base_url（select：主站/hk/de/de2）。
  - `qianwen`：api_key（secret，`qianwen.api_key`）。
  - `audiotool` / `gsgc`（含 zhuanhuanmao 线路）：`KindLocal`，无字段。
- 装配点集中在 service 启动路径（新 `providerCards()` 列表），与工具注册同处维护；设置读端点遍历此表。
- **落盘键零迁移**：全部沿用现键，新增仅 `qianwen.api_key`。`voxbox config set` 老用法不受影响。
- `configured` 判定：该卡 Required 的 secret/text 字段有值（沿用各卡现有判定口径：volcengine = app_id+token 或 api_key 任一组合可用；mediakit/mvsep/qianwen = 对应 secret 有值）。

## 4. 设置 API 变更

### 4.1 `GET /api/settings`（读）

响应新增 `providers` 数组（按 Order 排序）：

```json
{
  "providers": [
    {
      "name": "volcengine", "title": "…", "description": "…",
      "kind": "cloud", "order": 10, "configured": true,
      "fields": [
        {"key": "app_id", "label": "APP ID", "kind": "text", "value": "xxx", "placeholder": "…", "hint": "…"},
        {"key": "access_token", "label": "Access Token", "kind": "secret", "has_value": true, "hint": "…"}
      ]
    }
  ],
  "storage": { … 现状不变 … },
  "data_dir": "…"
}
```

旧响应的 `volc` / `mvsep` 平铺段随前端同次重写一并移除（无兼容期决策）；`storage` 段保留（`useStorageEnabled` 与存储卡继续消费）。

非 secret 字段回传 `value`；secret 字段只回 `has_value`。`kind=local` 的卡也出现在数组（fields 空），供本地组渲染能力清单（名称、描述、工具数——工具数由 Registry 按 provider 聚合）。

### 4.2 `PUT /api/settings/providers/<name>`（新增，凭证保存）

- body：`{"fields": {"api_key": "sk-…", "base_url": ""}}`；只接受该卡声明的字段，未知字段报 400。
- secret 字段空串 = 不修改；text/select 空串 = 清空/回落（沿用现 MVSep base_url 语义）。
- 落盘复用现 SaveCredentials 的 viper 整写机制（多键一次写回，保持 0600 权限）。
- 成功后只热重注册该卡对应的工具集：卡片名 → Reload 钩子映射（`volcengine`/`mediakit` → volcengine 包 `ReRegisterAll`；`mvsep` → mvsep 重注册；`qianwen` → qianwen 包 `ReRegisterAll`）。
- `Service.SaveCredentials`（6 位置参数）删除，替换为 `SaveProviderFields(name string, fields map[string]string) error`。

### 4.3 `PUT /api/settings/storage`（新增，存储保存）

- body 与现 `PUT /api/settings` 的 storage 分支完全一致（provider/endpoint/region/bucket/access_key/secret_key/prefix，secret 留空=不改）。
- 旧 `PUT /api/settings` 端点整体删除（凭证分支与 storage 分支都不再保留）。

### 4.4 `POST /api/settings/test-connection`

响应改为按卡动态：

```json
{"results": [{"name": "volcengine", "ok": true, "message": "…"}, …], "storage": {"ok": true, "message": "…"}}
```

- 每张 cloud 卡声明 Test 函数（可空=不测）。volcengine/mediakit/mvsep 沿用现探测逻辑；qianwen = 极短文本合成探活（同火山语音模式，消耗少量额度）。
- 顶层 `ok`/`message`（火山专用）与 `mediakit`/`mvsep` 分段字段删除，前端同步改。

## 5. 设置页 UI（云端 / 本地两 Tab）

- **云端服务 Tab**：
  - 各 cloud 卡按 `providers` 数组动态渲染：字段类型驱动（secret→SecretInput、select→Select、text→Input），每卡独立「保存」按钮 → `PUT /api/settings/providers/<name>`。
  - 对象存储卡：现状表单/逻辑不动，仅保存端点换成 `PUT /api/settings/storage`。
  - 连通性测试卡：按钮 → 新响应结构，逐卡渲染结果徽标。
- **本地环境 Tab**：
  - 数据目录（只读）、任务通知开关（localStorage，现状逻辑）、API Token（现状逻辑）。
  - 本地能力卡：`kind=local` 的 provider 清单（标题、描述、工具数），只读。
- `SettingsShape`（web）改为以 `providers` 数组为主；`useStorageEnabled` 读 storage 段不变。

## 6. 千问 provider（`internal/provider/qianwen`）

### 6.1 凭证与注册

- 凭证：`qianwen.api_key`（secret）。Base 固定 `https://maas.qianwenaiapi.com`，不覆写。
- 鉴权：`Authorization: Bearer <api_key>`。
- 工具注册/热重注册模式与 volcengine 一致（凭证缺失仍注册，Run 时报凭证错误）。

### 6.2 工具 `qianwen.tts`（非流式）

- 端点：`POST {base}/api/v1/services/aigc/multimodal-generation/generation`（DashScope 多模态生成协议，SDK `MultiModalConversation.call` 对应通道）。
- 参数（ParamSpecs）：
  - `text`（text，必填）：待合成文本。
  - `model`（enum）：`qwen3-tts-flash`（默认）/ `qwen3-tts-instruct-flash`。
  - `voice`（enum）：内嵌官方音色表（实现时从 `docs/developer-guides/speech/voice-list/qwen-tts` 固化进包内静态表，形态参照 volcengine voices）。
  - `language_type`（enum，如 中文/English，默认中文）。
  - `instructions`（text，仅 instruct 模型显示意义）：自然语言风格指令（语速/情感/风格）。
  - `format`（enum）：wav / mp3（默认 mp3）。
- 响应：JSON 内含合成音频下载 URL（24h 有效）→ 下载落盘为音频 artifact（kind=audio），Summary 带音频时长等元数据。
- ⚠️ 实现期校准项：文档以 SDK 参数展示，**原始 HTTP 请求体（messages 结构、响应 JSON 路径）须在实现第一步用真实 key curl 验证**后固化 client 结构体与测试夹具。

### 6.3 工具 `qianwen.asr`（文件转写 filetrans）

- 提交：`POST {base}/api/v1/services/audio/asr/transcription`，头 `X-DashScope-Async: enable`。
  - `model`（enum）：`qwen3-asr-flash-filetrans`（默认）/ `qwen-audio-3.1-asr-flash-filetrans`（注意两者 `input` 形态：qwen3 为 `file_url`，qwen-audio 为 `file_urls` 数组）。
  - 输入：公网 URL；本地文件经 `TaskInput.Storage` 预签名桥转存（未配置对象存储时按 volcengine 同款报错文案给配置指引）。
  - `parameters`（ParamSpecs 暴露）：`language_hints`（语言提示；ParamSpec 形态为单选枚举 `auto`/`zh`/`en`/`ja` 等 + 可空逗号分隔文本兜底，提交端映射为数组）、`diarization_enabled`（说话人分离，≤2h 单声道）、`enable_itn`、`enable_words`（词级时间戳）。
- 轮询：`GET {base}/api/v1/tasks/{task_id}`（输出 `output.task_status`：PENDING/RUNNING/SUCCEEDED/FAILED），指数退避，沿用任务引擎 ProgressReporter 上报进度。
- 结果：`output.results[].transcription_url` 拉取 JSON → `transcripts[].sentences[]`（`begin_time`/`end_time`/`text`/`words[]`/`speaker_id`/`emotion`/`language`）。
- 产物：txt + srt（时间戳映射与 volcengine asr 产物同构），说话人分离开启时文本带说话人标注（对齐 volcengine 现有格式约定）。
- 限制提示：≤12h/≤2GB；diarization ≤2h 且单声道。

### 6.4 连通性测试

- `qianwen` 卡 Test：极短文本（如「你好」）合成请求，成功=ok；401/403 报凭证无效，其余报上游信息。

## 7. 功能页引擎切换

- **TTSPage**：顶部引擎选择（火山引擎 / 千问）。引擎可用性由 settings `providers.configured` 驱动（千问未配置时禁用并给「去设置」引导）。
  - 火山：现三面板（同步/长文本/流式）不动。
  - 千问：单面板（文本、模型、音色、语言、instruct 指令、格式），提交 `{provider: "qianwen", tool: "tts", params}`。
- **ASRPage**：同构引擎选择。千问面板：文件上传/URL、模型、说话人分离开关、language_hints、ITN/词级时间戳；提交 `{provider: "qianwen", tool: "asr"}`。
- 面板字段以 `/api/tools` 返回的 qianwen 工具 ParamSpecs 为准渲染（schema 驱动），避免前端再硬编码一套。
- 结果展示、历史、产物下载复用现有任务链路（无改动）。

## 8. CLI / MCP

- CLI：`voxbox tts` 与 `voxbox asr` 增加 `--engine` 标志（默认 `volcengine`），映射 `runToolSync(c, engine, "tts"/"asr", …)`；参数集沿用各引擎 ParamSpecs。
- MCP：任务提交本就以 provider/tool 寻址，qianwen 工具注册后自动可见，无需改动。

## 9. 测试策略

- 后端（`go test ./...`）：
  - provider 元数据：卡声明快照测试（字段/落盘键/Required 齐全性——ConfigKey 必须非空等约束）。
  - `SaveProviderFields`：落盘正确性（含 secret 留空不改）、未知字段 400、热重注册触发。
  - qianwen client（httptest）：TTS 响应解析 + URL 下载落盘；ASR 提交/轮询状态机（PENDING→RUNNING→SUCCEEDED/FAILED）；transcription JSON → txt/srt 生成；qwen3 与 qwen-audio 两种 input 形态。
  - 路由：新端点（providers 保存、storage 保存、test-connection 新响应）契约测试；旧 `PUT /api/settings` 移除后 404。
- 前端：`npm run build`（tsc + vite）通过；手工走查两 Tab、动态卡渲染、引擎切换提交载荷。
- 端到端手工验收：真实千问 key——TTS 出音频可播放；≥30min 音频 ASR 出 txt+srt 时间戳正确；配置删除后工具报凭证指引。

## 10. 涉及面清单

| 层 | 文件/包 | 变更 |
|---|---|---|
| provider 抽象 | `internal/provider/providersettings.go`（新） | ProviderKind/CredentialField/ProviderInfo |
| volcengine | `provider.go` 等 | 卡声明、ReRegisterAll 复用、SaveCredentials 拆除配套 |
| mediakit/mvsep | 各包 | 卡声明 + reload 钩子 |
| qianwen | `internal/provider/qianwen/`（新） | 卡声明、tts/asr 工具、client、音色静态表、测试 |
| config | `internal/config/config.go` | 读 `qianwen.api_key`；多键写回辅助 |
| service | `internal/service/service.go` | SaveProviderFields + 卡→reload 映射；Test 装配 |
| server | `internal/server/routes.go` | 新读写字段、新端点、删旧 PUT 凭证分支、test-connection 新响应 |
| web | `SettingsPage.tsx`、`lib/useStorageEnabled.ts` | 两 Tab、动态卡、storage 新端点 |
| web | `TTSPage.tsx`、`tts/*`、`ASRPage.tsx` | 引擎选择 + 千问面板（schema 驱动） |
| CLI | `cmd/voxbox/tts.go`、`asr.go` | `--engine` 标志 |
