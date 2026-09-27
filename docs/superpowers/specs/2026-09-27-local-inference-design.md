# 本地语音推理对接(audio.cpp TTS + sherpa-onnx ASR)· 设计文档

日期:2026-09-27
状态:已与用户确认设计方向,待实施
前置:2026-09-27-local-model-hub-design.md(一期模型管理,已落地)

## 1. 背景与目标

一期(模型管理)已交付:内置静态目录 + 魔搭直链下载(进度/暂停/续传/删除),模型文件躺在 `<dataDir>/models/` 但没有任何代码能执行它们。二期把「已安装的模型」变成可用的推理能力:工具页选择本地引擎 → 任务本地执行 → 产物走现有链路。

目标:

1. 引擎二进制(audio.cpp、sherpa-onnx)作为新的可下载资产接入现有下载管理器(按需下载,不打包进安装包)。
2. 目录条目换成运行时兼容格式:ASR 用 SenseVoice int8,TTS 用 Qwen3-TTS GGUF(克隆 + 预置音色);移除一期占位的 pytorch 条目。
3. 新增本地推理 provider(`local.tts` / `local.asr`),TTSPage / ASRPage 各加「本地」引擎页签,未安装时引导去设置页。

### 已确认的设计决策

- 运行时路线:**audio.cpp(TTS)+ sherpa-onnx(ASR)**,均为预编译二进制子进程调用,不引 Python、不破 `CGO_ENABLED=0` 约束。(用户选定;Python sidecar 原版大模型路线维持非目标。)
- 引擎分发:**按需下载**,引擎作为 `kind: "engine"` 条目进现有下载管理器,设置页模型表新增引擎行;安装包体积不变。(用户选定。)
- TTS 音色:**克隆(Base)+ 预置(CustomVoice)两套 GGUF 都进目录**,用户下载哪个用哪个;另加 0.6B-Base 低配变体。(用户未答此问,按推荐执行,可改。)
- TTS 执行:**常驻 `audiocpp_server`**——2.5GB GGUF 每次冷启不可接受,证据驱动(调研实测 + yovoice 同款模式)。ASR 执行:**一次性 `sherpa-onnx-offline` 子进程**(RTF≈0.03,秒级,无守护进程必要)。
- 本地 provider 工具**始终注册**(注册表不动态增删);未安装时 Run 返回直述错误,前端按 `/api/models` 安装态做门控与引导。

### 非目标(本次不做)

- Python sidecar / R2T2 / Qwen3-TTS pytorch 原版(GPU 路线维持搁置)。
- 流式合成(SSE `/v1/audio/speech/live`)、实时流式 ASR。
- kokoro / vits 等 sherpa-onnx TTS 备选条目(目录加条目即可扩展,不进本期)。
- Windows arm64 引擎、CUDA/Vulkan 后端(只用 cpu/metal)。
- 本地 ASR 的 srt 产物(可由 token 级时间戳后补)、说话人分离。
- CLI(`voxbox`)新子命令;引擎/模型的 CLI 管理面。

## 2. 调研结论(本机实证,写死进目录的数字都来自这里)

### 2.1 sherpa-onnx(ASR)

- 预编译包:GitHub releases tag `v1.13.8`,资产 `sherpa-onnx-v1.13.8-{osx-arm64|osx-x64|win-x64|linux-x64|linux-aarch64}-shared.tar.bz2`,19.4-26.9MB;Windows 选 `-MT-`(静态 CRT,免装 VC Redist)。解压自包含(`bin/` 31 个可执行 + `lib/` onnxruntime,rpath `@loader_path/../lib`),**解压即跑零依赖**。
- ASR 调用:`bin/sherpa-onnx-offline --sense-voice-model=<model.int8.onnx> --tokens=<tokens.txt> --sense-voice-use-itn=true [--sense-voice-language=auto|zh|en|ja|ko|yue] <wav>`;stdout 一行 JSON `{"lang":..., "text":..., "timestamps":[...], ...}`;输入任意采样率自动重采样。
- 模型:`https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17.tar.bz2`(155.5MB,内含 `model.int8.onnx`、`tokens.txt`)。该版本 HF 无镜像,只 GitHub。
- 无 HTTP server(只有 WebSocket),CLI 每任务调用是既定集成方式。

### 2.2 audio.cpp(TTS)

- 预编译包:GitHub releases tag `v0.8.2`,资产 `audio-v0.8.2-bin-macos-arm64-metal.tar.gz`(27.2MB)/ `audio-v0.8.2-bin-macos-x64-metal.tar.gz` / `audio-v0.8.2-bin-windows-x64-cpu-portable.zip`(24MB)/ linux cpu 包。macOS 包只编 `cpu,metal` 两后端。
- **server 模式**(采用):`audiocpp_server --config server.json --no-ui`;server.json 含 `host/port/backend/device/threads/lazy_load/idle_unload_ms/max_loaded_models/models[]`;`GET /health` 就绪轮询;`POST /v1/tasks/run` body `{"model":"<id>","request":{"text":...,"voice_ref":...,"options":{...}}}` → 响应 `{"audio":"<base64 WAV>"}`。
- 关键参数:克隆 `voice_ref` + `options.reference_text`(缺省走 `x_vector_only_mode:true` 纯音色克隆);预置 `options.speaker`(Vivian/Serena/Uncle_Fu/Dylan/Eric/Ryan/Aiden/Ono_Anna/Sohee)+ 可选 `options.instruct` 风格指令;输出 24kHz pcm16 WAV;macOS 必须**显式** `--backend metal`(server 默认 cuda,不传必挂)。
- GGUF 模型(ModelScope `HereIsMark/audio.cpp-gguf/resolve/master/<path>` 直链实测 200):`Qwen3-TTS-12Hz-1.7B-Base-GGUF/qwen3-tts-12hz-1.7b-base-q8_0_v2.gguf`(2570.3MB)、`Qwen3-TTS-12Hz-1.7B-CustomVoice-GGUF/…q8_0.gguf`(2686.5MB)、`Qwen3-TTS-12Hz-0.6B-Base-GGUF/…q8_0.gguf`(1899.0MB)。
- 参考音频规范:ffmpeg 转 `-ac 1 -ar 24000 -c:a pcm_s16le`,限 60 秒 / 20MB。

### 2.3 仓库对接点(现状)

- 子进程先例:`internal/provider/audiotool/runner.go`——`exec.CommandContext`(取消即 kill)、stderr 末行报错、`.part` 先写后 rename、进度映射 [8,95];产物由 Engine 统一落库(`internal/task/engine.go:249-266`,工具只回 `TaskOutput.Artifacts`,Path 为 dataDir 相对路径)。
- 页面:TTSPage 引擎是顶层 Tabs(provider 名即页签值,面板硬编码提交 provider);ASRPage 引擎是 useState,按引擎分支 params;未配置凭证有「去设置页」引导卡模式可复用。
- 任务提交:`POST /api/tasks {provider, tool, params, file_ids|artifact_input(s)}`;`file_ids` 第一个键固定 `audio`(本地 TTS 克隆的参考音频走它)。
- ffmpeg:audiotool 已依赖,参考音频转码复用其探测/报错模式(`lookPath` 可替换变量 + 带安装指引的错误)。

## 3. 目录扩展(catalog.json + 下载管线)

### 3.1 引擎条目(kind: "engine")

```json
{
  "id": "audiocpp",
  "kind": "engine",
  "name": "audio.cpp 引擎",
  "version": "v0.8.2",
  "summary": "Qwen3-TTS GGUF 推理运行时",
  "binaries": ["audiocpp_server", "audiocpp_cli"],
  "assets": {
    "darwin/arm64":  { "url": "https://github.com/0xShug0/audio.cpp/releases/download/v0.8.2/audio-v0.8.2-bin-macos-arm64-metal.tar.gz", "size_bytes": 28500000, "sha256": "<实施期实抓>" },
    "darwin/amd64":  { "...": "macos-x64-metal" },
    "windows/amd64": { "url": "...audio-v0.8.2-bin-windows-x64-cpu-portable.zip", "size_bytes": 25200000, "sha256": "..." },
    "linux/amd64":   { "url": "...ubuntu-cpu 包..." },
    "linux/arm64":   { "url": "...ubuntu 包..." }
  }
}
```

- `sherpa-onnx` 引擎条目同理(`binaries: ["sherpa-onnx-offline"]`,五平台 `-shared` 包)。平台资产按 `runtime.GOOS/GOARCH` 选择;无对应平台的条目在该平台不展示(`assets` 缺键)。
- 安装布局:`<dataDir>/engines/<id>/`(解包后:二进制 + 运行库保持包内相对结构,manifest.json 记录 version/binaries/installedAt)。二进制 `chmod 0755`。
- **manifest 复用一期"磁盘即真相"语义**:引擎行状态同样走 idle/downloading/verifying/installed/failed;`installed` = 引擎目录 manifest 存在。

### 3.2 模型条目改版

- 移除三条 pytorch 条目(paraformer/confucius4-r2t2/qwen3-tts-1.7b-base 原版),替换为:

| kind | id | 资产 | 体积 | 依赖引擎 | 说明 |
|---|---|---|---|---|---|
| asr | `sensevoice-int8` | GitHub asr-models tag 的 tar.bz2 | 155.5MB | sherpa-onnx | 归档,解包出 model.int8.onnx + tokens.txt |
| tts | `qwen3-tts-base-q8` | ModelScope GGUF 单文件 | 2.57GB | audiocpp | 克隆 |
| tts | `qwen3-tts-customvoice-q8` | ModelScope GGUF 单文件 | 2.69GB | audiocpp | 9 预置音色 + instruct |
| tts | `qwen3-tts-base-0.6b-q8` | ModelScope GGUF 单文件 | 1.9GB | audiocpp | 低配克隆 |

- 条目新增字段:`archive`(归档格式:`tar.bz2|tar.gz|zip`,缺省=裸单文件)、`requires_engine`(引擎 id)、`extract_files`(归档条目:解包保留的白名单文件,如 `model.int8.onnx,tokens.txt`)。
- **下载管线扩展**(localmodel.Manager):
  - 归档条目:下载到 `<dataDir>/models/<id>/<原文件名>.part` → 解包(白名单提取 + zip-slip 防护 + 8GB 解包上限)→ 校验 → manifest。解包器:tar.bz2(archive/tar + compress/bzip2)、tar.gz(gzip+tar)、zip(archive/zip),全部标准库。
  - 引擎条目:下载到 `<dataDir>/engines/…` 同管线,解包后 walk 定位 `binaries` 白名单中的可执行文件,chmod 0755,manifest 记录 binary 绝对路径。
  - 大小校验沿用 Content-Length/Content-Range 既有逻辑;引擎归档 sha256 **必填**(实施期实抓),GGUF 模型 sha256 可空(与一期一致)。
  - 引擎与模型的删除/暂停/续传语义不变;删除引擎前若有对应模型已安装,允许删除但工具 Run 时会报「引擎未安装」(不做级联删除)。

## 4. TTS 运行时管理器(`internal/localruntime/audiocpp.go`)

- **生命周期**:进程级单例(挂 Service)。首次 TTS 任务且引擎+模型已安装时拉起;`audiocpp_server --config <runtime>/server.json --no-ui`;server.json 写随机 127.0.0.1 端口、`backend`(darwin=metal,其余=cpu)、`lazy_load:true`、`idle_unload_ms:300000`、`max_loaded_models:1`、`models[]`(每个已安装的 qwen3 GGUF 一条,`family:qwen3_tts`)。轮询 `/health`(250ms 间隔,上限 120s)至就绪。
- **执行**:`POST /v1/tasks/run`,`model` = server.json 中的模型索引;`request.text` = 待合成文本,`request.voice_ref` = 转码后的参考 wav 绝对路径,`options` 白名单透传(reference_text/x_vector_only_mode/speaker/instruct/temperature 等)。响应 base64 WAV → 写 `.part` → 校验非空 → rename 落产物。
- **稳健性**:健康检查失败的进程 kill 重启(一次/任务);Service.Close 时回收子进程;server 意外退出由下次任务拉起时发现(每次 run 前轻量 `/health` 探测,不养看门狗)。
- **进度**:合成无中间进度,任务进度报告为阶段式(探测/转码 5-10%、等待模型加载 10-40%、合成中 40-90%、落盘 95-100%)。
- **取消**:任务 ctx 取消 → 中止 HTTP 请求;server 子进程不打断(lazy_load + 空闲卸载自愈)。

## 5. ASR 执行器(`internal/localruntime/sherpa.go`)

- 每任务:`exec.CommandContext(ctx, engineBin, "--sense-voice-model=<dir>/model.int8.onnx", "--tokens=<dir>/tokens.txt", "--sense-voice-use-itn=<bool>", "--sense-voice-language=<lang>", <wav 绝对路径>)`;stdout 解析一行 JSON(`text` 产物文本、`timestamps` 留 Meta);stderr 末行进错误(audiotool 模式)。
- 输入 wav 不转码(任意采样率可接受);产物:`asr/<uuid>/<srcBase>_local.txt`(dataDir 相对),Meta 记 lang/emotion/engine 版本。
- 任务进度:阶段式(启动 5%、识别中 10-90%、落盘 100%),无流式百分比。

## 6. 本地 provider(`internal/provider/local/`)

- `ProviderCard`:`Name:"local"`,`Title:"本地推理"`,`Kind: KindLocal`,无凭证字段,Order 85(audiotool 90 之前)。`RegisterAll(reg, dataDir, models *localmodel.Manager, rt *localruntime.Manager)` 启动注册一次。
- 工具 `local.tts`:
  - ParamSpecs:`model`(enum,运行时校验已安装)、`mode`(enum: clone|preset,默认 clone)、`ref_text`(string,可选)、`speaker`(enum,9 音色,preset 模式必填)、`instruct`(string,可选)、`language`(string,可选)。
  - Run:引擎/模型安装校验(未装报「本地引擎未安装:请到设置页下载」类直述错误)→ clone 模式取 `in.Files["audio"]` 为参考音频(缺失报错),ffmpeg 转码(60s/20MB 限制)→ 调 audiocpp → 产物 `tts/<uuid>/<srcBase或text前缀>_local.wav`。
  - clone/preset 校验:clone 必须用 Base 条目;speaker 必须用 CustomVoice 条目(模型与模式不匹配报直述错误)。
- 工具 `local.asr`:ParamSpecs:`language`(enum auto/zh/en/ja/ko/yue,默认 auto)、`itn`(bool,默认 true)。Run 校验 sensevoice 已安装 + sherpa 引擎已安装 → 调执行器 → txt 产物。
- 两工具在 ParamSpecs 的 Group 里带「本地推理」分组名,前端参数面板照常渲染。
- 错误映射:未安装类错误返回普通 error(经 failErr → CodeTaskFailed=3,文案自解释),不进 ErrNoCred 体系。

## 7. HTTP API 变化

- `GET /api/models` 不变(引擎行自动出现,`requirements.device` 对引擎条目填后端说明如 "Metal/CPU")。
- `/api/voices?provider=local` 新增分支:返回 CustomVoice 9 音色(静态表,含 speaker 名与说明);前端本地 TTS 面板用它渲染音色选择。
- 新增 `GET /api/local/ready?tool=tts|asr`:返回 `{ready:bool, missing:[{type:"engine"|"model", id, name}]}`——前端「本地」页签的引导卡数据源(一次查询判定依赖链,避免前端拼装)。
- 其余无新端点;任务提交走既有 `/api/tasks`。

## 8. 前端

- **设置页模型表**:引擎行(类别列"引擎",大小列为解包后磁盘占用≈资产大小,许可证列沿用);无新组件,ModelItem 结构多 `requires_engine` 字段做依赖提示(模型行未安装 + 依赖引擎未安装 → 下载按钮旁 hint「需先下载 XX 引擎」)。
- **TTSPage**:引擎 Tabs 加「本地」;`LocalTTSPanel`:ready 引导卡(未装:列缺失项 + 「去设置页」链接;已装:正常表单)→ 模型选择(已安装的 qwen3 条目)/ 模式切换(克隆:参考音频上传 + 可选参考转写;预置:音色选择 `/api/voices?provider=local` + instruct)→ 提交 `{provider:"local", tool:"tts", params, file_ids?}`。
- **ASRPage**:引擎 Tabs 加「本地」;`LocalASRPanel`:ready 引导卡 → 语言/ITN → 上传/产物输入,提交 `{provider:"local", tool:"asr", params, file_ids|artifact_input}`。
- 引擎页签的显隐:始终显示(与云引擎一致,未安装引导即可),不做动态增删 Tabs。

## 9. 测试

- localmodel:归档解包(tar.bz2/tar.gz/zip 各一,httptest 起假归档资产)、白名单提取、zip-slip 拒绝、引擎二进制定位与 chmod、平台资产选择(GOOS/GOARCH 映射)、requires_engine 字段校验。
- localruntime:server.json 生成与 backend 选择(darwin=metal)、假 server(httptest)的就绪轮询/RPC/base64 WAV 落盘、崩溃后的重启路径、sherpa 命令行拼装与 stdout JSON 解析(含畸形容错)、取消传播。
- provider/local:参数校验(mode/model 匹配、clone 缺参考音频、未安装错误文案)、ffmpeg 转码限制(60s/20MB,mock ffmpeg 或跳过)、产物 TaskOutput 形状。
- routes:`/api/local/ready` 分支、`/api/voices?provider=local`。
- 前端:tsc + build + 手测(本地引擎下载→模型下载→TTSPage 本地合成出产物→ASRPage 本地识别出 txt;未安装引导卡)。
- 端到端:开发机(macOS,metal)真跑一轮合成与识别作冒烟。

## 10. 实施边界(预计触及)

- 新增:`internal/localruntime/`(audiocpp.go、sherpa.go、manager.go)、`internal/provider/local/`(card.go、tts.go、asr.go)、前端 `LocalTTSPanel.tsx`/`LocalASRPanel.tsx`。
- 修改:`internal/localmodel/`(catalog schema 引擎/归档扩展 + 解包器)、`internal/service/`(装配 local provider 与 runtime manager)、`internal/server/routes.go`(ready/voices 分支)、`web/src/pages/{TTSPage,ASRPage,SettingsPage 相关}`、`web/src/lib/models.ts`(类型)。
- 不动:任务引擎、产物链路、config.yaml、desktop 壳、CLI。
