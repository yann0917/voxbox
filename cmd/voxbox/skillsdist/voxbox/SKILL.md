---
name: voxbox
description: 语音 AI 工具箱 CLI（voxbox / voxbox.exe）——语音合成（TTS / 流式 / 长文本）、语音识别 ASR、AI 双人播客、人声与伴奏分离（MVSep 免费 / 格式工厂云端免费 / 转换猫镜像线路免费 / 火山 MediaKit 四引擎，支持本地文件）、音频剪辑（切割/合并/变调/均衡/混音/闪避/副歌切片）、机器翻译、语音妙记（音视频转结构化纪要）。Use when the user asks to 文字转语音 / 配音 / TTS / 有声书 / 方言配音 / 多语种配音、音频视频转文字 / 出字幕 / ASR 转写、生成播客、分离人声与伴奏 / 提取伴奏 / 卡拉OK / 去人声、剪辑音频 / 提取副歌 / 制作铃声 / 口播闪避 / 垫音混音、翻译文本、会议纪要 / 音视频转纪要 / 待办提取，or otherwise needs speech / audio / translation / minutes processing.
---

# voxbox

通过 `voxbox` 命令行完成语音、音频、翻译与纪要类处理。命令**同步执行**：进程退出即任务完成，产物路径直接可用。

> 本 skill 面向「只有二进制、没有源码」的部署环境 —— 所有能力均由 `voxbox` 可执行文件提供，不依赖仓库。命令有变动时以 `voxbox --help` 为准。

## 前置检查

1. **确认可执行文件**：`voxbox --version`（Windows 为 `voxbox.exe`）。找不到时问用户二进制路径，不要假设在 PATH 里。
2. **确认凭证**：`voxbox config list`。按空该项即禁用对应能力：

   | 凭证键 | 影响能力 |
   |---|---|
   | `volc.speech.app_id` + `access_token` | 播客（两个都必需） |
   | `volc.speech.api_key` | TTS / 长文本 / 流式 / 翻译 / 妙记 |
   | `volc.speech.access_token` | ASR |
   | `volc.mediakit.api_key` | 人声分离（MediaKit 引擎） |
   | `mvsep.api_token` | 人声分离（MVSep 引擎） |
   | `qianwen.api_key` | TTS / ASR（千问引擎 `--engine qianwen`） |
   | `storage.*` | 本地文件走「只收 URL」的上游时自动中转（分离/妙记/ASR 标准版） |

   缺失时请用户提供密钥，再 `voxbox config set <key> <value>`。**不要猜测或编造密钥**。

## 调用规则

- **始终加 `--json`**：stdout 只输出一个 JSON 对象（含产物路径、耗时），人类可读进度走 stderr。解析 stdout 时忽略 stderr。
- **显式指定产物路径**：`--out`（`separate` 用 `--out-dir`），避免依赖默认数据目录。
- 产物从 JSON 的 `artifacts[]` 读：`path`（**恒为绝对路径**）、`kind`（`audio`/`transcript`/`subtitle`/`dialog`/`translation`）、`format`、`size`。
- 退出码：`0` 成功；`2` 参数错误；`3` 任务失败（stderr 有中文原因）；`4` 凭证缺失或未开通。
- 耗时：TTS / 翻译 / 音色查询秒级；ASR 秒~分钟；**分离 / 播客 / 妙记分钟级**，用后台方式执行并轮询进程退出。

## 能力速查

```bash
# ---------- 语音合成 ----------
voxbox tts "今天天气不错" --voice zh_male_dayixiansheng_v2_saturn_bigtts --out speech.mp3 --json
voxbox tts "今天天气不错" --engine qianwen --out speech-qw.mp3 --json                  # 千问引擎（凭证 qianwen.api_key，默认音色 Cherry）
voxbox tts-long --file book.txt --timestamps --out audiobook.mp3 --json   # ≤10 万字，出 SRT
voxbox tts-stream "用粤语说一段开场白" --explicit-dialect yue --subtitle --out intro.mp3 --json

# ---------- 语音识别 ----------
voxbox asr recording.mp3 --out transcript.txt --json                      # 本地文件（一句话识别）
voxbox asr --url "https://example.com/a.mp3" --version flash --json       # 公网 URL（极速版）
voxbox asr --url "https://example.com/a.mp3" --engine qianwen --json      # 千问文件转写（异步，凭证 qianwen.api_key）

# ---------- 播客 / 翻译 / 妙记 ----------
voxbox podcast "介绍大模型在语音方向的应用" --speakers <voiceA>,<voiceB> --out podcast.mp3 --json
voxbox translate "火山引擎是字节跳动旗下的企业级智能技术服务平台" --to en --out translated.txt --json
voxbox minutes "https://example.com/meeting.mp4" --features summary,todo,chapter --out-dir ./minutes --json

# ---------- 人声分离（四引擎，见下节）----------
voxbox separate --file song.flac --engine mvsep --sep-type 48 --format 0 --out-dir ./sep --json
voxbox separate "https://example.com/video.mp4" --scene audio --out-dir ./sep --json

voxbox voices list --json
```

## 人声分离：选引擎

四个引擎能力差异明显，**默认优先 MVSep**：

| | `--engine mvsep`（推荐） | `--engine gsgc`（格式工厂云端） | `--engine zhuanhuanmao`（转换猫线路） | `--engine mediakit`（默认值，火山） |
|---|---|---|---|---|
| 费用 | 免费账号每日 50 次 | 免费（站点云端，私有协议有改版风险） | 免费（同 gsgc，镜像线路互为备用） | 按次计费 |
| 凭证 | `mvsep.api_token` | **无需任何凭证** | **无需任何凭证** | `volc.mediakit.api_key` |
| 本地文件 | ✅ `--file` **直传**，无需对象存储 | ✅ `--file` 直传，无需对象存储 | ✅ `--file` 直传，无需对象存储 | ⚠️ 需配置对象存储中转，否则只收公网 URL |
| 算法 | 120+ 算法，`--sep-type` 必填 | 固定模型（站点 model 103） | 固定模型（站点私有，不带 model 提交） | 4 个场景：`audio`/`music`/`drama`/`narrate` |
| 格式 | `--format 0`(MP3) / `1-4`(WAV 各位深) / `5`(FLAC) | 双轨 MP3（128k 自动转码） | 双轨 MP3（128k 自动转码） | `aac`/`mp3`/`wav`/`m4a`/`flac` |
| 轨道 | vocals + instrumental 两轨 | `--stems both/vocals/instrumental` | `--stems both/vocals/instrumental` | audio/music 两轨；drama/narrate 三轨 |

> `--engine gsgc` 是格式工厂在线版云端直连（z.pcgeshi.com），`--engine zhuanhuanmao` 是转换猫线路
> （www.zhuanhuanmao.com，与格式工厂同后端、参数同 gsgc），两路互为免费兜底；
> `--engine mediakit` 与 `--engine volcengine` 等价（火山链路的两种写法）；MCP 的 `voxbox_separate`
> 同样接受这两种写法，`scene` 只在火山链路生效。
> 火山分离产物会**转存到本地**，同时把上游产物地址（火山 TOS 签名 URL，形如
> `*.vod.<region>.volcvideo.com/...?preview=1&auth_key=<ts>-r0-u0-<sig>`，**24 小时有效**）写进 `Artifact.Meta["url"]`。
> 出口字段随通道不同：CLI `--json` 与 MCP 平铺为 `artifacts[].url`；REST `GET /api/tasks/<id>` 里是 `artifacts[].meta.url`。
> **这条就是要外发的分享链接，不必再自行上传对象存储。**

**关键坑（实测）**：MVSep 算法有 `price_coefficient` 系数，**系数 > 1 的算法要付费会员**，免费账号提交会收到
`HTTP 400: Seperation type is unavailable until you purchase premium membership`。

- 免费可用：系数 = 1 的 118 个算法。**`--sep-type 48`（MelBand Roformer）质量最好**，是免费档首选。
- 付费不可用：`26`（Ensemble，系数 2）、`28`（五轨 Ensemble，系数 4）等。
- 查算法列表：Web 控制台分离页，或 `GET /api/mvsep/algorithms`（`voxbox serve` 下）。

## 产物链接

CLI 输出的 `artifacts[].path` 是**本机绝对路径**。要可点击播放的 URL，需 `voxbox serve` 起服务后用：

```
http://127.0.0.1:<port>/api/artifacts/<artifact_id>/stream    # 支持 Range，可流式播放
http://127.0.0.1:<port>/api/artifacts/<artifact_id>/download  # 触发下载
```

`artifact_id` 从 `GET /api/tasks/<task_id>` 的 `artifacts[].id` 取。**注意**：`serve` 默认只监听
`127.0.0.1`，该链接仅本机可用，不可外发。

**但要先分清产物有没有「自带的公网链接」**：

| 产物来源 | 公网链接 | 取法 |
|---|---|---|
| `separate --engine mediakit`（= `volcengine`） | ✅ 火山 TOS 签名 URL，24 小时有效 | CLI `--json` / MCP：`artifacts[].url`；REST：`artifacts[].meta.url` |
| MVSep、TTS、ASR 等纯本地产物 | ❌ 只有本机路径 | 需 `serve` 出本机链接，或自行上传对象存储 |

也就是说 **mediakit 分离完就已经有可外发的 URL 了，别再折腾 TOS 直传**。实测想把 `config.yaml` 里的
`storage.access_key` / `secret_key` 解出来手工签 TOS S3 请求这条路走不通（`ListObjectsV2` 回
`InvalidAccessKeyId`）—— 本地文件要中转时交给 CLI 自带的存储逻辑即可。

## 参数选择要点

- 音色 ID 用 `voxbox voices list --json` 查（可 `--scene`/`--lang`/`--gender` 过滤），**不要编造音色 ID**。
- 短文本配音用 `tts`（秒级）或 `tts-stream`（20 语种 / 8 方言 / 语音指令 / 字级字幕）；≤10 万字用 `tts-long`（分钟级，可出字幕）。
- ASR 遇背景音重的音频，**先分离提纯人声再转写**，准确率更高（两个命令串联）。
- 播客建议 ≤12000 字；已写好对话稿用 `--script dialog.json` 更可控。
- 妙记仅收**公网 URL**（<1G、≤2 小时），要「纪要」而非纯转写时用它；纯转写或本地文件用 `asr`。
- 翻译单条 ≤1024 Tokens，超限分段；术语（产品名/专有名词）用 `--terms "原词=译词"` 提一致性。

## MCP 接入（AI Agent 首选）

`voxbox` 自带 MCP server，工具名 `voxbox_*`，输出与 `--json` 同一契约：

- **stdio**（客户端拉起子进程，最稳）：`{"command":"/path/to/voxbox","args":["mcp"]}`
- **HTTP**（复用已运行的 serve，与 Web 共享任务引擎）：`{"url":"http://127.0.0.1:8081/api/mcp"}`

15 个工具：`voxbox_tts` / `voxbox_tts_long` / `voxbox_tts_stream` / `voxbox_asr` / `voxbox_podcast` /
`voxbox_separate`（支持 `file` 本地文件 + `engine=mvsep|gsgc|zhuanhuanmao|mediakit`） / `voxbox_translate` / `voxbox_minutes` /
`voxbox_voices` / `voxbox_audio_edit` / `voxbox_audio_mix` / `voxbox_audio_analyze` / `voxbox_audio_hook` / `voxbox_audio_clip` / `voxbox_audio_duck`。

> 注意：同一台机器上**不要同时**跑 stdio 与 serve，两者争 SQLite 写锁会报 `database is locked`。
> 已经用 `voxbox serve` 时，MCP 走 HTTP url 方式。

## 完整参考

所有命令、flag、JSON 输出结构、错误码映射见 [references/cli.md](references/cli.md)。
运行时也可 `voxbox <command> --help` 查看。
