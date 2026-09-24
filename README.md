# voxbox

个人自用的语音 AI 工具箱：一套 Go 二进制，既是 CLI 也是 Web 控制台，围绕语音合成/识别与音频处理接入八个能力。

| 能力 | 说明 | 接口形态 |
|---|---|---|
| 语音合成 TTS | 文本转语音：同步秒级 / 流式低延迟（20 语种、8 方言、字级字幕、语音指令）/ 长文本（≤10 万字）异步合成，均可出 SRT 字幕 | HTTP + Chunked 流式 + 异步任务 + WebSocket |
| 语音识别 ASR | 本地文件/URL 转文字，分句时间戳、SRT 字幕 | 三版本：本地直发（官方协议）/ URL 异步（标准） / 闲时 / 极速同步 |
| 语音播客 | 主题/长文本/网页/对话稿一键生成双人播客 | WebSocket 事件流，支持断点续传 |
| 人声背景音分离 | 公网音视频 URL 多轨分离：Audio/Music 双轨（人声+背景/伴奏），Drama/Narrate 三轨（人声+音乐+音效） | AI MediaKit REST（产物 24h 临时链接，立即转存本地） |
| 音乐源分离（MVSep） | mvsep.com 120+ 分离算法：人声/伴奏、鼓、贝斯、键盘、吉他等 15 组；免费账号每天 50 次 | MVSep REST（multipart 直传 + hash 轮询，产物多轨转存本地） |
| 格式工厂工具集 | z.pcgeshi.com 云端直连（免费匿名）：人声/伴奏分离、视频转换压缩、音频转换压缩降噪、图片转换压缩，共 11 项（人声分离另有转换猫镜像线路 `zhuanhuanmao`，同后端不同站点入口） | 站点私有任务协议逆向（预签名直传 TOS → create_task → 轮询 → 产物直链） |
| 机器翻译 | 32 语种互译、自动检测源语言、术语定制（直传术语 / 术语表） | HTTP 同步（matx_translate，需开通 volc.speech.mt） |
| 语音妙记 | 音视频 URL 转结构化纪要：转写+说话人、全文总结、待办/问答提取、章节总结、中英翻译（≤2h、<1G） | HTTP 异步（lark submit/query，结果链接 24h 有效立即转存） |
| 千问平台语音 | qwen3-tts 非流式合成（48 官方音色、instruct 模型风格指令）+ qwen3-asr 录音文件转写（长音频 ≤12h、说话人分离、SRT 字幕） | DashScope 兼容 HTTP（固定入口，Bearer API Key） |
| 小米 MiMo 语音 | MiMo-V2.5-TTS 系列合成（9 官方预置音色、自然语言风格指令、文本描述定制音色 voicedesign）+ mimo-v2.5-asr 同步转写（mp3/wav ≤7.5MB，中英自动识别，输出纯文本） | OpenAI 兼容 chat/completions（固定入口，Bearer API Key） |
| 智谱平台语音 | glm-tts 合成（官方 7 音色 + 复刻音色、语速/音量调节、wav）+ glm-asr-2512 短音频转写（wav/mp3 ≤25MB/30 秒、热词与上下文、输出纯文本）+ 音色复刻/删除（接口对接，前端暂未展示） | REST（固定入口 open.bigmodel.cn，Bearer API Key） |

## 特性

- **双形态**：`voxbox tts/asr/podcast/separate/translate/minutes` 命令行直用（`tts`/`asr` 均支持 `--engine volcengine|qianwen|xiaomi|zhipu` 四引擎，`separate --engine mvsep|gsgc|zhuanhuanmao|mediakit` 四引擎切换；脚本/agent 友好，`--json` 机器可读输出）；`voxbox serve` 启动 Web 控制台。
- **单二进制**：前端产物 go:embed 内嵌，goroutine 任务池 + SQLite 状态，零外部依赖部署；纯 Go sqlite 驱动，可交叉编译（`make dist`）。
- **可扩展**：Provider 抽象层，新平台/新工具以「实现接口 + 注册」接入，前端表单与 CLI 由参数 schema 驱动。
- **Agent 可调用**：`voxbox skill install` 把 skill 装到本机 agent 目录（WorkBuddy / Claude Code / CodeBuddy），agent 即可按 CLI 调用全部能力——**skill 已 go:embed 进二进制，只分发可执行文件也自带**；`voxbox mcp` 提供 stdio MCP server（15 个工具，见 [docs/mcp.md](docs/mcp.md)），Claude Code 等 MCP 客户端可直连。

## Web 控制台

界面按「深空信号站」的调性设计：午夜蓝面板 + 电光青信号色、刻印微标签、等宽数字读数，暗色默认且亮色完整适配（可跟随系统）。

- **语音合成三通道**：同步 / 流式 / 长文本以 Tab 切换，每通道附计费说明（按场景与费用选择）
- **计费测算**：火山语音刊例价快照（2026-09-13），三通道同量对比 + ASR/播客/机器翻译/语音妙记估算器，以账单为准
- **工作台**：真实运行统计（任务数/成功率/累计耗时）+ 最近任务
- **试听**：自研波形播放器 + 全局播放条，历史页与各工具页的产物可直接播放、下载
- **工具联动**：人声分离页的人声轨可「送 ASR 识别」——产物直接作为识别输入，无需公网 URL
- **设置页云端/本地两 Tab**：云端凭证按厂商卡片独立保存与连通性测试（火山语音 / 千问平台 / MediaKit / MVSep），本地能力与存储位置归入本地 Tab

## 快速开始

```bash
# 构建前端 + 编译单二进制（前端产物 go:embed 内嵌）
make all

# 配置火山引擎语音凭证
./bin/voxbox config set volc.speech.app_id <APP ID>
./bin/voxbox config set volc.speech.access_token <Token>

# 配置千问平台凭证（qwen3-tts 合成 / qwen3-asr 转写，与火山凭证相互独立）
./bin/voxbox config set qianwen.api_key <API Key>

# 配置小米 MiMo 凭证（MiMo-V2.5-TTS 合成，与火山/千问凭证相互独立）
./bin/voxbox config set xiaomi.api_key <API Key>

# 配置智谱开放平台凭证（glm-tts 合成 / glm-asr 短音频转写，与火山/千问/小米凭证相互独立）
./bin/voxbox config set zhipu.api_key <API Key>

# CLI 合成（机器可读输出，退出码 0 成功）
./bin/voxbox tts "你好，voxbox" --out /tmp/hello.mp3 --json

# 长文本异步合成（≤10 万字，seed-tts-2.0；--timestamps 额外产出 SRT 字幕）
./bin/voxbox tts-long --file book.txt --timestamps --out /tmp/audiobook.mp3 --json

# 流式合成（低延迟；20 语种、8 方言、语音指令 --context-text、字级字幕 --subtitle）
./bin/voxbox tts-stream "用粤语说一段开场白" --explicit-dialect yue --subtitle --out /tmp/intro.mp3 --json

# 一句话识别（本地文件同步秒级，缺省版本；--srt 默认产出 SRT 字幕）
./bin/voxbox asr /tmp/recording.mp3 --out /tmp/transcript.txt --json

# 录音文件识别（URL；--version standard|idle|flash；本地文件配 --version 经对象存储中转，未配置存储时 standard 降级一句话）
./bin/voxbox asr --url "https://example.com/talk.mp3"                            --json   # 标准版，异步转写
./bin/voxbox asr --url "https://example.com/talk.mp3" --version flash --json   # 极速版，同步秒级返回
./bin/voxbox asr --url "https://example.com/talk.mp3" --version idle  --json   # 闲时版，低价、24h 内完成
./bin/voxbox asr --file /tmp/talk.m4a --version flash --json   # 本地文件走极速版（需配置对象存储）
./bin/voxbox asr --file /tmp/talk.wav --version standard --json   # 本地文件走标准版（需配置对象存储）

# 千问引擎（tts/asr --engine qianwen；音色缺省 Cherry，asr 仅录音文件转写、不支持 --version）
./bin/voxbox tts "你好，千问" --engine qianwen --voice Cherry --out /tmp/qwen.mp3 --json
./bin/voxbox asr --engine qianwen --url "https://example.com/talk.mp3" --out /tmp/transcript.txt --json

# 小米引擎（tts --engine xiaomi；音色缺省 mimo_default，--mimo-model voicedesign 时 --instructions 为音色描述）
./bin/voxbox tts "你好，小米" --engine xiaomi --voice 冰糖 --out /tmp/mimo.mp3 --json
./bin/voxbox asr /tmp/rec.mp3 --engine xiaomi --out /tmp/xiaomi.txt --json   # 小米同步转写（mp3/wav ≤7.5MB，本地直读无需对象存储）
./bin/voxbox tts "你好，智谱" --engine zhipu --voice tongtong --out /tmp/zhipu.wav --json   # 智谱合成（wav，官方/复刻音色，语速 --speed-ratio 音量 --volume-ratio）
./bin/voxbox asr /tmp/short.mp3 --engine zhipu --out /tmp/zhipu.txt --json   # 智谱短音频转写（wav/mp3 ≤25MB/30 秒，--hotwords 热词）

# 主题一键生成双人播客（--speakers 必填：两个音色 ID 逗号分隔，用 voxbox voices list 查询，支持 --scene/--lang 筛选）
./bin/voxbox podcast "用五分钟聊聊本地大模型" \
  --speakers zh_female_cancan_mars_bigtts,zh_male_dayixiansheng_v2_saturn_bigtts \
  --out /tmp/podcast.mp3 --json
# 播客输入四选一：位置参数文本 / --file 长文本文件 / --url 网页 / --script 对话稿 JSON（互斥）
# 音频格式 --format mp3|ogg_opus|pcm|aac，开头音乐 --head-music

# 对象存储中转（推荐火山 TOS：闲时/极速识别、人声分离、妙记的本地文件自动转存取签名 URL，默认 3 天生命周期）
./bin/voxbox config set storage.provider tos
./bin/voxbox config set storage.endpoint tos-cn-beijing.volces.com   # 与桶地域一致
./bin/voxbox config set storage.region cn-beijing
./bin/voxbox config set storage.bucket <bucket>
./bin/voxbox config set storage.access_key <AK>
./bin/voxbox config set storage.secret_key <SK>
./bin/voxbox config set storage.lifecycle_days 3   # Web 设置页可一键写入桶生命周期规则

# 人声背景音分离（四场景 audio/music 双轨、drama/narrate 三轨；需独立的 MediaKit API Key）
./bin/voxbox config set volc.mediakit.api_key <MediaKit API Key>
./bin/voxbox separate "https://example.com/media.mp4" --scene audio --format mp3 --json
./bin/voxbox separate --file /tmp/media.mp4 --scene audio --json   # 本地文件（需配置对象存储）
# 输入二选一：公网 URL 或 --file；输出格式 --format aac|mp3|wav|m4a|flac，产物音轨落盘 --out-dir 指定目录

# MVSep 音乐源分离（--engine mvsep；120+ 算法按分组选择，免费账号每天 50 次，本地文件直传无需对象存储）
./bin/voxbox config set mvsep.api_token <MVSep API Token>   # mvsep.com 注册后在「全页 API」页获取
./bin/voxbox separate --engine mvsep --sep-type 23 --format 0 /tmp/song.mp3 --json
# --sep-type 为算法 render_id（GET /api/mvsep/algorithms 或 Web 分离页查询；23=MDX B 人声/伴奏）
# 附加选项 --add-opt1/2/3 随算法而定；--base-url 可切区域线路（hk/de/de2.mvsep.com）

# 格式工厂云端分离（--engine gsgc；免费匿名无需凭证，直传站点 TOS，产物自动转标准 MP3）
./bin/voxbox separate --engine gsgc --stems both /tmp/song.mp3 --json

# 转换猫线路（--engine zhuanhuanmao；与格式工厂同后端的镜像站点，参数同 gsgc）
./bin/voxbox separate --engine zhuanhuanmao --stems both /tmp/song.mp3 --json

# 机器翻译（32 语种互译，--from 缺省自动检测；--terms 直传术语「原词=译词」，也可 --table-id/--table-name 用术语表）
./bin/voxbox translate "火山引擎是字节跳动旗下的企业级智能技术服务平台" --to en \
  --terms "火山引擎=Volcengine" --out /tmp/translated.txt --json

# 语音妙记（音视频转结构化纪要，--features 附加功能至少一项，分钟级异步；URL 或 --file 本地文件）
./bin/voxbox minutes "https://example.com/meeting.mp4" --features summary,todo,chapter \
  --speakers 0 --out-dir /tmp/minutes --json

# 启动 Web 控制台（默认端口取配置 server.port，可用 --port 覆盖）
./bin/voxbox serve --port 8081
# 浏览器打开 http://127.0.0.1:8081 → 各工具页交互、播放、查看历史

# 让本机 AI Agent 认领 voxbox 能力（skill 已内嵌，无需源码/网络）
./bin/voxbox skill install            # 自动探测 ~/.workbuddy|.claude|.codebuddy/skills
./bin/voxbox skill install --dir ~/.claude/skills
./bin/voxbox skill list               # 查看内嵌文件
```

未配置凭证时执行 `tts` / `asr` / `podcast` / `translate` / `minutes`（语音凭证；仅播客必须 APP ID + Access Token）、`separate`（MediaKit API Key）或 `separate --engine mvsep`（MVSep API Token，三套凭证相互独立）以退出码 4 结束，stderr 提示 `config set` 命令。

> 各火山能力需在控制台开通对应服务：语音三件套开「豆包语音」、机器翻译开 `volc.speech.mt`、语音妙记开 `volc.lark.minutes`、人声分离开 AI MediaKit；开通后数分钟内生效。

## 构建与发布

```bash
make all     # 构建前端 + 单二进制到 bin/voxbox（版本号取自 git describe）
make test    # Go 测试全量
make web     # 仅构建前端并同步到 embed 目录
make skills  # 仅同步 agent skill 到 embed 目录（skills/ 为唯一源）
make dist    # 交叉编译五个平台（darwin/linux × amd64/arm64 + windows/amd64）打包到 dist/
```

`make dist` 依赖无 CGO 的纯 Go sqlite 驱动，因此无需交叉编译工具链即可产出各平台可执行文件。darwin 产物已含 ad-hoc 签名（Go 交叉编译 darwin/arm64 时自动签，release workflow 另在 macOS runner 上显式 `codesign` 加固），但未做 Apple 公证。

**macOS 安装说明**（从 Release 下载 zip 解压后）：

```bash
# 浏览器下载的文件带 quarantine 隔离属性，未公证的二进制会被 Gatekeeper 拦
# （提示「Apple could not verify …」）。移除隔离属性即可运行：
xattr -d com.apple.quarantine ./voxbox

# 也可在「系统设置 → 隐私与安全性」中对该文件点「仍要打开」。
# 用 curl/wget 直接下载的文件不带隔离属性，解压即可运行：
curl -LO <release 里的 zip 地址> && unzip voxbox-*-darwin-arm64.zip && ./voxbox-*/voxbox --version
```

## 技术栈

Go（gin / gorm / cobra / resty / viper / gorilla/websocket）· React 19 + TypeScript + Vite + Tailwind CSS v4 · SQLite · Lucide 图标 · Fira Sans / Fira Code（自托管，离线可用）

## 文档

- [UI 设计规范](design-system/voxbox/MASTER.md)：色彩/字体/间距/组件规格（Web 界面实现依据）
- [agent skill](skills/voxbox/SKILL.md)：CLI 完整参考（[references/cli.md](skills/voxbox/references/cli.md)），供 agent 与脚本调用（改完执行 `make skills` 同步到 embed 目录）
- [JSON 输出契约](docs/json-contract.md)：`--json` / MCP 输出的字段与稳定性承诺
- [MCP Server](docs/mcp.md)：`voxbox mcp` 接入配置与工具清单

## License

Private.
