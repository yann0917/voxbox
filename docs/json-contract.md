# voxbox JSON 输出契约（v1）

适用范围：CLI `--json` stdout、`voxbox mcp` 工具输出。二者共享同一执行核心，形状完全一致。
Web 控制台 REST 的任务对象（`GET /api/tasks`）携带同一份 `params` / `summary`，外层多一个统一包络 `{code, data, message}`（HTTP 恒 200，`code!==0` 为业务错误）。

## 稳定性承诺

- v1 已有字段**只增不改名不删除**；新增键不视为破坏性变更。
- `artifacts[].path` 恒为**绝对路径**（CLI `--out` 绝对路径重定向时亦然）。
- `artifacts[].url` 为**上游产物地址**（如火山分离各轨的 TOS 签名 URL），仅转存型产物写入、缺省不出现；
  有效期由上游决定（MediaKit 为 24 小时），本地 `path` 才是长期可用的那份。
- 退出码 / 业务码复用同一分级：`0` 成功、`2` 参数错误（请求未发往上游）、`3` 任务失败（上游报错）、`4` 凭证缺失/无效或资源未开通、`5` 内部错误、`6` 资源不存在（仅 Web）。

## 顶层对象

```json
{
  "task_id": "uuid",
  "provider": "volcengine",
  "tool": "tts",
  "status": "succeeded",
  "cost_ms": 1059,
  "artifacts": [
    {"kind": "audio", "path": "/abs/path.mp3", "format": "mp3", "size": 44640, "duration_ms": 2151},
    {"kind": "audio", "path": "/abs/vocals.mp3", "format": "mp3", "size": 9011200, "duration_ms": 329000,
     "url": "https://tos-cn-beijing.volces.com/...&X-Tos-Expires=86400"}
  ],
  "summary": { }
}
```

失败时 CLI 以非零退出码返回，错误原因打印在 stderr（中文）；Web 侧落在 `task.error`。

## artifacts[].kind 枚举

`audio`（音频产物）｜`transcript`（转写/译文文本）｜`subtitle`（SRT 字幕）｜`dialog`（播客对话稿 JSON）｜`translation`（妙记翻译文件）｜`minutes`（妙记功能 JSON：总结/待办/章节等）

## summary 键位（按工具）

各工具只写自己相关的键；下表为全部键位的权威清单。

| 工具 | summary 键 |
|---|---|
| tts | `char_count` `segment_num` |
| tts_long | `char_count` `req_text_length` `synthesized_chars` `sentence_count` `status` `task_id`（上游任务）`upstream_task_id` |
| tts_stream | `char_count` `billed_chars` `chunks` `duration_ms` |
| asr | `segments[]{text,start_ms,end_ms}` `duration_ms` `source`（file/url）`version` |
| podcast | `rounds` `rounds_done` `round_id` `speaker` `text`（最近轮对话）`duration_s` `audio_url_fallback` |
| separate | `scene` `tracks`（轨名数组）`duration_s` `task_id`（上游任务） |
| translate | `translation`（译文正文）`source_language` `target_language` `char_count` `terms_count` `prompt_tokens` `completion_tokens` `total_tokens`，自动检测时附 `detected_source_language` |
| minutes | `minutes_title` `summary_text` `translation_text` `features` `sentences` `speakers_count` `duration_ms` `upstream_task_id` `segments[]{text,start_ms,end_ms}`（text 带说话人前缀）`todos[]{content,executor[],start_time}` `chapters[]{title,summary,start_time,end_time}` |

## 消费方

- **CLI / 脚本**：`voxbox <tool> ... --json`，以退出码判断成败后直接解析 stdout。
- **MCP agent**：`voxbox_*` 工具输出即本契约（JSON 文本内容）。
- **Web 控制台**：任务列表/详情的 `task.params` / `task.summary` 与上表一致；`segments` 驱动音频-文稿同步回放。
