# 本地语音推理对接(前端)· 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把二期后端能力接到 UI——TTS/ASR 页各加「本地」引擎页签(未安装引导卡 + 表单 + 任务进度),设置页模型表补引擎依赖提示。

**Architecture:** 复用既有模式而非新造:引擎页签 = TTSPage/ASRPage 现有 Engine 枚举扩一项;引导卡 = 「未配置凭证→去设置页」同款;面板结构 = QianwenTTSPanel(WS 事件 + TTSShared 进度/产物件);数据源 = 后端已交付的 `GET /api/local/ready`、`/api/models`、`/api/voices?provider=local`。后端不改。

**Tech Stack:** React + @tanstack/react-query v5 + `web/src/ui` 组件库(MASTER §5 强制复用)。

**Spec:** `docs/superpowers/specs/2026-09-27-local-inference-design.md` §8(前端章节);后端计划 `2026-09-27-local-inference.md` 已落地。

## Global Constraints

- 前端无测试基建:每任务收尾 `cd web && npx tsc --noEmit && npm run build` 全绿;真机手测集中在最后一任务。
- MASTER §5:只复用 `web/src/ui` 组件,禁手写卡片/按钮 class;状态不得只靠颜色;域名内嵌链接;EmptyState 用于空态。
- 中文文案直述;不引新依赖。
- wire 契约(后端已定,前端照抄):`GET /api/local/ready?tool=tts|asr` → `{ready:bool, missing:[{type:"engine"|"model", id, name}]}`(**ready 时 missing 为 `[]`**);`GET /api/models` items 平铺字段含 `requires_engine`;`GET /api/voices?provider=local` → `{voices:[{id,name}]}`;任务提交 `{provider:"local", tool:"tts"|"asr", params, file_ids?}`。
- 提交参数(与后端 provider/local 严格对齐):tts = `text, model, mode(clone|preset), ref_text?, speaker?, instruct?, language`;asr = `language(auto|zh|en|ja|ko|yue), itn(bool)`。

---

### Task 1: models.ts 扩展——requires_engine / useLocalReady / useLocalVoices

**Files:**
- Modify: `web/src/lib/models.ts`

**Interfaces:**
- Consumes: `fetchJSON`。
- Produces(Task 2/3/4 消费):`ModelItem.requires_engine?: string`;`export interface LocalReady { ready: boolean; missing: { type: "engine" | "model"; id: string; name: string }[] }`;`export function useLocalReady(tool: "tts" | "asr")`(返回 useQuery 对象,data?: LocalReady);`export function useLocalVoices()`(data?: {id,name}[])。

- [ ] **Step 1: 实现(models.ts 追加)**

`ModelItem` 接口追加字段(与后端平铺 JSON 对齐):

```ts
  requires_engine?: string; // tts/asr 条目:依赖的引擎 id(engine 条目无此字段)
```

文件尾部追加:

```ts
export interface LocalReadyMissing {
  type: "engine" | "model";
  id: string;
  name: string;
}

export interface LocalReady {
  ready: boolean;
  missing: LocalReadyMissing[];
}

/** 本地推理就绪查询(引擎+模型依赖链一次判定);挂载即拉,下载完成后回到页面会自动刷新。 */
export function useLocalReady(tool: "tts" | "asr") {
  return useQuery({
    queryKey: ["local-ready", tool],
    queryFn: () => fetchJSON<LocalReady>(`/api/local/ready?tool=${tool}`),
  });
}

/** 本地预置音色(CustomVoice GGUF 的 9 个 speaker)。 */
export function useLocalVoices() {
  return useQuery({
    queryKey: ["voices", "local"],
    queryFn: () => fetchJSON<{ voices: { id: string; name: string }[] }>("/api/voices?provider=local"),
  });
}
```

- [ ] **Step 2: 类型检查 + 提交**

Run: `cd web && npx tsc --noEmit`
Expected: 零错误。

```bash
git add web/src/lib/models.ts
git commit -m "feat(web): 本地推理 API 层——requires_engine/ready/预置音色 hooks"
```

---

### Task 2: 设置页模型表——引擎依赖提示

**Files:**
- Modify: `web/src/pages/LocalModelsSection.tsx`(ModelRow)

**Interfaces:**
- Consumes: `ModelItem.requires_engine`、items 里同表的引擎行(可按 id 找到引擎条目的 status)。
- Produces: 无(纯展示)。

- [ ] **Step 1: 实现(ModelRow 模型格)**

`ModelRow` 组件需要能查同表其他条目——给 `ModelRow` 加一个 prop `engineInstalled: (id: string) => boolean`,父组件传入:

```tsx
// LocalModelsSection 内:
const engineInstalled = (id?: string) => {
  if (!id) return true; // 无依赖条目视作满足
  return items.some((m) => m.id === id && m.status === "installed");
};
```

`items.map((m) => <ModelRow ... engineInstalled={engineInstalled} />)`。

`ModelRow` 简介行下方追加提示(仅 tts/asr 条目、依赖引擎未安装、且自身未安装时显示;纯信息性——模型可以先下,只是不能用):

```tsx
        {!engineInstalled(m.requires_engine) && m.status !== "installed" && (
          <p className="mt-1 text-[11px] text-warn">
            需先下载引擎才能本地运行(模型可先行下载)
          </p>
        )}
```

(注:`requires_engine` 是引擎条目的 id,提示不重复渲染引擎名——引擎行就在同表上方/下方,状态点已可见。)

- [ ] **Step 2: 类型检查 + 构建 + 提交**

Run: `cd web && npx tsc --noEmit && npm run build`

```bash
git add web/src/pages/LocalModelsSection.tsx
git commit -m "feat(web): 设置页模型表——依赖引擎未安装的提示"
```

---

### Task 3: LocalTTSPanel + TTSPage「本地」页签

**Files:**
- Create: `web/src/pages/tts/LocalTTSPanel.tsx`
- Modify: `web/src/pages/TTSPage.tsx`(Engine 类型/ENGINE_TABS/渲染分支)

**Interfaces:**
- Consumes: `useLocalReady("tts")`、`useLocalVoices()`、`useModels()`(筛已安装 tts 模型)、`useTaskEvents`、`ProgressBody/AudioRow/Run`(tts/TTSShared)、`POST /api/uploads`。
- Produces: `export default function LocalTTSPanel()`(无 props)。

- [ ] **Step 1: 实现 LocalTTSPanel.tsx**

结构照 QianwenTTSPanel(两栏 grid、WS 事件、终态拉详情),本地差异:模型下拉来自 useModels 已安装条目;mode 切换克隆/预置;克隆需要参考音频文件(上传走 /api/uploads);参考转写 ref_text 可选。

```tsx
import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AudioLines, Play, RefreshCw, Upload } from "lucide-react";
import { fetchJSON } from "../../lib/api";
import { useLocalReady, useLocalVoices, useModels } from "../../lib/models";
import type { TaskDetail } from "../../lib/types";
import { useTaskEvents } from "../../lib/ws";
import { AudioRow, ProgressBody, type Run } from "./TTSShared";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Field,
  Select,
  StatusBadge,
  Textarea,
  useToast,
} from "../../ui";

const LANGS = [
  { value: "Chinese", label: "中文" },
  { value: "English", label: "英文" },
  { value: "Japanese", label: "日语" },
  { value: "Korean", label: "韩语" },
];

/** 本地语音合成面板:audio.cpp 引擎 + 已安装 Qwen3-TTS,克隆(参考音频)或预置音色。 */
export default function LocalTTSPanel() {
  const [text, setText] = useState("");
  const [model, setModel] = useState("");
  const [mode, setMode] = useState<"clone" | "preset">("clone");
  const [refFile, setRefFile] = useState<File | null>(null);
  const [refText, setRefText] = useState("");
  const [speaker, setSpeaker] = useState("");
  const [instruct, setInstruct] = useState("");
  const [language, setLanguage] = useState("Chinese");
  const [taskId, setTaskId] = useState<string | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [submitError, setSubmitError] = useState("");
  const { toast } = useToast();
  const qc = useQueryClient();
  const ev = useTaskEvents();

  const ready = useLocalReady("tts");
  const voices = useLocalVoices();
  const models = useModels();
  const installedModels = (models.data?.items ?? []).filter(
    (m) => m.kind === "tts" && m.status === "installed",
  );
  // 已安装模型唯一时自动选中;多模型时默认第一个
  useEffect(() => {
    if (!model && installedModels.length > 0) setModel(installedModels[0].id);
  }, [installedModels, model]);

  /* WS 事件驱动进度;终态拉详情(与 QianwenTTSPanel 同款) */
  useEffect(() => {
    if (!ev || !taskId || ev.task_id !== taskId) return;
    if (ev.type === "progress") {
      setRun({ status: "running", progress: ev.progress ?? 0, note: ev.note ?? "处理中" });
      return;
    }
    if (ev.type === "done" || ev.type === "error" || ev.type === "canceled") {
      fetchJSON<TaskDetail>(`/api/tasks/${taskId}`)
        .then((d) => {
          setRun({ status: d.task.status, progress: d.task.progress, note: d.task.progress_note, error: d.task.error });
          setDetail(d);
          if (d.task.status === "failed") {
            toast({ tone: "error", title: "合成失败", description: d.task.error || undefined });
          }
        })
        .catch((e: Error) => {
          setRun((r) => ({ status: "failed", progress: r?.progress ?? 0, note: "读取任务结果失败", error: e.message }));
        });
    }
  }, [ev, taskId, toast]);

  const submit = useMutation({
    mutationFn: async () => {
      const params: Record<string, unknown> = {
        text: text.trim(), model, mode, language,
      };
      if (mode === "clone" && refText.trim()) params.ref_text = refText.trim();
      if (mode === "preset") {
        params.speaker = speaker;
        if (instruct.trim()) params.instruct = instruct.trim();
      }
      let file_ids: string[] | undefined;
      if (mode === "clone" && refFile) {
        const fd = new FormData();
        fd.append("file", refFile);
        const up = await fetchJSON<{ file_id: string }>("/api/uploads", { method: "POST", body: fd, headers: {} });
        file_ids = [up.file_id];
      }
      return fetchJSON<{ task_id: string }>("/api/tasks", {
        method: "POST",
        body: JSON.stringify({ provider: "local", tool: "tts", params, file_ids }),
      });
    },
    onSuccess: (d) => {
      setTaskId(d.task_id);
      setRun({ status: "pending", progress: 0, note: "已提交" });
      setDetail(null);
      setSubmitError("");
      void qc.invalidateQueries({ queryKey: ["tasks"] });
    },
    onError: (e: Error) => {
      setSubmitError(e.message);
      toast({ tone: "error", title: "提交失败", description: e.message });
    },
  });

  // —— 未就绪引导卡 ——
  if (ready.isLoading) return null;
  if (ready.data && !ready.data.ready) {
    return (
      <Card>
        <CardBody className="space-y-2">
          <p className="text-sm text-fg-2">本地推理引擎或模型尚未安装:</p>
          <ul className="space-y-1 text-xs text-muted">
            {(ready.data.missing ?? []).map((m) => (
              <li key={`${m.type}-${m.id}`}>
                · {m.type === "engine" ? "引擎" : "模型"}:{m.name}
              </li>
            ))}
          </ul>
          <a href="/settings" className="text-xs text-accent hover:opacity-80">
            去设置页「本地环境」下载 →
          </a>
        </CardBody>
      </Card>
    );
  }

  const canSubmit = text.trim() !== "" && model !== "" && (mode === "preset" ? speaker !== "" : refFile !== null);
  const artifacts = detail?.artifacts ?? [];
  const audioArtifacts = artifacts.filter((a) => a.kind === "audio");

  return (
    <>
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        {/* 左:文本编辑区 + 结果 */}
        <Card className="min-w-0">
          <CardHeader title="合成文本" icon={<AudioLines size={15} strokeWidth={1.75} />} />
          <CardBody className="space-y-3">
            <Textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              rows={8}
              placeholder="输入要合成的文本…"
            />
            {taskId && run && <ProgressBody run={run} />}
            {audioArtifacts.map((a) => (
              <AudioRow key={a.id} artifact={a} title="本地合成产物" />
            ))}
            {submitError && <p className="text-xs text-danger">{submitError}</p>}
          </CardBody>
        </Card>
        {/* 右:参数面板 */}
        <Card>
          <CardHeader title="本地推理参数" />
          <CardBody className="space-y-4">
            <Field label="本地模型">
              {({ id, ...rest }) => (
                <Select id={id} value={model} onChange={(e) => setModel(e.target.value)} {...rest}>
                  {installedModels.map((m) => (
                    <option key={m.id} value={m.id}>{m.name}</option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="音色模式">
              {({ id, ...rest }) => (
                <Select id={id} value={mode} onChange={(e) => setMode(e.target.value as "clone" | "preset")} {...rest}>
                  <option value="clone">参考音频克隆</option>
                  <option value="preset">预置音色</option>
                </Select>
              )}
            </Field>
            {mode === "clone" ? (
              <>
                <Field label="参考音频" hint="3-60 秒清晰人声,自动转 24kHz 单声道">
                  {({ id, ...rest }) => (
                    <div className="flex items-center gap-2">
                      <Button
                        type="button"
                        size="sm"
                        variant="secondary"
                        icon={<Upload size={13} strokeWidth={1.75} />}
                        onClick={() => document.getElementById(id)?.click()}
                      >
                        {refFile ? refFile.name : "选择音频文件"}
                      </Button>
                      <input
                        id={id}
                        type="file"
                        accept="audio/*,.wav,.mp3,.m4a,.flac"
                        className="hidden"
                        onChange={(e) => setRefFile(e.target.files?.[0] ?? null)}
                        {...rest}
                      />
                    </div>
                  )}
                </Field>
                <Field label="参考音频转写" hint="选填;填了克隆更准,留空走纯音色克隆">
                  {({ id, ...rest }) => (
                    <Input id={id} value={refText} onChange={(e) => setRefText(e.target.value)} placeholder="参考音频实际说的内容" {...rest} />
                  )}
                </Field>
              </>
            ) : (
              <>
                <Field label="预置音色">
                  {({ id, ...rest }) => (
                    <Select id={id} value={speaker} onChange={(e) => setSpeaker(e.target.value)} {...rest}>
                      <option value="">选择音色…</option>
                      {(voices.data?.voices ?? []).map((v) => (
                        <option key={v.id} value={v.id}>{v.name}</option>
                      ))}
                    </Select>
                  )}
                </Field>
                <Field label="风格指令" hint="选填;自然语言描述语气,如 Very happy">
                  {({ id, ...rest }) => (
                    <Input id={id} value={instruct} onChange={(e) => setInstruct(e.target.value)} {...rest} />
                  )}
                </Field>
              </>
            )}
            <Field label="语言">
              {({ id, ...rest }) => (
                <Select id={id} value={language} onChange={(e) => setLanguage(e.target.value)} {...rest}>
                  {LANGS.map((l) => (
                    <option key={l.value} value={l.value}>{l.label}</option>
                  ))}
                </Select>
              )}
            </Field>
            <Button variant="primary" loading={submit.isPending} disabled={!canSubmit} onClick={() => submit.mutate()}>
              开始合成
            </Button>
          </CardBody>
        </Card>
      </div>
    </>
  );
}
```

(注:`ProgressBody`/`AudioRow`/`Run` 的实际 props 以 `web/src/pages/tts/TTSShared.tsx` 现有签名为准——实现者先读该文件再定调用形态;上面写法与 QianwenTTSPanel 的用法对齐,若签名有出入以 TTSShared 为准修正调用、不改 TTSShared。)

- [ ] **Step 2: TTSPage 接线**

`web/src/pages/TTSPage.tsx`:

1. `type Engine` 追加 `| "local"`;`ENGINE_TABS` 追加 `{ value: "local", label: "本地推理" }`。
2. `engine` 归一化(:42-46 的三元链)追加 `|| engineParam === "local"`。
3. 渲染链(`engine === "qianwen" ? … : engine === "xiaomi" ? … : engine === "zhipu" ? … : 火山`)在 zhipu 分支后插一个 `: engine === "local" ? (<LocalTTSPanel />) :` 火山兜底(import LocalTTSPanel)。

- [ ] **Step 3: 类型检查 + 构建 + 提交**

Run: `cd web && npx tsc --noEmit && npm run build`

```bash
git add web/src/pages/tts/LocalTTSPanel.tsx web/src/pages/TTSPage.tsx
git commit -m "feat(web): TTS 页「本地推理」页签——就绪引导卡/克隆与预置表单"
```

---

### Task 4: ASRPage 本地引擎

**Files:**
- Modify: `web/src/pages/ASRPage.tsx`

**Interfaces:**
- Consumes: `useLocalReady("asr")`、既有引擎页签/提交流(Engine/ENGINE_TABS/engineReady/changeEngine/submit)。
- Produces: 无(页内状态)。

- [ ] **Step 1: 实现(ASRPage 七处改动)**

1. `type Engine`(:68)追加 `| "local"`;`ENGINE_TABS`(:71)追加 `{ value: "local", label: "本地推理" }`。
2. `engineReady` 门(:216-224)追加 local 分支——注意 local 是异步查询,不能塞进同步三元链,单独取:

```tsx
  const localReady = useLocalReady("asr");
```

`engineReady` 改为:local 时 `localReady.data?.ready === true`(undefined = 加载中,同云端「保守态」走引导卡);其余引擎照旧。

3. `changeEngine`(:296-315)追加 local 分支(与 xiaomi/zhipu 同款:录音回落上传):

```tsx
    } else if (v === "local") {
      // 本地引擎只有本地上传/录音通道(URL 直下不支持;sherpa 只吃 wav 本地文件)
      setMode((m) => (m === "url" ? "upload" : m));
    } else if (version === "sentence") {
```

4. 扩展名白名单:文件头常量区(:50-107 附近)加

```tsx
/** 本地引擎(sherpa-onnx-offline)只吃 wav:任意采样率自动重采样;录音产物即 wav 恒可用。 */
const LOCAL_EXTS = ["wav"];
```

`allowedExts`(:228)追加 `engine === "local"` 分支 → `LOCAL_EXTS`;`pickFile`/大小校验的错误文案随之按引擎分支(local 用专述文案「本地引擎仅支持 wav(任意采样率)」)。
5. `submit` 的 params 分支(:317-343)追加:

```tsx
      } else if (engine === "local") {
        // 本地识别:语言枚举是 sherpa 口径(auto/zh/en/ja/ko/yue),空串归一 auto
        params = { language: language.trim() || "auto", itn: true };
```

6. 引导卡渲染:页内引擎分支渲染处(zhipu 引导卡之后、火山面板之前)追加 local 分支——`localReady.isLoading` → null;`!ready` → 引导卡(文案「本地识别引擎或模型尚未安装」+ missing 列表 + 去设置页链接,与 LocalTTSPanel 同款);ready → 正常表单区(火山面板同款布局:输入区/录音 Tab 沿用现有 upload/recording 通道渲染——local 引擎下 URL Tab 隐藏:模式 Tabs 渲染处按 `engine === "local"` 过滤掉 url 项)。
7. `changeVersion`/录音逻辑不动(录音产物是 wav,恒在白名单)。

- [ ] **Step 2: 类型检查 + 构建 + 提交**

Run: `cd web && npx tsc --noEmit && npm run build`

```bash
git add web/src/pages/ASRPage.tsx
git commit -m "feat(web): ASR 页「本地推理」引擎——就绪引导卡与 wav-only 通道"
```

---

### Task 5: 全量验证与真机手测

**Files:**
- 无新代码(验证;发现问题走修复)

- [ ] **Step 1: 自动化门禁**

```bash
go test ./... && gofmt -l cmd internal && go vet ./... && (cd web && npx tsc --noEmit && npm run build)
```

- [ ] **Step 2: 真机手测清单(浏览器;后端真实服务)**

逐项核对,任何一项不过即修复后重跑(修复经正常提交):

1. 设置页 → 本地环境:下载 sherpa-onnx 引擎 + sensevoice-int8(如未装);audiocpp + 任一 Qwen3 模型(如已装跳过)。模型行依赖提示:未装引擎的模型行出现「需先下载引擎才能本地运行」小字;装完提示消失。
2. `/tts?engine=local`:未装 audiocpp 或任一 GGUF 时渲染引导卡(missing 列出引擎/模型名);装齐后渲染表单。
3. TTS 克隆:选已安装模型 + 上传 3-60 秒参考音频 + 参考转写 → 开始合成 → 进度推进 → 产物 WavePlayer 可播放。
4. TTS 预置(若装了 CustomVoice):选音色 + 风格指令 → 合成成功(未装则该模式自然不可选——预置模型不在已安装列表,选 base 模型 + preset 提交会收到后端直述错误 Toast,验证之)。
5. `/asr` 引擎切「本地推理」:未就绪时引导卡;就绪后表单——URL Tab 隐藏、上传/录音可用;上传 mp3 应被白名单拦(文案指明仅 wav);上传 wav → 识别成功 → 文稿/播放联动正常。
6. 切回火山/千问引擎一切照旧(回归)。
7. 服务重启后进 TTS/ASR 本地页签:ready 查询自动刷新,无需手动刷新页面。

- [ ] **Step 3: 收尾提交(若手测有修复)**

```bash
git add -A && git commit -m "fix(web): 前端手测修复(如有)"
```

---

## Self-Review 结论(已执行)

1. **Spec 覆盖**:spec §8 四个要点——设置表依赖提示(Task 2)、TTSPage 本地页签+引导卡(Task 3)、ASRPage 本地引擎+wav-only(Task 4)、ready 查询接线(Task 1/3/4);验证(Task 5)。§8 的「引擎页签始终显示」在两页签实现中保持。
2. **占位扫描**:无 TBD;LocalTTSPanel 的 ProgressBody/AudioRow 调用形态已注明以 TTSShared 实际签名为准(给出裁决规则:改调用不改 TTSShared)。
3. **类型一致性**:`LocalReady/LocalReadyMissing/useLocalReady/useLocalVoices` 在 Task 1 定义、Task 3/4 消费;`engineInstalled` prop 在 Task 2 内闭环;提交参数与后端 provider/local 的 ParamSpecs 逐一对应(model/mode/ref_text/speaker/instruct/language/text/itn)。
