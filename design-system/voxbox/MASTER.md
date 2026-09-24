# Design System Master File — voxbox

> **LOGIC:** 构建具体页面时，先查 `design-system/voxbox/pages/[page].md`。存在则该页规则**覆盖**本文件；不存在则严格遵循本文件。
>
> 本文件基于 ui-ux-pro-max 生成结果**人工校正**：保留其有效判据（AAA 对比、Fira Code/Fira Sans 技术精确调性、Dark audio 色板取向、间距与反模式清单），替换掉不适用于后台工具的落地页 Pattern/Style，并补齐中文与离线约束。2026-09-24 二次设计：由「暖黑金属机架 + 琥珀信号灯」改向「深空信号站」。2026-09-24 三次设计：改向「暖纸咖啡 × 暗夜烘焙」（亮色对齐 R2T2 demo 的暖纸+深咖啡，暗色由同一暖色身份推导），深空蓝青版全部退役。

**Project:** voxbox（AI 语音工具箱 · 单用户本地工具）
**Category:** Admin Console / Developer Tool
**Updated:** 2026-09-24

---

## 0. 设计方向（一句话）

**把界面做成一间暖调工作室（暗色是深夜的烘焙间）**：亮色是暖纸画布 + 深咖啡墨色（纸面铅字的手作质感），暗色是深烘咖啡棕黑 + 焦糖铜信号色（咖啡豆在暗处的发光态）；刻印微标签、等宽数字读数不变。产品调性是「温暖的精密工作台」，不是「炫酷 AI 产品」，更不是「模板化后台」。

**材质三律（反「Gradio 味」的硬判据，2026-09-24 三次校准）**：
1. **无纯平面**——一切容器表面必须有方向性：卡片用极缓的顶亮渐变（暖顶光），输入用凹槽内嵌阴影（仪表凹陷）。
2. **无中性灰**——中性色全部带暖棕倾向（暗色 canvas 偏 `#161009` 族，非 `#0A0A0C` 无彩黑，更禁止纯黑白与蓝冷）；亮色主题是暖纸/咖啡，不是冷雾白。
3. **有空气感**——画布铺 ≤2.5% 噪点纹理（胶片颗粒），主操作按钮带焦糖光晕；光来自「暖光工作灯」，不来自装饰彩虹。

设计决策的判据：任何视觉选择都要能回答「这像不像暖灯下做音频的手作工作台」。不像的，删掉。

---

## 1. 色彩

### 1.1 暗色（默认）——暗夜烘焙间

| 角色 | 值 | 语义 / 用途 |
|---|---|---|
| `--color-bg` | `#161009` | 应用画布（深烘咖啡棕黑，暖调倾向，**禁止纯黑、蓝冷与无彩灰黑**） |
| `--color-panel` | `#1F1810` | 侧栏、面板底、表头 |
| `--color-raise` | `#271E13` | 卡片、内容容器 |
| `--color-raise-2` | `#322718` | 卡片内嵌槽、hover 底 |
| `--color-inset` | `#100B05` | 输入凹槽底（比 raise 更深，配内嵌阴影） |
| `--color-line` | `rgba(240,205,155,.11)` | 发丝分隔线（奶油低透明） |
| `--color-line-strong` | `rgba(240,205,155,.22)` | 分组边界、输入框边框 |
| `--color-fg` | `#F6ECDD` | 主文本（奶油白） |
| `--color-fg-2` | `#CDB697` | 次级文本、值（焦糖） |
| `--color-muted` | `#937F66` | 微标签、说明、占位（灰褐） |
| `--color-accent` | `#E2A35A` | **焦糖铜**：主操作、激活态、焦点环（亮色深咖啡的发光态） |
| `--color-accent-hi` | `#F2C184` | accent hover / 按钮渐变顶 |
| `--color-accent-ink` | `#2A1A06` | 落在 accent 底上的文字（深咖啡） |
| `--color-meter` | `#9FBE6F` | **苔藓绿**：成功、波形、电平（不做主操作色，大地色系与焦糖和谐） |
| `--color-warn` | `#F0C04C` | 警示（亮琥珀，与铜 accent 拉开明度与色相） |
| `--color-danger` | `#FF6D5C` | 错误、破坏性操作（暖珊瑚） |
| `--atmosphere-primary` | `rgba(226,163,90,.15)` | 画布中心焦糖暖光（body::before） |
| `--atmosphere-secondary` | `rgba(240,192,76,.09)` | 左上角琥珀次级光晕 |
| `--grid-color` | `rgba(240,205,155,.07)` | 42px 图纸网格线 |

### 1.2 亮色（完整适配，非降级）——暖纸咖啡

| 角色 | 值 |
|---|---|
| `--color-bg` | `#F7F0E6` |
| `--color-panel` | `#FCF6EC` |
| `--color-raise` | `#FFFAF2` |
| `--color-raise-2` | `#F0E5D5` |
| `--color-inset` | `#ECDFCC` |
| `--color-line` | `rgba(90,61,38,.15)` |
| `--color-line-strong` | `rgba(90,61,38,.30)` |
| `--color-fg` | `#241A13` |
| `--color-fg-2` | `#4E3B2D` |
| `--color-muted` | `#7C6654` |
| `--color-accent` | `#543521` |
| `--color-accent-hi` | `#754A2B` |
| `--color-accent-ink` | `#FFFAF2` |
| `--color-meter` | `#5A6E2D` |
| `--color-warn` | `#B45309` |
| `--color-danger` | `#B42318` |
| `--atmosphere-primary` | `rgba(194,142,90,.34)` |
| `--atmosphere-secondary` | `rgba(232,198,157,.46)` |
| `--grid-color` | `rgba(111,77,48,.10)` |

亮色是「暖纸上的铅字」：暖纸画布 + 咖啡墨色（取自 R2T2 demo 暖纸主题），**禁止纯白 `#FFFFFF` 大面积做画布/面板**（纯白=通用后台即视感的根源），也**禁止蓝冷色任何形式回流**。meter 用暗橄榄绿而非 demo 的纯棕：voxbox 需要「绿=成功/电平」的语义区分，橄榄绿与大地色系和谐。

### 1.3 材质规则（全主题通用，组件层实现）

1. **卡片 = 暖顶光玻璃面**：`.card-surface`——`linear-gradient(180deg, edge-light 混入 3%, raise)` 顶亮渐变 + `backdrop-filter: blur(10px)`（raise 为半透明色，透出画布氛围光）+ `--shadow-1` 环境投影。禁止再写裸 `bg-raise`。
2. **输入 = 仪表凹槽**：底用 `--color-inset`（比容器深一档，**保持实色**保证可读性）+ `inset 0 1px 2px var(--inset-shadow)`；focus 时 accent 色边框+外环，内嵌阴影保留。
3. **主操作按钮 = 焦糖束流**：`.btn-primary`——`linear-gradient(180deg, accent-hi, accent)` + 顶部内高光 + `0 2px 14px -4px` 焦糖光晕；hover 整体提亮，**禁止位移**。
4. **画布噪点**：`body::after` 全屏 feTurbulence 噪点，暗色 `opacity .025`、亮色 `.02`，`pointer-events:none`，z 最顶层——胶片颗粒统一材质，**禁止任何大于 4% 的纹理透明度**。
5. **波形 idle 色**：`--wave-idle` 随主题（暗 `rgba(246,236,221,.16)` / 亮 `rgba(90,61,38,.22)`），WavePlayer 从令牌读取，禁止硬编码。
6. **画布氛围（景深的来源）**：`body::before` 固定层（`inset: -24%`，z -2）——焦糖暖光双晕（`--atmosphere-primary` 画面中心 + `--atmosphere-secondary` 左上角）叠 42px 图纸网格（`--grid-color` 1px 线），`mask-image` 上下缘 14% 渐隐。**面板因此「浮」在纸面上，禁止把 panel/raise 改回实色平涂**。
7. **玻璃外壳**：侧栏/顶栏/移动抽屉/浮出菜单用半透明 `bg-panel` + `backdrop-blur-xl`（顶栏 `backdrop-blur`）——玻璃后面透出氛围光；`--panel`/`--raise` 的 alpha 是设计值，调整需两主题同步。

**色彩纪律**：焦糖铜（暗）/深咖啡（亮）是唯一主操作色；绿只表示「信号/成功/电平」；红只表示「错误/破坏」；黄只表示「警示」。禁止第二个装饰性强调色（紫/粉霓虹一律不进界面）。

---

## 2. 字体

**Fira 不覆盖中文**，必须逐字形回退——中英混排是本产品常态，字体栈顺序不可改：

```css
--font-sans: "Fira Sans", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", system-ui, sans-serif;
--font-mono: "Fira Code", ui-monospace, SFMono-Regular, Menlo, monospace;
```

- **Fira Sans**：界面文本（拉丁 + 数字）
- **Fira Code**：**全部数字读数**——时间码、时长、耗时、计数、ID、参数值
- **中文**：PingFang SC / 微软雅黑回退（不做中文字体定制）

**字体交付**：`@fontsource/fira-sans` 与 `@fontsource/fira-code`（npm 自托管、Vite 打包）。**禁止 Google Fonts CDN**——本地工具要离线可用，且 CDN 在境内不稳定。

### 字号与字重标尺

| Token | 值 | 用途 |
|---|---|---|
| `micro` | 11px / `letter-spacing .08em` / uppercase | **刻印微标签**：字段名、区块标题、表头 |
| `xs` | 12px | 辅助、时间戳 |
| `sm` | 13px | 次级正文、按钮 |
| `base` | 14px | 正文 |
| `lg` | 16px | 区块标题 |
| `xl` | 20px | 页面标题 |
| `2xl` | 26px | 面板级大标题 |
| `3xl` | 34px / mono | **统计读数**（工作台） |

字重仅用 400 / 500 / 600。禁止 700+（会破坏精密感）。数字一律 `font-variant-numeric: tabular-nums`。

---

## 3. 空间、圆角、阴影、动效

**间距**（只用这几个，禁止随手写 px）：`4 / 8 / 12 / 16 / 24 / 32 / 48`

**圆角**：`--radius-sm 7px`（输入、按钮）· `md 12px`（卡片）· `lg 16px`（浮层）· `full`（徽标、圆点）

**阴影**（暗色底靠「顶缘暖光内高光 + 深投影」造层次，不是靠大黑影；阴影随主题切换，定义为变量）：
```css
/* 暗色 */
--elev-1: 0 1px 2px rgba(6,3,1,.55), inset 0 1px 0 rgba(255,225,180,.06);
--elev-2: 0 8px 24px -8px rgba(6,3,1,.70), inset 0 1px 0 rgba(255,225,180,.06);
--elev-3: 0 24px 48px -12px rgba(4,2,0,.80);
/* 亮色 */
--elev-1: 0 1px 2px rgba(83,51,25,.10), inset 0 1px 0 rgba(255,255,255,.8);
--elev-2: 0 8px 24px -10px rgba(83,51,25,.18), inset 0 1px 0 rgba(255,255,255,.8);
--elev-3: 0 24px 48px -12px rgba(83,51,25,.24);
```

**动效**：`--dur-1 120ms`（状态切换）· `--dur-2 200ms`（进浮层）· `--ease cubic-bezier(.2,.8,.2,1)`
- 只做**有意义的动效**：任务进度、播放电平、浮层进离场、页面首屏分段浮现（staggered）。
- **禁止**装饰性无限动画（呼吸/波形/闪烁只用于 running/loading 状态，且尊重 `prefers-reduced-motion`）。
- **禁止** layout-shifting hover（`scale` / `translateY` 位移）。

---

## 4. 图标

**Lucide**（`lucide-react`），统一 `stroke-width 1.75`、尺寸 16 / 20 两档。

- **严禁 emoji 作图标**（一律 SVG）
- 导航图标、操作图标、空状态图标一律来自同一套 stroke 图标
- 图标不单独承担语义时配文字标签（a11y）

---

## 5. 组件规格

组件位于 `web/src/ui/`，**所有页面必须复用，禁止再手写卡片/按钮 class**。

| 组件 | 要点 |
|---|---|
| `Button` | 变体 `primary`(焦糖实底) / `secondary`(描边) / `ghost` / `danger`；尺寸 sm/md；`loading` 态内置 spinner 且禁点；hover 只变底色/描边色，**不位移** |
| `Field` | **刻印微标签**(micro) + 控件 + hint/error 行；label 必须与控件关联（htmlFor） |
| `Input` / `Textarea` | 底 `raise-2`、边框 `line-strong`、focus 时边框转 accent + 2px accent 外环（`focus-visible`，不可移除） |
| `Select` | **自定义 listbox，禁止原生 `<select>`**。触发钮与 Input 同材质；面板 `bg-panel` + `line-strong` + `shadow-3` + `radius-md`，下方放不下向上翻；选中项 accent `Check`，活动项 `raise-2`，`optgroup` 组头用 micro 微标签；键盘 ↑↓/Home/End/Enter/Esc/首字跳转，Esc 只关面板不冒泡给 Modal。API 与原生同形（`onChange={(e) => e.target.value}`，子元素写 `<option>/<optgroup>`） |
| `Card` | 可选 header（标题 + 右侧操作）；内部 16/24 间距；hover 只提亮边框/底色。**`CardBody` 自带 `p-4`，传 `className` 是叠加不是覆盖**——只传 `space-y-*` 控制纵向节奏；改 padding 须显式传 `px-*`/`py-*`/`p-*`（曾因默认值被覆盖导致全站卡片掉内边距，勿回退） |
| `Badge` | 任务状态：pending 灰 / running accent(带呼吸点) / succeeded 绿 / failed 红 / canceled 灰；**不得只靠颜色**（带文字） |
| `ProgressBar` | 细条(2px)，底 `raise-2`、条 accent；running 时条上叠加流光 |
| `WaveLoader` | **波形加载指示**：4 根错相弹跳电平条（`currentColor`，默认 accent），用于「等待/处理中」的行内状态（任务进度卡、加载更多、空结果等待），替代 spinner 语义；静态占位仍用 Skeleton；`role="status"` + `aria-label` |
| `Skeleton` | 与最终布局同形状的骨架（**内容区不用 spinner**），`animate-pulse` |
| `EmptyState` | 图标 + 标题 + 一句说明 + 一个主操作（禁止空白页） |
| `Toast` | 右上角，250ms 滑入淡出，4s 自动消失，`role="status"`；**全站替换 `alert()`** |
| `ConfirmDialog` | 破坏性操作（删除任务）必须二次确认，危险按钮在右侧 |
| `Tabs` | 分段控件（segmented）；键盘方向键可切，`role="tablist"` |
| `IconButton` | 方形图标按钮，必有 `title`/`aria-label` |
| `MicroLabel` | 刻印微标签的统一实现 |
| **`WavePlayer`** | **签名组件**：WebAudio 解码取峰值 → canvas 波形（accent 已播 / idle 未播）+ 播放头 + mono 时间码 `mm:ss.d`；用于所有音频产物 |
| `MiniPlayerBar` | 全局底部播放条：当前产物、上/下一个、波形、关闭；跨页面常驻（zustand） |
| `LevelMeter` | 播放中电平条（绿），仅在播放态出现 |

---

## 6. 页面模式（后台控制台，非落地页）

**外壳**：左侧固定导轨 232px（图标+文字，激活态 = accent 左侧标记 + 提亮底）· 顶部条（页面标题 + 上下文 + 全局操作：主题切换/健康状态）· 主区 `max-w-[1100px]` 居中，32px 内边距。
**响应式**：`<1024` 导轨收为纯图标（64px），`<768` 导轨变底部/抽屉；主区不出现横向滚动。

**页面结构**：
1. **页头**：标题(20/600) + 一句说明(13 muted) + 右侧主操作
2. **内容**：表单类页面在 `≥1024` 走两栏（编辑区 flex-1 + 参数面板 320px），窄屏单栏
3. **结果**：卡片 + 行式布局；音频行 = 轨道标签 + WavePlayer + 下载/联动操作
4. **任务列表**：表格，mono 时间戳、状态 Badge、行内操作
5. **加载**：Skeleton 同构骨架；**空态**：EmptyState 带主操作；**错误**：行内错误 + Toast，不用 alert

**工作台**：只放真数据（`/api/tasks` 汇总：各工具任务数/成功率/最近任务），**禁止编造统计**。

---

## 7. 反模式（禁止）

沿用并强化 ui-ux-pro-max 清单：

- ❌ **emoji 作图标** —— 一律 SVG（Lucide）
- ❌ **纯黑 #000 / 无彩灰黑 / 蓝冷画布** —— 用 `#161009` 族暖棕画布
- ❌ **位移/缩放 hover** —— 只改颜色与边框
- ❌ **超过 300ms 的过渡**，或瞬时无过渡
- ❌ **`alert()` / `confirm()`** —— 用 Toast / ConfirmDialog
- ❌ **只靠颜色区分状态**（状态必须带文字）
- ❌ **移除默认焦点环**（必须换成本设计的 accent 焦点环）
- ❌ **内容区 spinner**（用 Skeleton）
- ❌ **空白页**（必须有 EmptyState）
- ❌ **`var(--x)` 任意值散写**（用 `@theme` 语义工具类，如 `bg-panel` / `text-fg-2`）
- ❌ 第二个装饰性强调色（紫/粉/橙霓虹）、700+ 字重、多套圆角混用
- ❌ **旧深空青蓝版色值回流**（`#22D3EE`/`#070B15` 族与冷雾亮色已整体退役，新代码禁止引用）

---

## 8. 交付前检查表

- [ ] 无 emoji 图标；图标全部来自 Lucide 且尺寸统一
- [ ] 所有可点元素有 `cursor-pointer` 与可见 hover 反馈
- [ ] 过渡 150–300ms；hover 无位移
- [ ] 亮色模式文本对比 ≥ 4.5:1，边框可见，两种主题都实测
- [ ] 键盘可达：焦点环可见、Tab 顺序合理、对话框可 Esc 关闭
- [ ] `prefers-reduced-motion` 下禁用呼吸/滑入动画
- [ ] 375 / 768 / 1024 / 1440 四档响应式，无横向滚动
- [ ] 加载有 Skeleton、空态有引导、错误有 Toast 与行内提示
- [ ] 数字读数使用 mono + tabular-nums
- [ ] 无 `alert()` 残留、无 `var()` 任意值散写

---

## 9. 页面级覆盖

页面特定偏离写在 `design-system/voxbox/pages/<page>.md`（存在即覆盖本文件）。当前无覆盖文件。
