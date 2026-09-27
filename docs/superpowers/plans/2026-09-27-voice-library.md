# 音色库 + IndexTTS2.5 + 情感控制 · 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 参考音频录/传一次入库、TTS 面板下拉选用;接入 IndexTTS2.5(克隆型,audiocpp 原生家族);IndexTTS 情感文本控制透传。

**Architecture:** 音色库延续「磁盘即真相」(`<dataDir>/voices/<id>/` wav+meta,无 DB 表),新包 `internal/voicelib` + 5 端点;catalog 加 `family` 字段使 audiocpp server 能注册 index_tts2 家族;local.tts 的参考音频改为 voice_id(库)/临时上传二选一,family 感知校验;前端 LocalTTSPanel 重构为音色下拉 + 面板内录音 + 家族感知 UI。

**Tech Stack:** Go 1.26 标准库 + ffmpeg(转码,已有依赖)、react-query v5、`web/src/ui`、`lib/recorder.ts`(录音复用)。

**Spec 依据:** `2026-09-27-local-inference-design.md` 的扩展(本轮为 bounded 增量,设计已在会话中与用户确认;spike 实证:index-tts2_5-q8_0.gguf 在 audiocpp v0.8.2 CLI 与 server 两条通道克隆合成均通过,macOS Metal)。

## Global Constraints

- 纯 Go 无 CGO;每任务收尾 `go test ./...`(或涉包)+ gofmt/vet 干净;前端 `tsc --noEmit && npm run build` 全绿。
- 音色规格与既有 convertRef 同源:24kHz 单声道 pcm_s16le,上限 60 秒(超长裁剪)、原始文件 20MB、成品 <1 秒拒收。
- 磁盘即真相:音色库无 DB 表;删除/改名即盘面操作。
- catalog 是唯一清单维护面;`family` 为 tts 条目必填(现值 `qwen3_tts`,新增 `index_tts2`);asr/engine 条目不设。
- 中文文案直述;错误指明去哪操作;MASTER §5 UI 约束不变。
- spike 实证数据(写死进 catalog):GGUF `index-tts2_5-q8_0.gguf` 3,502,955,328 字节,sha256 `5e827b2072042e4a1b21ccf24a5cb4f71cb1011403067a0a9b039311d8b38628`,直链 `https://modelscope.cn/api/v1/models/HereIsMark/audio.cpp-gguf/repo?Revision=master&FilePath=IndexTTS2.5-GGUF/index-tts2_5-q8_0.gguf`,family `index_tts2`。
- 情感控制范围:仅透传 `emotion_text`/`emotion_alpha`(index_tts2 家族);qwen3 不涉及。

---

### Task 1: 音色库包与端点(`internal/voicelib` + routes)

**Files:**
- Create: `internal/voicelib/voicelib.go`、`internal/voicelib/voicelib_test.go`
- Modify: `internal/service/service.go`(构造与持有)、`internal/server/routes.go`(5 端点)、Create `internal/server/voicelib_test.go`

**Interfaces:**
- Produces(Task 3/4 消费):`package voicelib`;`type Library struct`;`func New(dataDir string) *Library`;`type Voice struct { ID, Name string; DurationMS int64; CreatedAt time.Time }`;`(l *Library) List() []Voice`;`(l *Library) Add(ctx context.Context, name, srcPath string) (Voice, error)`(ffmpeg 转码 24k mono pcm16 + `-t 60` 裁剪 + ffprobe 校验成品 ≥1s,失败清理);`(l *Library) Path(id string) (string, error)`(绝对路径;不存在报错);`(l *Library) Rename(id, name string) error`;`(l *Library) Delete(id string) error`;`var ErrNotFound = errors.New("音色不存在")`。Service 访问器 `VoiceLibrary() *voicelib.Library`。路由:`GET /api/voice-library`、`POST /api/voice-library`(multipart: name + file)、`GET /api/voice-library/:id/stream`(音频流,复用产物 stream 模式)、`PATCH /api/voice-library/:id`(`{name}`)、`DELETE /api/voice-library/:id`;错误映射 ErrNotFound→6,其余→2。

- [ ] **Step 1: 写失败测试(voicelib_test.go)**

用例(ffmpeg 真依赖,mock 探测用可替换 `lookPath` 变量,缺失用例跳过——CI 无 ffmpeg 时 `t.Skip`):①Add 成功:用 ffmpeg 生成 2 秒正弦 wav 作输入(`exec.Command("ffmpeg","-f","lavfi","-i","sine=frequency=440:duration=2",...)`,lookPath 命中),断言返回 Voice(name/duration≈2000±500ms)、目录内 voice.wav+meta.json 存在、再 List() 可见;②成品 <1 秒拒收(输入 duration=0.3);③Add 后 Rename 生效(meta 更新);④Delete 后 List 不再包含、Path 报 ErrNotFound;⑤Path 未知 id → ErrNotFound;⑥name 空 → 参数错误;⑦lookPath 缺失 → 明确 ffmpeg 错误。

- [ ] **Step 2: 跑红 → 实现 → 跑绿**

实现要点:目录布局 `<dataDir>/voices/<uuid8>/`(voice.wav + meta.json);Add 流程:lookPath ffmpeg → `ffmpeg -hide_banner -nostdin -y -v error -i src -t 60 -ac 1 -ar 24000 -c:a pcm_s16le <dir>/voice.wav`(CommandContext 绑 ctx)→ ffprobe 拿时长(照 audiotool probeAudio 模式)→ <1000ms 删目录报「参考音频太短(需 1-60 秒)」→ 写 meta.json → 返回;List 读目录聚合(按 CreatedAt 倒序);Rename/Delete 直接盘面操作(Delete 先删目录,ErrNotFound 对齐)。

- [ ] **Step 3: 装配与端点**

service.go:字段 `voices *voicelib.Library`、`newWithRoot` 构造 `voicelib.New(dataDir)`、访问器;routes.go api 组追加 5 行;handler 放新文件 `internal/server/voicelib.go`——POST 用 `c.FormFile("file")` 存临时文件 → `s.svc.VoiceLibrary().Add(ctx, name, tmp)`(defer 删临时);stream 用 `c.Header("Accept-Ranges","bytes")` + `http.ServeFile`(产物 stream 同款);PATCH body `{name}`。server 集成测试:newTestServer → POST 入库(真 ffmpeg,无 ffmpeg 则 Skip)→ list 断言 → stream 200 → PATCH → DELETE → 404 语义。

- [ ] **Step 4: 全绿 + 提交**

`go test ./internal/voicelib/ ./internal/server/ ./internal/service/ -race` + gofmt/vet;`git commit -m "feat(voicelib,server): 音色库——入库/列表/预览/改名/删除"`。

---

### Task 2: catalog family 字段 + IndexTTS2.5 条目

**Files:**
- Modify: `internal/localmodel/catalog.go`、`internal/localmodel/catalog.json`(追加条目)
- Test: `internal/localmodel/catalog_test.go`

**Interfaces:**
- Produces: `Entry.Family string`(json `family`);校验:kind=tts 必填且 ∈{qwen3_tts, index_tts2},kind∈{asr,engine} 必须为空;Task 3 消费 `v.Entry.Family`。

- [ ] **Step 1: 失败测试**:tts 缺 family 拒;family 非法值拒;asr 带 family 拒;两条既有 qwen3 条目补 `family:"qwen3_tts"` 后嵌入目录自检继续过。
- [ ] **Step 2: 实现 + catalog.json**:既有三条 qwen3 tts 条目加 `"family": "qwen3_tts"`;追加:

```json
  {
    "id": "index-tts2_5-q8", "kind": "tts", "family": "index_tts2",
    "name": "IndexTTS 2.5 克隆(q8)",
    "summary": "bilibili IndexTTS 2.5 音色克隆,支持情感文本控制与中英日西阿多语(GGUF 量化)",
    "size_bytes": 3502955328,
    "requirements": { "device": "metal" },
    "license": "bilibili Model Use License",
    "license_url": "https://modelscope.cn/models/HereIsMark/audio.cpp-gguf",
    "requires_engine": "audiocpp",
    "files": ["index-tts2_5-q8_0.gguf"],
    "file_urls": { "index-tts2_5-q8_0.gguf": "https://modelscope.cn/api/v1/models/HereIsMark/audio.cpp-gguf/repo?Revision=master&FilePath=IndexTTS2.5-GGUF/index-tts2_5-q8_0.gguf" },
    "sha256": { "index-tts2_5-q8_0.gguf": "5e827b2072042e4a1b21ccf24a5cb4f71cb1011403067a0a9b039311d8b38628" }
  }
```

(sha256 为本机对整文件实测;file_urls 直链已实测下载成功。)
- [ ] **Step 3:** `go test ./internal/localmodel/ -race` + gofmt/vet;`git commit -m "feat(localmodel): family 字段与 IndexTTS2.5 条目"`。

---

### Task 3: 运行时 family + 情感透传 + local.tts 升级

**Files:**
- Modify: `internal/localruntime/audiocpp.go`(server.json family)、`internal/provider/local/tts.go`(family 校验/voice_id/emotion)
- Test: `internal/localruntime/audiocpp_test.go`、`internal/provider/local/local_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Library.Path/Add`、Task 2 的 `Entry.Family`(经 `List()` 的 `v.Entry.Family`)。
- Produces: `SynthRequest` 加 `EmotionText string; EmotionAlpha float64`(audiocpp.go 的 options 组装:EmotionText 非空 → `emotion_text`+`use_emotion_text:true`;EmotionAlpha>0 且 <1 → `emotion_alpha`);tts 工具参数:`voice_id`(string,克隆模式与临时上传二选一,库优先)、`emotion_text`、`emotion_alpha`(0-1 float);ParamSpecs 的 language 改为 string 类型(前端按家族渲染下拉)。

- [ ] **Step 1: 失败测试**:①audiocpp:server.json 的 models[].family 取自条目(播种一个 family=index_tts2 的已安装 tts 条目 → 拉起 fake server → 断言落盘 server.json 的 family;qwen3 条目回归);②Synthesize options:EmotionText/Alpha 进 options 的 JSON 断言;③local.tts:family=index_tts2 条目 + preset 模式 → 直述错误「IndexTTS 为克隆模型,不支持预置音色」;voice_id 入库后克隆成功(seam 注入 synthesizeFn,断言 RefWav 指向库内 wav);voice_id 与 file_ids 同传 → voice_id 优先;emotion_text/alpha 透传到 SynthRequest;qwen3 条目行为回归(base/customvoice 匹配校验只在 qwen3 生效)。
- [ ] **Step 2: 实现**:audiocpp.go startLocked 的 serverModels 组装取 `v.Entry.Family`(空回落 qwen3_tts 防御);options 组装加 emotion;local.tts Run:clone 模式先查 `params.voice_id` → `s.svc 不可达`——**voice 库经 Tool 构造注入**:`newTTSTool(dataDir, models, tts, voices *voicelib.Library)`(register.go/AllTools 签名同步,Task 1 已有 Service 持有);voice_id 存在 → `voices.Path(voice_id)` 为 RefWav;否则走 file_ids+convertRef 旧路;emotion 参数仅 family=index_tts2 时透传(其他家族忽略);language 校验:qwen3 ∈{Chinese,English,Japanese,Korean},index ∈{auto,zh,en,ja,es,ar},非法直述;ParamSpecs 的 language 改 ParamString + placeholder「由所选模型决定」,mode/其他不变。
- [ ] **Step 3:** 相关包 `-race` 全绿 + gofmt/vet;`git commit -m "feat(localruntime,local): server family 注册/情感透传/音色库克隆"`。

---

### Task 4: 前端——音色库选择器 + 家族感知面板

**Files:**
- Create: `web/src/lib/voiceLibrary.ts`
- Modify: `web/src/lib/models.ts`(ModelItem.family)、`web/src/pages/tts/LocalTTSPanel.tsx`(重构克隆区)

**Interfaces:**
- Produces: `useVoiceLibrary()`(list)、`uploadVoice(name, file)`、`renameVoice(id, name)`、`deleteVoice(id)`、`voiceStreamUrl(id)`(`${apiBase}/api/voice-library/${id}/stream`)。

- [ ] **Step 1: 实现 voiceLibrary.ts**(react-query,["voice-library"] 缓存;mutations 成功后 invalidate)
- [ ] **Step 2: LocalTTSPanel 重构**:

- `useModels()` 取选中模型的 `family`;克隆区改造:
  - **音色下拉**(库列表,`name(duration)` 格式)+ 每项预览:选中音色下方 `WavePlayer`(`src=voiceStreamUrl(id)`,先读 `web/src/ui/WavePlayer.tsx` 实际 props 适配)或 `<audio controls>` 兜底;
  - **录入新音色**:面板内「录音」按钮(复用 `lib/recorder.ts` 的 RecordingSession——先读 ASRPage 对它的用法照抄启停/时长显示;录完弹 name 输入 → uploadVoice)+「上传文件」按钮(选文件 + name → uploadVoice;前端预检 20MB);
  - **管理**:选中项旁 改名(prompt 或小输入)/删除(ConfirmDialog)按钮;
  - 提交参数:克隆模式带 `voice_id`,不再走临时 file_ids 上传(后端保留兼容,前端不再用);
  - **家族感知**:family=index_tts2 → 模式锁克隆(隐藏预置选项)、语言下拉换 `自动/中文/英文/日语/西班牙语/阿拉伯语`(value: auto/zh/en/ja/es/ar)、显示「情感文本」输入(选填)+「情感强度」数字输入(0-1,步进 0.1,默认留空=1.0);family=qwen3_tts → 现状不变;
  - IndexTTS2.5 模型行出现在模型下拉(已安装 tts 条目自然入选)。

- [ ] **Step 3:** `tsc --noEmit && npm run build`;同步 webdist(`cp -r web/dist/* cmd/voxbox/webdist/`);`git commit -m "feat(web): 音色库选择器与家族感知本地合成面板"`。

---

### Task 5: 全量验证 + 浏览器真机手测

**Files:** 无新代码(验证;问题走修复)。

- [ ] **Step 1 自动化门禁**:`go test ./...`、`-race` 四重点包、gofmt/vet、`tsc+build`。
- [ ] **Step 2 浏览器手测**(playwright MCP,真实服务 + throwaway home;后端冒烟先备资产:下载 audiocpp 引擎 + index-tts2_5-q8(3.5GB)+ 任一 qwen3 模型):
  1. 设置页下载 index-tts2_5-q8 → installed;sha 校验通过(下载管线已有)。
  2. TTS 本地页签:模型下拉含 IndexTTS 2.5;选中 → 模式锁克隆 + 语言词表切换 + 情感字段出现。
  3. 音色库:面板内「录音」录一段(`say` 铺底不可用——录音是真麦克风,playwright 环境 grant fake media stream?playwright 可用 `--use-fake-ui-for-media-stream`/fake device;若不可行则以上传代替录音验证,录音项列为手测留给用户)→ 入库后出现在下拉;上传第二个音色 → 下拉两项;预览播放;改名;删除。
  4. **IndexTTS 克隆合成**:选 IndexTTS 模型 + 选库内音色 + 情感文本填「你吓死我了!」→ 合成 → 产物可播放;再合成一次带 `emotion_alpha=0.5` → 成功。
  5. **qwen3 回归**:选 qwen3 模型 → 预置/克隆照旧;情感字段不出现。
  6. ASR 本地页签回归(不应受影响);console 零新增错误。
- [ ] **Step 3 收尾提交**(修复单提);webdist 同步。

---

## Self-Review 结论(已执行)

1. **覆盖**:音色库(Task 1/3/4)、IndexTTS2.5(Task 2/3/4)、情感控制(Task 3/4)、验证(Task 5)——与确认的三项范围一一对应。
2. **占位扫描**:无 TBD;WavePlayer/Recorder 的调用形态注明「先读实际签名适配」并给了裁决规则(改调用不改共享件)。
3. **类型一致性**:`Library` 方法集在 Task 1 定义、Task 3 消费;`Entry.Family` Task 2 定义、Task 3(server.json/工具校验)与 Task 4(前端)消费;`voice_id/emotion_text/emotion_alpha` 参数名前后端一致;`SynthRequest` 新字段与 audiocpp options 组装一致。
