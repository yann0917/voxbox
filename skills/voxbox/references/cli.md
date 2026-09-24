# voxbox CLI 完整参考

目录：

- [通用约定](#通用约定)
- [tts 语音合成](#tts-语音合成)
- [tts-long 长文本语音合成](#tts-long-长文本语音合成)
- [tts-stream 流式语音合成](#tts-stream-流式语音合成)
- [asr 语音识别](#asr-语音识别)
- [podcast 播客生成](#podcast-播客生成)
- [separate 人声背景音分离](#separate-人声背景音分离)
- [translate 机器翻译](#translate-机器翻译)
- [minutes 语音妙记](#minutes-语音妙记)
- [run 通用工具入口](#run-通用工具入口)
- [voices 音色查询](#voices-音色查询)
- [config 配置管理](#config-配置管理)
- [serve Web 服务](#serve-web-服务)
- [mcp MCP server](#mcp-mcp-server)
- [产物链接](#产物链接)
- [错误码映射](#错误码映射)

## 通用约定

- `--json`：stdout 输出单个 JSON 对象；进度、警告等人类可读信息一律走 stderr。不加 `--json` 时输出人类可读进度条与摘要。
- JSON 结果统一结构：

```json
{
  "task_id": "uuid",
  "provider": "volcengine",
  "tool": "tts",
  "status": "succeeded",
  "cost_ms": 3210,
  "artifacts": [
    {"kind": "audio", "path": "/abs/path/speech.mp3", "format": "mp3", "size": 245760, "duration_ms": 8400}
  ],
  "summary": {"char_count": 42}
}
```

- `artifacts[].kind` 取值：`audio`（音频产物）、`transcript`（纯文本转写）、`subtitle`（SRT 字幕）、`dialog`（播客对话稿 JSON）、`translation`（译文文本）、`minutes`（妙记纪要 JSON：总结/章节/结构化/翻译）。
- 退出码：`0` 成功；`2` 用法/参数错误（含长度/载荷超限，不会消耗配额）；`3` 任务失败（上游报错，stderr 给中文原因）；`4` 凭证缺失或无效，或目标服务未开通（如机器翻译缺 `volc.speech.mt`）。
- `artifacts[].path` 一律为绝对路径（含 `--out` 重定向与默认数据目录两种来源），可直接交给下游工具使用。
- 未显式传 `--out` 时产物写入默认数据目录（`~/.voxbox/data`，可用 `config set data_dir` 修改）。
- `--out` 传相对路径时按数据目录（默认 `~/.voxbox/data`）解析；产物 JSON 中仍返回绝对路径。

## tts 语音合成

```bash
voxbox tts <text | --file path> [flags]
```

| flag | 默认 | 说明 |
|---|---|---|
| `--engine` | `volcengine` | `volcengine`（火山引擎）/ `qianwen`（千问非流式合成） |
| `--voice` | `zh_female_cancan_mars_bigtts` | 音色 ID，用 `voxbox voices list` 查询（仅火山音色）；`--engine qianwen` 用千问音色（默认 `Cherry`，共 48 官方音色） |
| `--format` | `mp3` | `mp3` / `wav` / `pcm` / `ogg_opus`（`--engine qianwen` 仅 `mp3` / `wav`） |
| `--speed-ratio` | `1.0` | 语速倍率，0.2 ~ 3.0（仅火山） |
| `--volume-ratio` | `1.0` | 音量倍率，0.2 ~ 3.0（仅火山） |
| `--qwen-model` | `qwen3-tts-flash` | 仅千问：`qwen3-tts-flash` / `qwen3-tts-instruct-flash` |
| `--instructions` | 空 | 仅千问 instruct 模型：自然语言风格指令（语速/情感/风格） |
| `--file` | — | 从文件读文本（与位置参数二选一） |
| `--out` | 数据目录自动命名 | 产物路径 |
| `--json` | 关 | 机器可读输出 |

超过 1000 字的长文本自动改走分段合成后拼接（仅支持 mp3，其他格式长文本会报参数错误），无需手动处理（仅火山）。

- `--engine qianwen` 走千问合成通道（凭证 `qianwen.api_key`）：`voxbox tts "今天天气不错" --engine qianwen --json`；
  instruct 模型可加风格指令，如 `--qwen-model qwen3-tts-instruct-flash --instructions "语速放慢，情感温柔"`。

## tts-long 长文本语音合成

```bash
voxbox tts-long <text | --file path> [flags]
```

异步长文本合成（官方 `/api/v3/tts/submit` + `/api/v3/tts/query`，seed-tts-2.0 资源）：
提交任务后轮询，完成时下载音频；支持 ≤10 万字符、分句时间戳与 SRT 字幕。
合成耗时与文本量正相关（分钟级），脚本调用请用后台方式执行并等待进程退出。

| flag | 默认 | 说明 |
|---|---|---|
| `--voice` | `zh_female_vv_uranus_bigtts` | 2.0/复刻音色 ID，用 `voxbox voices list` 查询（generation=2.0） |
| `--format` | `mp3` | `mp3` / `pcm` / `ogg_opus`（无 wav） |
| `--sample-rate` | `24000` | Hz；ogg_opus 仅支持 48000（自动强制） |
| `--speech-rate` | `0` | 语速 -50~100，100=2 倍速 |
| `--loudness-rate` | `0` | 音量 -50~100，100=2 倍音量 |
| `--timestamps` | 关 | 开启时间戳，额外产出 `.srt` 字幕（artifacts kind=`subtitle`） |
| `--resource` | `seed-tts-2.0` | 复刻音色传 `seed-icl-2.0` |
| `--model` | 空 | 复刻音色的模型版本（`req_params.model` 透传） |
| `--explicit-language` | 空 | 朗读语种：zh-cn/en/es-mx/id/pt-br |
| `--pitch` | `0` | 音调 -12~12 |
| `--bit-rate` | 服务端默认 | `64000` / `160000`；pcm 不支持指定 |
| `--aigc-watermark` | 关 | 音频结尾 AIGC 节奏标识 |
| `--file` | — | 从文件读文本（大段文本推荐） |
| `--out` | 数据目录自动命名 | 产物路径（SRT 跟随同路径 `.srt`） |
| `--json` | 关 | 机器可读输出 |

- 文本预检（退出码 2）：超 10 万字符；非法 ASCII 控制字符（除 `\t` `\n`，`\r` 算非法）占比 >10%。
- summary 含 `char_count` / `synthesized_chars` / `sentence_count` / `upstream_task_id`（火山侧任务 ID，反馈定位用）。
- 音频与链接在服务端保留 7 天 / 1 小时；voxbox 查询成功后立即下载落盘，不依赖链接有效期。

## tts-stream 流式语音合成

```bash
voxbox tts-stream <text | --file path> [flags]
```

单向流式合成（官方 `/api/v3/tts/unidirectional`，HTTP Chunked）：一次性输入文本，
服务端流式返回音频分片，到齐后落盘。seed-tts-2.0 模型，支持 20 语种、8 方言、
字级时间戳字幕（仅中英）与语音指令（`--context-text`，不参与计费）。

| flag | 默认 | 说明 |
|---|---|---|
| `--voice` | `zh_female_vv_uranus_bigtts` | 2.0/复刻音色 ID，用 `voxbox voices list` 查询 |
| `--format` | `mp3` | `mp3` / `pcm`（流式推荐）/ `ogg_opus` / `wav`（不建议） |
| `--sample-rate` | `24000` | Hz；ogg_opus 仅 48000（自动强制） |
| `--speech-rate` | `0` | 语速 -50~100，100=2 倍速 |
| `--loudness-rate` | `0` | 音量 -50~100，100=2 倍音量 |
| `--subtitle` | 关 | 字级时间戳按句末标点聚合并产出 `.srt`（仅中英语种） |
| `--resource` | `seed-tts-2.0` | 复刻音色传 `seed-icl-2.0` |
| `--model` | 空 | 复刻音色的模型版本；指定后不支持 `--context-text` |
| `--explicit-language` | 空 | 20 语种：zh-cn/en/ja/ko/de/fr/ru/th/vi/fil/ms/ar/pl/tr/sv 等 |
| `--explicit-dialect` | 空 | 方言：beijing/dongbei/henan/shaanxi/shanghai/sichuan/tianjin/yue |
| `--pitch` | `0` | 音调 -12~12 |
| `--bit-rate` | 服务端默认 | `64000` / `160000`；wav/pcm 不支持 |
| `--silence-duration` | `0` | 文本末尾静音 ms（0-30000） |
| `--context-text` | 空 | 语音指令，如「你可以用特别痛心的语气说话吗」（仅 2.0 音色） |
| `--tone-fidelity` | 关 | 还原模式，尽量复刻训练音频风格（仅复刻音色，不支持跨语种） |
| `--aigc-watermark` | 关 | 音频结尾 AIGC 节奏标识 |
| `--file` | — | 从文件读文本 |
| `--out` | 数据目录自动命名 | 产物路径（SRT 跟随同路径 `.srt`） |
| `--json` | 关 | 机器可读输出 |

- summary 含 `char_count` / `billed_chars`（计费字符数，含标点）/ `chunks`（音频分片数）/ `duration_ms`。
- 三条 TTS 通道选择：短文本秒级用 `tts`；2.0 模型/多语种/方言/语音指令用 `tts-stream`；
  ≤10 万字长文本（分句时间戳）用 `tts-long`。

## asr 语音识别

```bash
voxbox asr <file> [--url audio_url] [flags]
```

位置参数 file 与 `--url` 二选一；同传报参数错误，都不传也报参数错误。

版本按输入自动推断（`--version` 可显式覆盖）：本地文件 → `sentence` 一句话识别（单向流式大模型整段同步，秒级）；URL → `standard` 标准版（录音文件识别，异步 submit/query 后接 `idle` 闲时 / `flash` 极速）。

| flag | 默认 | 说明 |
|---|---|---|
| `--engine` | `volcengine` | `volcengine`（火山引擎，四版本）/ `qianwen`（千问，仅录音文件转写） |
| `--version` | 按输入推断 | `sentence` 一句话（仅本地文件）/ `standard` 标准版（仅 URL）/ `idle` 闲时（仅 URL）/ `flash` 极速（仅 URL）；版本与输入不匹配报参数错误（`--engine qianwen` 不适用，显式传入报参数冲突） |
| `--qwen-model` | `qwen3-asr-flash-filetrans` | 仅千问：`qwen3-asr-flash-filetrans` / `qwen-audio-3.1-asr-flash-filetrans`（说话人分离更强） |
| `--diarization` | 关 | 仅千问：说话人分离（≤2 小时且单声道音频） |
| `--out` | 数据目录自动命名 | 转写文本输出路径 |
| `--srt` | 开 | 额外产出 `.srt` 字幕（artifacts 中 kind=`subtitle`）；关闭传 `--srt=false` |
| `--hotwords` | 空 | 逗号分隔热词，提升专有名词准确率（仅火山） |
| `--language` | （空） | 识别语言，留空自动识别（中文/英文/常见方言）；可选 zh-CN/en-US/ja-JP/yue-CN 等 25 种（`--engine qianwen` 仅 zh/en/ja/ko/de/fr/ru/es/pt/it） |
| `--url` | — | 公网音频 URL，走异步批量通道 |
| `--json` | 关 | 机器可读输出 |

- 本地文件：官方协议直发一句话识别端点（全速分片），无需公网可达。
- `--engine qianwen`（凭证 `qianwen.api_key`）：异步文件转写，URL 直用，本地文件需对象存储中转；
  `--hotwords` 不生效。分句（含说话人前缀）在 JSON `summary.segments` 中（每句 `{text, start_ms, end_ms, speaker_id}`）。
- `artifacts` 中 `transcript` 为全文文本；分句与时间戳在 JSON `summary.segments` 中（每句 `{text, start_ms, end_ms}`）。
- 支持 mp3/wav/ogg/pcm（wav/pcm 内部需 pcm_s16le），其他格式报参数错误。

## podcast 播客生成

```bash
voxbox podcast <text | --file path | --url page_url | --script dialog.json> [flags]
```

| flag | 默认 | 说明 |
|---|---|---|
| `--speakers` | —（必填） | 两个音色 ID，逗号分隔，顺序为说话人 A、B；用 `voxbox voices list` 查询，缺失或数量不对报参数错误 |
| `--format` | `mp3` | `mp3` / `ogg_opus` / `pcm` / `aac` |
| `--head-music` | 关 | 是否加开头音乐 |
| `--file` | — | 从文件读播客文本（与位置参数/`--url`/`--script` 互斥） |
| `--out` | 数据目录自动命名 | 播客音频输出路径（对话稿跟随同路径 `.json`） |
| `--json` | 关 | 机器可读输出 |

四种输入互斥，最多提供一个（多个报参数错误）：位置参数 text / `--file` 文本文件 / `--url` 网页 / `--script` 对话稿 JSON 文件。

1. 位置参数 / `--file`：主题或长文本，模型自动提炼为双人对话（建议 ≤12000 字）。
2. `--url`：网页链接，服务端联网总结后生成。
3. `--script`：自备对话稿（本地 JSON 文件，CLI 读文件内容作为 script 参数提交），内容完全可控。格式：

```json
{
  "rounds": [
    {"speaker": "zh_male_dayixiansheng_v2_saturn_bigtts", "text": "开场白……"},
    {"speaker": "zh_female_mizaitongxue_v2_saturn_bigtts", "text": "回应……"}
  ]
}
```

- 产物：`audio`（播客成品）+ `dialog`（实际使用的对话稿 JSON，含每轮起止时间）。
- `summary` 结构：`rounds`（对话轮数）、`duration_s`（总时长秒）、`speakers`（两个音色 ID）、`audio_url_fallback`（音频分片为空时是否走了 audio_url 兜底下载）；有计费信息时附 `usage`。
- 注意：生成耗时数分钟，长文本更久；上游断线会自动断点续传；生成过程 stderr 实时输出逐轮进度。

## separate 人声背景音分离

```bash
voxbox separate [<url> | --file <path>] [flags]
```

四引擎，`--engine` 选择。**默认值是 `mediakit`，但 `mvsep` 通常更划算**（免费、支持本地文件直传）；
`gsgc`（格式工厂）/ `zhuanhuanmao`（转换猫，同后端镜像线路）免费且无凭证要求，适合 MVSep 额度用完时的兜底。

| flag | 默认 | 说明 |
|---|---|---|
| `--engine` | `mediakit` | `mvsep` / `mediakit`（`volcengine` 是 `mediakit` 的等价写法）/ `gsgc`（格式工厂线路，别名 `cloudsep`/`pcgeshi`/`geshi`）/ `zhuanhuanmao`（转换猫线路，别名 `convertmao`） |
| `--file` | — | 本地音频文件路径（与 URL 位置参数互斥） |
| `--sep-type` | — | **mvsep 必填**：算法 render_id，如 `48`=MelBand Roformer |
| `--scene` | `audio` | 仅 mediakit：`audio` 通用（人声+背景）/ `music` 音乐（人声+伴奏）/ `drama` 短剧、`narrate` 口播（人声+音乐+音效） |
| `--format` | `mp3` | mvsep：`0`=MP3 / `1-4`=WAV 各位深 / `5`=FLAC；mediakit：`aac`/`mp3`/`wav`/`m4a`/`flac`；gsgc/zhuanhuanmao 产物固定 MP3 128k，该 flag 不适用 |
| `--stems` | `both` | 仅 gsgc/zhuanhuanmao：`both` 双轨 / `vocals` 只提取人声 / `instrumental` 只提取伴奏 |
| `--add-opt1..3` | — | mvsep 算法附加选项（按所选算法 `algorithm_fields` 定义） |
| `--out-dir` | 数据目录 | 音轨输出目录（传相对路径时按数据目录解析） |
| `--json` | 关 | 机器可读输出 |

### engine=mvsep（MVSep，免费）

- **免费账号每日 50 次**（`price_coefficient=1` 的 118 个算法）；本地文件**服务端 direct 上传**，无需对象存储。
- 凭证：`mvsep.api_token`。账号档位与剩余额度：`GET /api/mvsep/status`（`voxbox serve` 下）。
- **坑**：系数 > 1 的算法需付费会员，免费账号提交报
  `HTTP 400: Seperation type is unavailable until you purchase premium membership`。
  `26`（Ensemble，系数 2）和 `28`（五轨，系数 4）都属此类。**免费首选 `--sep-type 48`（MelBand Roformer）**。
- 算法列表：Web 控制台分离页，或 `GET /api/mvsep/algorithms`（返回 121 个，含 `price_coefficient` 字段）。
- 产物： vocals + instrumental 两轨（`_Vocals` / `_Other`）；`artifacts[].meta.url` 为上游产物直链（公开链接，供分享/外部下载）。

### engine=gsgc（格式工厂云端通道，免费无凭证）

- 格式工厂在线版（z.pcgeshi.com）云端直连；线路与模型编号已硬编码（z.pcgeshi.com / model 103）。
- **无需任何凭证**、本地文件直传（服务端直传格式工厂的 TOS，无需对象存储中转）。
- `--stems` 双轨/单轨；站点产物为 WAV，**保存前自动转码标准 MP3（128k）**——需服务器有 ffmpeg/ffprobe，缺失或转码失败时保留 WAV 并在 summary.warnings 提示；`artifacts[].meta.url` 为上游 TOS 预签名直链（**约 1 小时有效**，指向原始 WAV）。Web 分离页该引擎 Tab 名「格式工厂 · 免费」。
- 上游为站点私有接口（逆向自网页前端），存在限流或改版风险——**定位为免费备用通道**，主力仍用 mvsep/mediakit。
- 分离不走 sep-cache（同曲重跑会再次请求上游）。
- 该通道还有 10 个站点功能（视频/音频转换压缩、图片处理），无 CLI 命令：Web 工具集页（/gsgc）或通用任务通道 `POST /api/tasks`（provider=gsgc）。

### engine=zhuanhuanmao（转换猫线路，免费无凭证）

- 转换猫（www.zhuanhuanmao.com）云端直连，与格式工厂**同一后端的镜像站点**（同 TOS 桶同 AK 同任务接口）；2026-09-19 起为独立引擎，不再作为 `gsgc` 的别名。
- **参数与产物和 `gsgc` 完全一致**（`--stems`、MP3 128k 自动转码、直链约 1 小时）；仅站点入口与分离参数形态不同（转换猫 stem 为字符串 `instrumental_vocals` 且不带 model，格式工厂为 stem 数组 + `model:103`）。
- 两路互为免费备用：一路限流/改版时切另一路（`--engine gsgc` ↔ `--engine zhuanhuanmao`）。

### engine=mediakit（火山 AI MediaKit，计费）

- 输入须为**公网可访问**的音视频 URL，或配置对象存储中转（`storage.*`）后走 `--file`；提交时按 URL 扩展名自动选择 `video_url` / `audio_url`。
- 产物：Audio/Music 场景 2 个 `audio` artifact（voice/background），Drama/Narrate 场景 3 个（voice/music/sfx）；音轨以文件名区分：`<源文件名>_<track>.<ext>`（音乐下载源自动剥掉 `.standard` 这类档位后缀，产物形如 `稻香-周杰伦_vocals.mp3`）。
- 每轨同时把上游 TOS 签名 URL 写进 `artifacts[].url`（**24 小时有效**，本地 `path` 是长期那份）；Web 侧对应 `artifact.meta.url`。
- `summary` 结构：`scene`（分离场景）、`duration_s`（媒体时长秒）、`tracks`（音轨 kind 列表，与 artifacts 顺序一致）。
- 分离为异步重计算任务：提交后自动轮询，单次最长等待 15 分钟（超时报任务失败，可重试）。
- 凭证独立：需要 `volc.mediakit.api_key`（与语音三件套无关），缺失报凭证错误（退出码 4）。

## translate 机器翻译

```bash
voxbox translate <text | --file path> [flags]
```

火山机器翻译大模型（官方 `/api/v3/machine_translation/matx_translate`，同步接口，秒级返回）：
32 语种互译，源语言缺省自动检测；支持术语定制。译文落 `translation` 产物（txt），
翻译后的文本在 JSON `summary.translation` 中也可直接读取。

| flag | 默认 | 说明 |
|---|---|---|
| `--to` | `en` | 目标语言代码（32 语种：zh/en/ja/ko/fr/de/es/pt/ru/ar/it/nl/pl/ro/sv/da/nb/fi/hu/cs/hr/el/he/tr/uk/th/vi/id/ms/tl/hi/zh-Hant） |
| `--from` | 空（自动检测） | 源语言代码，同上清单 |
| `--terms` | 空 | 直传术语：`原词=译词`，逗号或换行分隔（优先级高于术语表） |
| `--terms-file` | 空 | 从文件读术语，每行一条 `原词=译词` |
| `--table-id` / `--table-name` | 空 | 术语管理平台的术语表 ID / 名称，二选一或同传 |
| `--file` | — | 从文件读待翻译文本 |
| `--out` | 数据目录自动命名 | 译文输出路径 |
| `--json` | 关 | 机器可读输出 |

- 官方限制：单条文本 ≤1024 Tokens、单次 1 条（voxbox 当前按单文本提交）；超限时上游返回 45000130，工具层映射为参数错误（退出码 2）并提示分段。实测中文约 1.4 字符/token，3000 字符左右即触限。
- 需在火山控制台开通「机器翻译」服务（资源 ID `volc.speech.mt`）；未开通时上游返回 `requested resource not granted`，工具层识别为**退出码 4** 并提示去控制台开通（凭证本身有效，重试无意义）。
- 需在火山控制台开通 `volc.speech.mt` 权限；凭证与语音三件套共用（新版 API Key 或 APP ID + Access Token）。
- `summary` 结构：`translation`（译文文本）、`source_language` / `target_language`（回显）、
  `detected_source_language`（自动检测时返回）、`char_count`、`terms_count`、
  `prompt_tokens` / `completion_tokens` / `total_tokens`（Token 用量）。

## minutes 语音妙记

```bash
voxbox minutes <url> [flags]
```

语音妙记（官方 `/api/v3/auc/lark/submit` + `/api/v3/auc/lark/query`，资源 `volc.lark.minutes`）：
提交公网音视频 URL，异步生成结构化纪要。转写必产（带说话人，落 txt 全文与 SRT 字幕）；
附加功能至少一项，否则上游提交失败。生成耗时与音视频时长正相关（分钟级，工具层 30s 起步退避轮询，2h 兜底）。

| flag | 默认 | 说明 |
|---|---|---|
| `--features` | `summary` | 附加功能逗号分隔，至少一项：`summary` 全文总结 / `todo` 待办 / `qa` 问答 / `chapter` 章节 / `translation` 翻译 |
| `--lang` | `zh_cn` | 源语种：`zh_cn` / `en_us` |
| `--target-lang` | `en_us` | 翻译目标语（features 含 translation 时生效） |
| `--speakers` | `0` | 说话人数，0 自动识别 |
| `--hotwords` | 空 | 逗号分隔热词 |
| `--all-activate` | 开 | 打包计费；`--all-activate=false` 按所选功能汇总计费 |
| `--word-timestamps` | 关 | 需要字级时间序列 |
| `--out-dir` | 数据目录 | 结果输出目录（产物 `<uuid>_<类型>.<ext>`） |
| `--json` | 关 | 机器可读输出 |

- 官方限制：文件 <1G、时长 ≤2 小时；仅收公网 URL（音频 MP3/WAV/AAC/FLAC/OGG、视频 MP4/AVI/MKV/MOV/FLV/WMV，按扩展名自动推断 FileType）；任务 24h 未结束自动丢弃。
- 产物：`transcript`（说话人前缀全文 txt）+ `subtitle`（SRT）必产；开启的功能另落 `minutes` JSON（summary/chapter/extraction/translation）。
- `summary` 结构：`minutes_title` / `summary_text`（全文总结）、`todos`（待办：content/executor/start_time）、`chapters`（章节：title/summary/start_time/end_time）、`translation_text`（翻译纯文本）、`segments`（带说话人前缀分句）、`features`、`sentences` / `speakers_count` / `duration_ms`、`upstream_task_id`。
- 凭证同语音三件套：新版 API Key 或 APP ID + Access Token 均可（服务端两种鉴权都接受，demo 双头为兼容写法）。
- 计费：转写 1.8 元/小时（必选）+ 音频结构（集合 0.5 元/小时或单功能 0.11 元/小时×N）。

## run 通用工具入口

```bash
voxbox run <provider>.<tool> [--param key=value ...] [--json]
voxbox run <provider>.<tool> --help   # 动态查看该工具的参数 schema
```

按注册表调用任意已注册工具（含后续新接入的平台），`--param` 按工具 schema 传参。上面的快捷命令等价于 `voxbox run volcengine.tts` 等。

## voices 音色查询

```bash
voxbox voices list [--json] [--scene 场景] [--lang 语种]
# 场景：通用场景/角色扮演/视频配音/教育场景/客服场景/有声阅读/外语音色/多情感/趣味口音
# 语种：中文/美式英语/日语/韩语……（完整词表见 --json 输出 languages 字段）
```

音色表内置于程序（来源：火山引擎在线音色列表 6561/1257544，2.0+1.0 全量 500+ 条，离线可用）。
`--json` 输出对象数组，字段：`id/name/gender/scenes/languages/dialects/tags/emotions/generation/note`；
人类可读模式每行 `ID 名称 性别 场景·语种`，计数走 stderr。

## config 配置管理

```bash
voxbox config set <key> <value>       # 写入 ~/.voxbox/config.yaml（0600）
voxbox config list                    # 查看配置（密钥打码显示）
```

| key | 说明 |
|---|---|
| `volc.speech.app_id` | 火山引擎语音 APP ID（TTS/ASR/播客/翻译/妙记共用；播客必须 APP ID + Token，其余支持新版 API Key 单键） |
| `volc.speech.access_token` | 语音 Access Token |
| `volc.speech.api_key` | 新版控制台 API Key（TTS/ASR/翻译可用；播客必须 APP ID + Access Token） |
| `volc.mediakit.api_key` | AI MediaKit API Key（人声分离火山通道） |
| `mvsep.api_token` | MVSep API Token（mvsep.com 注册后全页 API 页获取） |
| `mvsep.base_url` | MVSep 线路覆写，空=主站 geo 就近（可选 de/de2/hk.mvsep.com；同一任务须全程固定线路） |
| `qianwen.api_key` | 千问平台 API Key（`tts` / `asr` 的 `--engine qianwen` 使用） |
| `server.port` | Web 端口，默认 8081 |
| `data_dir` | 产物数据目录，默认 `~/.voxbox/data` |

**对象存储**（本地文件中转通道，按存储类型分段、互不覆盖；Web 设置页整表保存等价）：

| key | 说明 |
|---|---|
| `storage.provider` | 启用通道选择器：`tos` / `oss`；空=未启用 |
| `storage.providers.tos.*` | 火山 TOS 通道段：endpoint（如 tos-cn-beijing.volces.com）/ region / bucket / access_key / secret_key / prefix |
| `storage.providers.oss.*` | 阿里 OSS 通道段：字段同上（endpoint 如 oss-cn-hangzhou.aliyuncs.com，region 参与 V4 签名） |

仅当无任何分段时旧平铺键（`storage.endpoint` 等）会被一次性自动迁移，之后不再读取；`config list` 按
`storage.providers.<名>.bucket / access_key / secret_key` 分通道列出。

## serve Web 服务

```bash
voxbox serve [--port 8081]
```

启动 Web 控制台（含 REST API 与 WebSocket）。CLI 与 Web 共用同一数据目录与任务历史。
`--port` 缺省取配置文件（默认 `8081`），**仅监听 `127.0.0.1`**。

## mcp MCP server

```bash
voxbox mcp        # stdio 模式，客户端拉起子进程
voxbox serve      # 内嵌 Streamable HTTP 端点 /api/mcp（与 Web 同进程同端口）
```

两种传输共用同一工具集与 JSON 契约（与 CLI `--json` 一致）。15 个工具：

`voxbox_tts` / `voxbox_tts_long` / `voxbox_tts_stream` / `voxbox_asr` / `voxbox_podcast` /
`voxbox_separate` / `voxbox_translate` / `voxbox_minutes` / `voxbox_voices` /
`voxbox_audio_edit` / `voxbox_audio_mix` / `voxbox_audio_analyze` / `voxbox_audio_hook` / `voxbox_audio_clip` / `voxbox_audio_duck`

客户端配置：

```json
{"mcpServers": {"voxbox": {"command": "/path/to/voxbox", "args": ["mcp"]}}}
{"mcpServers": {"voxbox": {"url": "http://127.0.0.1:8081/api/mcp"}}}
```

- `voxbox_separate` 支持 `file`（本地文件）+ `engine`（`mvsep`/`gsgc`/`zhuanhuanmao`/`mediakit`）。
- 工具调用**串行排队**执行（SQLite 单写者）；分钟级任务会阻塞后续调用，避免并发发起多个长任务。
- **别把端口暴露到公网** —— MCP 工具可触发计费云 API。
- 同一个数据目录下**不要同时**运行 stdio 与 serve，会争 SQLite 写锁报 `database is locked`。

## 产物链接

CLI 的 `artifacts[].path` 是**本机绝对路径**。要可点击播放的 URL，需先 `voxbox serve`：

```bash
GET /api/artifacts/<artifact_id>/stream     # 支持 Range，可流式播放（<audio> 直接可用）
GET /api/artifacts/<artifact_id>/download   # Content-Disposition 触发下载
```

`artifact_id` 取自 `GET /api/tasks/<task_id>` 返回的 `artifacts[].id`。
服务仅监听 `127.0.0.1`，链接**仅本机可访问、不可外发**。

**例外（先查再传）**：转存型产物**自带公网链接**，无需 `serve`、也无需再传对象存储 ——
火山分离各轨（`separate --engine mediakit|volcengine`）的上游 TOS 签名 URL 就是外发地址，
字段位置见 [separate 人声背景音分离](#separate-人声背景音分离)（`artifacts[].url` / REST `artifacts[].meta.url`，24 小时有效）。
实测把 `config.yaml` 通道段的 `access_key` / `secret_key` 解出来手工签 TOS S3 请求不可行
（`ListObjectsV2` 回 `InvalidAccessKeyId`）；本地文件需中转时交给 CLI 内置存储逻辑。

## 错误码映射

| 上游情形 | 退出码 | stderr 提示方向 |
|---|---|---|
| 鉴权失败（App ID / Token 错误） | 4 | 「凭证无效，请检查 config」 |
| 未配置凭证 | 4 | 「先执行 voxbox config set …」 |
| 目标服务未开通（如 `volc.speech.mt`） | 4 | 「去控制台开通对应服务」（重试无意义） |
| 妙记任务失败（ErrCode 4801-4813） | 3 | 「空音频/url 无效/时长超限」等中文原因 |
| 参数错误 / 长度超限 | 2 | 具体参数与限制 |
| 内容审核拦截 | 3 | 「内容触发安全审核」 |
| 额度/余额不足 | 3 | 「资源包额度不足」 |
| 限流 | 3 | 「触发限流，请稍后重试」 |
| 网络中断 | 3 | 「连接中断（已尝试断点续传）」 |
