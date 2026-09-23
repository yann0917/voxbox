# voxbox MCP Server

voxbox 以 Model Context Protocol 把全部语音能力直接暴露给 AI Agent——与 CLI、Web 控制台并列的第三个消费方。两种传输，同一工具集、同一 JSON 契约：

- **stdio**：`voxbox mcp`，客户端拉起子进程（本地客户端首选）；
- **HTTP**：`voxbox serve` 内嵌 Streamable HTTP 端点 `/api/mcp`（适合无法拉起子进程的客户端）。工具入参与 CLI 旗标同词表（snake_case），输出与 `--json` 同一契约（见 [json-contract.md](json-contract.md)）。

## 接入配置

凭证复用 `~/.voxbox/config.yaml`，无需为 MCP 单独配置。

**Claude Code**（项目根 `.mcp.json` 或全局配置）：

```json
{
  "mcpServers": {
    "voxbox": {
      "command": "/absolute/path/to/voxbox",
      "args": ["mcp"]
    }
  }
}
```

其他 MCP 客户端：标准 stdio 握手，`command` 指向 voxbox 二进制、参数 `mcp` 即可。

**HTTP 传输鉴权**（远程部署）：`/api/mcp` 只认 `Authorization: Bearer tbx_*` API token
（Web 设置页生成/重置，明文仅签发响应显示一次；stdio 同机信任无需 token）：

```json
{
  "mcpServers": {
    "voxbox": {
      "url": "https://your-host/api/mcp",
      "headers": { "Authorization": "Bearer tbx_xxxxxxxx" }
    }
  }
}
```

无/错 token 返回 401（JSON-RPC 错误体）。部署步骤见 [deploy/README.md](../deploy/README.md)。

## 工具清单（15 个）

| 工具 | 用途 | 必填参数 | 耗时提示 |
|---|---|---|---|
| `voxbox_tts` | 短文本语音合成（同步秒级） | `text` | 秒级 |
| `voxbox_tts_long` | 长文本合成 ≤10 万字，`timestamps` 出 SRT | `text` | 分钟级 |
| `voxbox_tts_stream` | 流式低延迟，20 语种 8 方言，`context_text` 语音指令、`subtitle` 字级字幕 | `text` | 秒级 |
| `voxbox_asr` | 语音识别：`path`（本地文件）或 `url`（+`version: idle/flash`）二选一 | — | 秒~分钟 |
| `voxbox_podcast` | 双人播客：`text`/`url`/`script` 三选一 | `speakers`（恰好 2 个音色 ID） | 分钟级 |
| `voxbox_separate` | 人声/伴奏分离四引擎：`engine=mvsep`（免费，**支持 `file` 本地文件直传**）/ `engine=gsgc`（格式工厂线路，免费无凭证，`stems=both/vocals/instrumental`，产物自动转码 MP3 128k）/ `engine=zhuanhuanmao`（转换猫线路，同后端镜像，参数同 gsgc）/ `engine=mediakit`（火山计费，需 URL 或对象存储） | `url` 或 `file` 二选一 | 分钟级 |
| `voxbox_translate` | 32 语种翻译，`terms` 固定术语译法 | `text`, `to` | 秒级 |
| `voxbox_minutes` | 音视频 URL → 结构化纪要（总结/待办/章节/翻译） | `url`, `features` | 分钟级 |
| `voxbox_voices` | 音色查询（场景/语种/性别/关键词筛选） | — | 即时 |
| `voxbox_audio_edit` | **音频剪辑八合一**（本地 ffmpeg）：`op=trim` 切割（保留/挖除选区）/`merge` 合并（可交叉淡化，`files` 传其余文件）/`pitch` 变调变速（半音+速度倍率双轴）/`eq` 10 段均衡/`volume` 音量与响度归一化/`fade` 淡入淡出/`reverse` 倒放 | `op`, `file` | 秒~十秒 |
| `voxbox_audio_analyze` | **调与 BPM 查询**（本地 DSP）：调（key）、音阶、Camelot 编码、BPM 与备选节奏 | `file` | 秒级 |
| `voxbox_audio_mix` | **垫音混音**：分离产物的伴奏+人声双轨混音，`vocal_gain` 人声增益（默认 -16 垫音档，-99 纯伴奏）、`vocal_env` 分段垫音包络 JSON、`vocal_highpass` 低切、`master_loudness` 响度归一（0=关），`format` mp3|wav | `music`, `vocal` | 秒~十秒 |
| `voxbox_audio_hook` | **副歌候选检测**（本地 DSP，零额度）：按能量与起伏定位最像副歌的 top-N 选区（start/end 秒、score 评分 0-1），**返回候选不产音频**，供切片选区参考；`duration` 目标时长秒（5-60，默认 30）、`count` 候选数（1-5，默认 3） | `file` | 秒级 |
| `voxbox_audio_clip` | **选区切片导出**：选区加淡入淡出与响度归一导出成片，铃声 `format=m4r`（iPhone 直接可用）、短视频用 mp3；`fade_in`/`fade_out` 默认 1/0.5 秒（0=关）、`loudness` 响度归一默认 -14 LUFS（0=关），选区可先用 `voxbox_audio_hook` 取候选 | `file`, `start`, `end` | 秒~十秒 |
| `voxbox_audio_duck` | **口播闪避**：说话时人声自动压低 BGM（`bgm` 背景音乐、`vocal` 人声两轨绝对路径，files 顺序 `audio`=BGM、`audio2`=人声，序不可颠倒），单滑杆 `depth` 闪避深度默认 -12 dB（0=不压直通，域 -40~0，实际压深随语音密度过冲，约再加深 4~8dB）、`bgm_gain` 基线增益默认 -6、`loudness` 响度归一默认 -14 LUFS（0=关），`format` mp3|wav | `bgm`, `vocal` | 秒~十秒 |

### MVSep 分离的免费档约束

`voxbox_separate` 的 `engine` 缺省为 `mvsep`，`sep_type` 缺省为 `48`
（MelBand Roformer，vocals + instrumental 两轨）。

> `engine` 是**使用者侧的引擎别名**，注册表里的 provider 名是 `mvsep` / `volcengine` / `gsgc`：
> 传 `mediakit` 或 `volcengine` 都指向火山 AI MediaKit 那条链路（工具内部统一归一化，两者等价）；
> 传 `cloudsep` / `pcgeshi` / `geshi` 都指向 `gsgc`（格式工厂线路）；`zhuanhuanmao`（别名 `convertmao`）自 2026-09-19 起是独立的转换猫线路，不再归一为 `gsgc`。

#### gsgc 引擎（格式工厂云端直连）

`engine=gsgc` 走格式工厂在线版（z.pcgeshi.com）的云端任务接口
（线路与模型编号已硬编码）。免费、无需凭证与对象存储，
`stems=both`（双轨，默认）/ `vocals` / `instrumental`，站点产物为 **WAV**，保存前自动转码标准 **MP3 128k**（需服务器 ffmpeg，缺失时保留 WAV 并在 summary.warnings 提示）。
上游为站点私有接口（非官方公开 API），存在限流或改版风险，定位为**免费备用通道**；
分离不走 sep-cache（同曲重跑会再次请求上游）。

`engine=zhuanhuanmao` 是**转换猫线路**（www.zhuanhuanmao.com）：与格式工厂同一后端的镜像站点，
免费无需凭证、参数与 `gsgc` 完全一致；区别仅在站点入口与分离参数形态（转换猫 stem 为字符串
`instrumental_vocals` 且不带 model，格式工厂为 stem 数组 + `model:103`，各按自家前端形态提交）。
两线路互为免费备用：一路限流/改版时切另一路。

算法有 `price_coefficient` 系数，**系数 > 1 需付费会员**，免费账号提交会收到
`HTTP 400: Seperation type is unavailable until you purchase premium membership`。
`26`（Ensemble，系数 2）与 `28`（五轨 Ensemble，系数 4）都属此类；免费优先用 `48` 或其它系数 1 的算法。
算法全表见 Web 分离页或 `GET /api/mvsep/algorithms`。


## HTTP 端点（serve 内嵌）

`voxbox serve` 启动时同时在 **`http://127.0.0.1:<port>/api/mcp`** 暴露 Streamable HTTP 端点（与 Web 控制台同进程、同端口、共享同一任务引擎与数据目录）。客户端配置示例：

```json
{
  "mcpServers": {
    "voxbox": { "url": "http://127.0.0.1:8081/api/mcp" }
  }
}
```

注意：

- 默认仅监听 `127.0.0.1`，与 `/api` 其余端点同一安全姿态（无独立鉴权）——**不要把该端口暴露到公网**，MCP 工具可触发计费云 API；
- 工具调用与 stdio 模式同样**串行排队**执行；
- `voxbox mcp`（stdio）与 HTTP 端点可同时使用，互不影响。

## 行为约定

- **串行执行**：工具调用在服务端排队串行处理（SQLite 单写者约束）。妙记/播客这类分钟级任务会阻塞后续调用——agent 侧应避免并发发起多个长任务。
- **同步等待**：所有工具同步等到任务终态才返回；没有后台任务句柄，取消 = 中断整个调用。
- **产物路径**：`artifacts[].path` 恒为绝对路径；入参 `out` / `out_dir` 可重定向产物位置（等价 CLI `--out`）。
- **计费**：TTS/长文本/流式按字符、ASR 按时长、妙记按小时、播客按字符、分离按次数计费，工具描述内已标注；`voxbox_voices` 免费无调用次数限制。
- **错误**：参数缺失/互斥在调用前返回错误消息（中文），凭证缺失或上游未开通同样以错误文本返回——agent 可据此向用户索取密钥或建议开通。

## 本地验证

```bash
# 握手 + 工具列表（echo 管道即最小客户端）
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | voxbox mcp
```
