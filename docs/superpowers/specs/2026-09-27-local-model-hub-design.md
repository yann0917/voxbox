# 设置页「本地环境」本地语音模型管理 · 设计文档

日期:2026-09-27
状态:已与用户确认设计方向,待实施

## 1. 背景与目标

设置页「本地环境」Tab 目前只有 audiotool / gsgc 只读能力卡、存储位置、通知、API Token,与"本地语音模型"无关;后端没有任何模型下载、模型文件管理、本地推理基础设施(全仓库无 modelscope / onnx / 本地模型相关代码)。用户预期该分区管理的是从魔搭社区(ModelScope)下载的语音模型(ASR / TTS 等)。

目标:

1. 「本地环境」Tab 重构为以**本地语音模型**为主体的管理页:内置静态模型目录 + 魔搭直链下载,支持进度、暂停、断点续传、删除。
2. 为二期本地推理预留接入点:目录条目声明设备要求与文件清单,推理引擎(形态二期另定)将来按"已安装模型"判断可用性。

参考范式:yovoice(重管理——内置目录 + 专用模型列表 + 进度/暂停/删除)为管理页蓝本;KnowClip(零管理)不采纳。

### 已确认的设计决策

- **本次只做模型管理,不做本地推理**。你点名的两个大模型(Qwen3-TTS-12Hz-1.7B-Base、Confucius4-R2T2)均无 CPU/ONNX 路径,只能 GPU+Python 跑;推理运行时形态(sherpa-onnx 独立二进制 / Python sidecar)二期单独决策。
- 模型目录:**内置静态目录**(`catalog.json`,`go:embed`),不做用户自定义添加、不做魔搭全站浏览。调整清单 = 改一个文件 + 发版。
- 下载源:**只魔搭直链**(公开模型免 token),不引 HuggingFace / hf-mirror 切换。
- **独立子系统** `internal/localmodel`,不进 provider 元数据注册表(卡结构为"凭证字段+工具数"设计,装不下下载进度与操作),不挂任务系统(任务面向"输入→产物",模型是常驻磁盘资产,生命周期不同)。
- **磁盘即真相**:安装态 = 目录内 manifest,暂停态 = `.part` 文件;不新增 DB 表。服务重启靠扫盘恢复。
- 并发:**全局同时 1 个下载**,同模型内逐文件顺序;已有下载进行中时其他模型的"下载"请求被拒(前端相应禁用)。
- 增强项(已确认要做):桌面形态「打开模型目录」按钮(走 Go 端点,不引入 Tauri JS 桥);下载完成 Toast。

### 非目标(本次不做)

- 本地推理 / 推理运行时下载(Python sidecar、sherpa-onnx、vLLM 等)。
- 更改模型存储位置(固定 `<dataDir>/models/`)。
- 用户自定义模型 ID、魔搭搜索/浏览。
- CLI(`voxbox models`)与 MCP 面。
- 任务系统集成、下载速度显示、SSE/WebSocket 推送(轮询够用)。
- HuggingFace / 镜像下载源。

## 2. 现状摘要

- 设置页:`web/src/pages/SettingsPage.tsx`,`云端服务 / 本地环境` 两 Tab;本地 Tab 渲染 `GET /api/settings` 中 `kind === "local"` 的只读卡(audiotool、gsgc)+ 存储位置 + 通知 + API Token。
- 后端装配:`internal/service/cards.go` 的 `ProviderStates`;数据三层存储——凭证/存储/端口在 `~/.voxbox/config.yaml`,任务/产物在 `<dataDir>/voxbox.db`,通知开关在前端 localStorage。
- 服务形态:单二进制纯 Go(`CGO_ENABLED=0` 五平台交叉编译,Makefile 明示无 CGO 依赖);桌面版 Tauri 2 壳把窗口指向 `http://127.0.0.1:<port>`,**壳与 web 之间无 JS invoke 桥**,业务面全走 HTTP。
- 错误码体系:`internal/server/apierr.go`(业务码)与 `cmd/voxbox/output.go`(CLI 退出码)共用语义;本功能无凭证,不触碰 ErrNoCred 两处映射。
- 前端组件库(`web/src/ui/`):`Card`、`SignalDot`(纯色状态点,支持 pulse)、`ProgressBar`(2px 细条 + running 流光)、`ConfirmDialog`、`Toast`、`IconButton`、`EmptyState`;MASTER.md 禁止手写卡片/按钮 class。前端无测试基建,走 typecheck/build + 手工验收。

## 3. 模型目录(`internal/localmodel/catalog.go` + `catalog.json`)

`//go:embed catalog.json`,清单是唯一需要人工维护的文件。条目 schema:

```json
{
  "id": "qwen3-tts-1.7b-base",
  "repo": "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
  "revision": "master",
  "name": "Qwen3-TTS 1.7B Base",
  "kind": "tts",
  "summary": "音色克隆语音合成,3 秒参考音频即可复刻音色",
  "size_bytes": 4544230475,
  "files": [
    "model.safetensors",
    "speech_tokenizer/model.safetensors",
    "config.json",
    "generation_config.json",
    "preprocessor_config.json",
    "vocab.json",
    "merges.txt"
  ],
  "requirements": { "device": "cuda", "vram_gb": 8 },
  "license": "Apache-2.0",
  "license_url": "https://modelscope.cn/models/Qwen/Qwen3-TTS-12Hz-1.7B-Base",
  "sha256": {}
}
```

约定:

- `id`:稳定 slug,磁盘目录名与 API 路径参数;`kind`:`asr | tts`(开放枚举,将来可扩);同族量化变体 = 同 repo 不同 `id` 的多条目(如 paraformer 常规版/量化版),不引入变体嵌套结构。
- `size_bytes` **仅用于展示**("约 4.3 GB");真实大小以下载时响应 `Content-Length` 为准。`sha256` 可选映射(path→hash):声明了才校验,魔搭不强制提供,一期允许为空(预留字段)。
- `requirements.device`:`cuda | cpu`,前端据此渲染设备要求文案。
- 启动时对 embed JSON 做合法性校验(id 唯一、非空、path 无 `..`/绝对路径),不合法直接 panic(fail-fast,目录是随二进制发布的静态资产)。

### 首批候选条目(占位,以用户后续圈定为准;调整只改 catalog.json)

| kind | repo | 说明 | 设备 |
|---|---|---|---|
| asr | `iic/speech_paraformer-large-vad-punc_asr_nat-zh-cn-16k-common-vocab8404-pytorch` | ~1GB,用户点名参考 | CPU 可跑(ONNX 导出版另定) |
| asr | `netease-youdao/Confucius4-R2T2` | 4.1GB 真流式 ASR | 需 GPU(vLLM) |
| tts | `Qwen/Qwen3-TTS-12Hz-1.7B-Base` | 4.5GB 音色克隆 TTS | 需 GPU(PyTorch) |

## 4. 下载管理器(`internal/localmodel/manager.go`)

### 4.1 状态模型

每模型状态:`idle | downloading | verifying | installed | failed`,另配 `hasPartial bool`(.part 存在)。语义:

- `idle` + 无 .part = 未下载;`idle` + 有 .part = 可续传(前端文案"继续下载")。
- `downloading`:逐文件顺序下载,单文件走 HTTP Range 断点续传(`.part` 后缀,如 `model.safetensors.part`);进度为跨文件累计字节 `downloaded_bytes / total_bytes`。
- `verifying`:全部文件落地后核对各文件字节数与 `sha256`(若目录声明),通过后写 `<dataDir>/models/<id>/manifest.json`(记录 revision、files+size、completedAt),进入 `installed`。
- `failed`:携带错误消息(磁盘不足、网络失败、远端 404 等),可重试(重试即续传)。
- `stop`(暂停/取消):终止传输,保留 `.part`,回到 `idle`+hasPartial。

### 4.2 下载与恢复

- 直链模板:`https://modelscope.cn/api/v1/models/{repo}/repo?Revision={revision}&FilePath={urlencoded path}`(公开模型免 token,已实测)。文件清单来自内置目录,不运行时爬 repo。
- 服务启动扫盘恢复:`models/<id>/` 有完整 manifest → `installed`;有 `.part` → `idle`+hasPartial;否则 `idle`。manifest 损坏按未安装处理。
- 磁盘预检:启动下载前核对剩余空间 ≥ 剩余待下字节,不足直接 `failed`,错误文案直述还需多少 GB。
- Range 不被支持(远端返回 200 而非 206)时降级为整文件重下,`.part` 重头写。
- 进度不落库、不节流:内存即时更新,API 轮询即节流。

### 4.3 并发与安全

- 全局单飞行:同一时刻至多 1 个模型在下载;`download` 请求撞上正在进行的下载直接拒绝(业务错误,文案"已有模型在下载")。
- 删除:仅接受目录内存在的 `id`(未知 id → NotFound);`downloading/verifying` 状态禁止删除;删除 = 整目录移除(含 manifest 与 .part)。
- 路径安全:写入路径 = `dataDir/models/<id>/<clean(file)>`,file 来自内置目录白名单,拒绝 `..` 与绝对路径;`<id>` 必须能反查到目录条目。

## 5. HTTP API(`internal/server/routes.go` + `internal/service` 转发)

`Service` 持有 `localmodel.Manager`(构造时注入 `dataDir`),方法薄转发,routes 只做参数解析与错误映射。

| 端点 | 说明 |
|---|---|
| `GET /api/models` | 目录全量 + 实时状态:`[{id, name, kind, summary, size_bytes, requirements, license, license_url, status, hasPartial, downloaded_bytes, total_bytes, error}]` |
| `POST /api/models/:id/download` | 开始/续传;全局冲突返回业务错误 |
| `POST /api/models/:id/stop` | 停止并保留 .part |
| `DELETE /api/models/:id` | 删除已安装/残留文件 |
| `POST /api/models/open-dir` | 打开 `<dataDir>/models`(见 §7) |

前端数据流:`useModels` hook——页面挂载拉一次;存在 `downloading/verifying` 时 1s 轮询,否则不轮询。下载完成 Toast 由前端捕捉 `downloading/verifying → installed` 状态边沿触发。

## 6. 设置页 UI(SettingsPage「本地环境」Tab 重构)

Tab 内布局顺序:**模型 → 本地能力卡(audiotool / gsgc,现状不动)→ 存储位置 → 通知 → API Token**。

模型区按 `kind` 分组(组头刻印微标签"语音识别 / 语音合成"),每模型一张 `Card`:

- **头部**:名称 + 纯色状态点(SignalDot+文字,遵守"不得只靠颜色"):未下载 muted / 下载中 accent+pulse / 校验中 accent / 已安装 meter / 失败 danger。右侧操作:未下载→`下载`(primary sm;有 .part→`继续下载`);下载中→`暂停`;已安装→`删除`(IconButton,danger 语义);失败→`重试`。全局有下载进行中时,其他卡的下载/继续按钮禁用。
- **正文**:一句简介(取目录 `summary`,不编造文案);meta 行(mono 小字):总大小 + 设备要求(`requirements.device=cuda` → "需 NVIDIA GPU · ≥{vram_gb}GB 显存";`cpu` → "CPU 可用")+ 许可证名(内嵌链接指向 `license_url` 的魔搭模型页,遵守域名内嵌链接偏好)。
- **下载中**:`ProgressBar(active)` + mono"已收 / 总字节(百分比)";`verifying` 显示校验中文案。
- **删除**:`ConfirmDialog` 二次确认,标题含模型名,说明释放空间大小,危险按钮在右。
- **失败**:行内错误文本 + Toast;错误文案直述原因(磁盘不足/网络中断/模型不存在或已下架,后者附魔搭页链接)。
- **完成 Toast**:"{name} 已就绪"。

模型区组头右侧(桌面形态 `me.desktop` 时)显示「打开模型目录」IconButton,调 `POST /api/models/open-dir`;web 形态隐藏该按钮(与 API Token 卡的显隐逻辑相反方向,均以形态判断)。

## 7. 桌面「打开模型目录」:Go 端点而非 Tauri 插件

现架构壳与 web 无 JS 桥,为一个小按钮引入 `tauri-plugin-opener` + npm 包 + capability 配置不成比例。改为:Go 端点 `POST /api/models/open-dir`,sidecar 进程执行平台打开命令——darwin `open <dir>`,windows `explorer <dir>`,linux `xdg-open <dir>`。路径固定为 `<dataDir>/models`,无用户输入拼接,无注入面;后端不做形态 gate,显隐由前端 `me.desktop` 控制。打开失败(如 linux 无 xdg-open)返回错误,前端 Toast。

## 8. 错误与边界

- 磁盘空间不足:启动前预检,failed 态 + 直述还需空间。
- 网络中断:单文件 Range 续传;重试不丢进度;多次失败停留 failed 态等用户操作。
- 服务重启:扫盘恢复(§4.2),桌面版 sidecar 随壳重启不丢 `.part`。
- 魔搭 404/私有化/下架:failed 态,错误文案区分"模型不存在或已下架",附模型页链接。
- 远端文件更新:以 `Content-Length` 为准校验;若 .part 大于远端当前大小(远端变更),废弃 .part 重下。
- 删除与路径:仅目录内 id 可删;非法 id → NotFound;下载中禁止删除。

## 9. 测试

- Go 单测(`internal/localmodel`):状态机迁移;Range 续传与降级(httptest 伪造魔搭端点,断言 206/200 路径);字节数不符;sha256 声明校验;磁盘预检;删除路径/状态校验;启动扫盘恢复;全局单飞行拒绝。catalog 嵌入校验(id 唯一、路径安全、JSON 合法)。
- routes 集成测试:五个端点的成功/冲突/NotFound 分支。
- 前端:无测试基建,走 `npm run build` + typecheck + 手测清单——下载/暂停/继续/完成 Toast/删除确认/失败重试/重启恢复/桌面打开目录按钮显隐/全局单下载禁用。

## 10. 实施边界(预计触及)

- 新增:`internal/localmodel/`(catalog.go、catalog.json、manager.go、manager_test.go 等)。
- 修改:`internal/service/`(构造与持有 Manager)、`internal/server/routes.go`(端点)、`web/src/lib/api.ts` + `types.ts`(端点与类型)、`web/src/pages/SettingsPage.tsx`(本地 Tab 重构,模型区建议抽独立组件如 `LocalModelsSection`)。
- 不动:provider 注册表、任务系统、config.yaml 结构、desktop 壳。
