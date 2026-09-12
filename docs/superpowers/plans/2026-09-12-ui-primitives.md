# 控件层与设计规范落地 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立一层共享控件（Button / Badge / StatusDot / Card / DataTable）和一份可执行的设计规范，把仓库里 5 套表格、5 套卡片、重复定义的徽章收敛成一套，并修掉 10 个「用了但 CSS 里没有」的失效类名——**观感保持不变**。

**Architecture:** 样式仍然全部留在 `web/src/styles.css`（不拆文件、不引框架，遵守 CLAUDE.md）。改动是在 `web/src/components/` 下新增扁平的控件组件，把散落在各页面的 `className` 字符串拼接换成组件的 `props`。规范不只写成文档，还写成一个 `node` 守卫脚本 `web/scripts/check-ui.mjs`，接进 `npm run build`——**规范能被 CI 执行，才不会半年后又漂回去**。

**Tech Stack:** React 18 + TypeScript 5.7 + Vite 6，无测试框架（`tsc -b` 与本计划新增的守卫脚本是仅有的自动检查）。

**Spec:** 设计画布 artboard `System.dc.html`（「设计规范」画板），来源 Artifact `65792558-bd3b-4c9d-aa22-3aa26b2dbb71`，正文摘要见本文件「附录 A」。

## Global Constraints

这些约束适用于**每一个** task，不再逐条重复：

- **观感不动。** 本计划不改圆角令牌（`--radius-sm/--radius/--radius-lg` 保持 `6px/9px/14px`）、不去掉 `.btn` 的渐变与阴影、不重排字号档位。规范文稿里的 4/6/8 圆角与扁平按钮**本轮不落地**，只写进文档的「后续」小节。
- 所有样式写在 `web/src/styles.css`。不新增 CSS 文件、不引 CSS 框架 / 组件库 / CSS-in-JS。
- 颜色、圆角、阴影、时长、缓动一律取 `styles.css` 开头的令牌，不写裸 hex。新增令牌必须 light / dark 两个块都加。
- 代码注释用英文，文档（`docs/`、`CHANGELOG.md`）用中文。注释解释**为什么**，不解释代码在做什么。
- 任何可能装长文本的 flex/grid 子项写 `min-width: 0`（列方向 `min-height: 0`）。
- 每个 task 结束前跑 `npm --prefix web run build`，必须通过。
- 每个 task 单独提交，commit message 说清**为什么改**。
- 开发分支：`claude/determined-ramanujan-a85yio`。
- Commit message 结尾附加：
  ```
  Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
  ```

---

## File Structure

| 文件 | 职责 |
| --- | --- |
| `web/scripts/check-ui.mjs` | **新建。** 设计规范的可执行部分：交叉比对 tsx 里用到的 BEM 类名与 `styles.css` 里定义的选择器，报告失效类名；后续 task 逐步加严规则。 |
| `web/package.json` | **修改。** 新增 `check:ui` 脚本，并把它接进 `build`。 |
| `docs/design-system.md` | **新建。** 规范正文：令牌、尺度、控件清单、六条原则、与规范文稿的取舍说明。 |
| `web/src/components/Button.tsx` | **新建。** `variant` / `size` / `icon` 三个维度，取代 `.btn` 类名拼接。 |
| `web/src/components/Badge.tsx` | **新建。** 静态状态标签（原 `.badge` 族 + `.status__meta`）。 |
| `web/src/components/StatusDot.tsx` | **新建。** 状态圆点（原 `.status__dot` / `.console__dot` / `.hostterm__dot` / `.pill` / `.pdot`）。 |
| `web/src/components/Card.tsx` | **新建。** 表面容器，取代 `.card` / `.browse-card` / `.schemcard` / `.jvmcard` / `.kpi`。 |
| `web/src/components/DataTable.tsx` | **新建。** 栅格表格，取代 `.ptable` / `.data-table` / `.plugin-table` / `.rows` / `.asset`。 |
| `web/src/styles.css` | **修改。** 合并重复选择器、补 `.btn--small`、把五套卡片/表格的规则收敛到 `.card` / `.dtable` 两族。 |
| `CHANGELOG.md` | **修改。** 「未发布」小节记一条用户可见的变化（失效类名修复）。 |

**不做的事：** 不动 `Page.tsx`（页面框已经统一，28 个页面在用）；不动 `.chip`（它是**可点的筛选控件**，不是徽章，与 `.badge` 语义不同，合并会是错的）；不动终端两块画布 `--term-*` / `--shell-*` 的配色。

---

### Task 1: 类名守卫脚本 + 修掉 10 个失效类

这是整个计划的地基：先有一个能报错的检查，后面每一步才有红/绿可依。它也立刻还掉一笔债——目前有 10 个 BEM 类名在 tsx 里被使用、在 `styles.css` 里根本没有定义，其中 `btn--small` 影响 15 个按钮的尺寸。

**Files:**
- Create: `web/scripts/check-ui.mjs`
- Modify: `web/package.json`
- Modify: `web/src/styles.css`
- Modify: `web/src/components/InstancePlugins.tsx:291`
- Modify: `web/src/components/PluginLibraryDrawer.tsx`（`btn--small` ×6、`matrix__recon`、`pstate--ok`）
- Modify: `web/src/components/PluginLibraryPage.tsx`（`btn--small` ×6）
- Modify: `web/src/components/PluginSourceSettings.tsx`（`btn--small` ×2）
- Modify: `web/src/components/ConfigHistory.tsx:837`（`chist__line`）
- Modify: `web/src/components/TopBar.tsx`（`topbar__fact--cpu` / `--memory`）
- Modify: `web/src/components/NewInstanceWizard.tsx`（`wizard-step__label`、`file-toolbar__hint`）
- Modify: `web/src/components/InstanceCorePicker.tsx`、`web/src/components/JavaPage.tsx`（`file-toolbar__hint`）
- Modify: `web/src/components/NetworkPage.tsx`（`netlink__dot--foreign`）

**Interfaces:**
- Consumes: 无（第一个 task）
- Produces: `npm --prefix web run check:ui` 命令，退出码非零即失败。后续 task 靠它验证；`web/scripts/check-ui.mjs` 里的 `RULES` 数组是后续 task 加规则的挂载点。

- [ ] **Step 1: 写守卫脚本（此时它应当报错——这就是 failing test）**

创建 `web/scripts/check-ui.mjs`：

```js
// The design system, as something CI can fail on.
//
// A className in this codebase is a bare string, so a typo is invisible: the
// element just renders unstyled and nobody notices for months. `btn--small`
// was used fifteen times and defined zero times. Types cannot catch that, and
// there is no CSS build step that would — so this script is the check.
//
// Only BEM-shaped tokens (containing `__` or `--`) are verified. Single-word
// lowercase tokens are skipped: template-literal interpolation makes object
// keys and state names look like classes, and every one of those is a false
// positive.
import fs from 'node:fs'
import path from 'node:path'

const SRC = new URL('../src/', import.meta.url).pathname
const CSS = path.join(SRC, 'styles.css')

/** Every class name that appears anywhere in a selector. */
function definedClasses() {
  const css = fs.readFileSync(CSS, 'utf8')
  const out = new Set()
  for (const m of css.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) out.add(m[1])
  return out
}

function tsxFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) return tsxFiles(p)
    return p.endsWith('.tsx') ? [p] : []
  })
}

/** class name -> files that use it, for BEM-shaped tokens only. */
function usedClasses() {
  const out = new Map()
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    for (const m of src.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\}|\{"([^"]*)"\})/g)) {
      for (const raw of (m[1] ?? m[2] ?? m[3] ?? '').split(/[\s`]+/)) {
        const token = raw.trim()
        if (!/^[a-z][\w-]*$/.test(token)) continue
        if (!token.includes('__') && !token.includes('--')) continue
        if (!out.has(token)) out.set(token, new Set())
        out.get(token).add(path.relative(SRC, file))
      }
    }
  }
  return out
}

const problems = []

/** Rule: every BEM class used in a component exists in styles.css. */
function ruleNoUndefinedClasses() {
  const defined = definedClasses()
  for (const [cls, files] of [...usedClasses()].sort()) {
    if (defined.has(cls)) continue
    problems.push(`未定义的类名 .${cls}  ←  ${[...files].join(', ')}`)
  }
}

const RULES = [ruleNoUndefinedClasses]

for (const rule of RULES) rule()

if (problems.length > 0) {
  console.error(`check-ui: ${problems.length} 处问题\n`)
  for (const p of problems) console.error('  ' + p)
  process.exit(1)
}
console.log('check-ui: 通过')
```

- [ ] **Step 2: 跑它，确认失败**

Run: `node web/scripts/check-ui.mjs`
Expected: FAIL，退出码 1，列出 10 条：

```
  未定义的类名 .btn--sm                ←  components/InstancePlugins.tsx
  未定义的类名 .btn--small             ←  components/PluginLibraryDrawer.tsx, components/PluginLibraryPage.tsx, components/PluginSourceSettings.tsx
  未定义的类名 .chist__line            ←  components/ConfigHistory.tsx
  未定义的类名 .file-toolbar__hint     ←  components/InstanceCorePicker.tsx, components/JavaPage.tsx, components/NewInstanceWizard.tsx
  未定义的类名 .matrix__recon          ←  components/PluginLibraryDrawer.tsx
  未定义的类名 .netlink__dot--foreign  ←  components/NetworkPage.tsx
  未定义的类名 .pstate--ok             ←  components/PluginLibraryDrawer.tsx
  未定义的类名 .topbar__fact--cpu      ←  components/TopBar.tsx
  未定义的类名 .topbar__fact--memory   ←  components/TopBar.tsx
  未定义的类名 .wizard-step__label     ←  components/NewInstanceWizard.tsx
```

- [ ] **Step 3: 逐条判断——每个失效类是「该补样式」还是「该删类名」**

对每一条，先看使用处的意图，再决定方向。不要一律补 CSS，也不要一律删。判断结果如下（已逐个核对过使用处）：

| 类名 | 处理 | 理由 |
| --- | --- | --- |
| `btn--small` / `btn--sm` | **补 CSS**，并把 `btn--sm` 统一成 `btn--small` | 16 处调用点明确要小号按钮，规范里也有 28px 这一档 |
| `chist__line` | **补 CSS**（基类），保留 `--add` / `--delete` | 只定义了修饰符没定义基类，是漏写 |
| `pstate--ok` | **删类名** | `.pstate` 基类即中性态，`ok` 本就不该上色（规范：只有异常才上色） |
| `topbar__fact--cpu` / `--memory` | **删类名** | 只有 `--uptime` 有窄屏隐藏规则；这两个从未被任何规则用到 |
| `file-toolbar__hint` | **补 CSS** | 三个页面都在用它显示提示文字，目前是裸文字 |
| `matrix__recon` | **删类名** | 同一元素上已有 `matrix__act`，`recon` 是重构残留 |
| `netlink__dot--foreign` | **补 CSS** | `.pdot--foreign` 有定义，这里是同一语义换了块名，补一条对齐 |
| `wizard-step__label` | **补 CSS** | 向导步骤文字，目前继承默认字号 |

- [ ] **Step 4: 补上该补的 CSS**

在 `web/src/styles.css` 中 `.btn--icon` 规则之后插入（小号按钮档；高度靠 padding 撑到 28px，与 `.btn` 的 `7px 14px→32px` 同一算法）：

```css
/* The second and only other control height. Fifteen call sites asked for this
   class for months while it did not exist, so every one of them silently
   rendered full size. */
.btn--small {
  padding: 5px 10px;
  font-size: 12px;
}
```

在 `.chist__line--add` 之前插入基类：

```css
/* Only the two modifiers existed, so an unchanged line had no row styling at
   all and sat flush against its neighbours. */
.chist__line {
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.7;
}
```

在 `.pdot--foreign` 规则旁补上 netlink 的对应写法：

```css
.netlink__dot--foreign {
  background: var(--caution);
}
```

在 `.wizard-step` 相关规则后补：

```css
.wizard-step__label {
  font-size: 12px;
  color: var(--text-dim);
}
```

在 `.file-toolbar` 相关规则后补：

```css
.file-toolbar__hint {
  color: var(--text-faint);
  font-size: 12px;
}
```

- [ ] **Step 5: 删掉该删的类名**

- `web/src/components/InstancePlugins.tsx:291`：`className="btn btn--sm"` → `className="btn btn--small"`
- `web/src/components/PluginLibraryDrawer.tsx`：去掉 `pstate--ok`（保留 `pstate`）、去掉 `matrix__recon`（保留 `matrix__act`）
- `web/src/components/TopBar.tsx`：去掉 `topbar__fact--cpu` 和 `topbar__fact--memory`（保留 `topbar__fact`）

- [ ] **Step 6: 跑守卫，确认通过**

Run: `node web/scripts/check-ui.mjs`
Expected: `check-ui: 通过`，退出码 0

- [ ] **Step 7: 接进 npm 脚本**

`web/package.json` 的 `scripts` 改成：

```json
  "scripts": {
    "dev": "vite",
    "build": "node scripts/check-ui.mjs && tsc -b && vite build",
    "check:ui": "node scripts/check-ui.mjs",
    "preview": "vite preview"
  },
```

放在 `tsc -b` **之前**：它是几十毫秒的纯文本检查，先跑先失败，省掉一次完整类型编译。

- [ ] **Step 8: 跑完整构建**

Run: `npm --prefix web run build`
Expected: `check-ui: 通过`，随后 `tsc -b` 与 `vite build` 均成功

- [ ] **Step 9: 人工核对那 16 个按钮**

`npm --prefix web run dev`，打开插件库页、插件库抽屉、插件源设置、实例插件页，确认这些按钮现在是小号（28px 高）且与相邻按钮对齐，没有把所在行撑高。明暗两种模式都看。

- [ ] **Step 10: 提交**

```bash
git add web/scripts/check-ui.mjs web/package.json web/src/styles.css web/src/components
git commit -m "$(cat <<'EOF'
新增类名守卫，修掉 10 个从未生效的类名

className 在这个仓库里是裸字符串，拼错了既不报错也不报警，元素只是安静地
渲染成没样式的样子。btn--small 被用了 15 次、定义了 0 次，也就是说插件库
那一片的小号按钮一直是全尺寸；chist__line 只有修饰符没有基类；
topbar__fact--cpu 从来没有被任何规则匹配过。tsc 管不到这一层，CSS 也没有
构建步骤会管，所以加一个脚本来管。

脚本只校验 BEM 形状（含 __ 或 --）的类名——单词形的 token 会被模板字符串
里的对象键和状态名污染，全是误报。接在 build 的最前面，几十毫秒，先失败
先省一次类型编译。

十处里六处补样式、四处删类名：pstate--ok 和 topbar__fact--cpu/--memory
本就不该有样式（正常态不上色是规范里的一条），matrix__recon 是重构残留。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
EOF
)"
```

---

### Task 2: 规范文档

把规范文稿落成仓库里的一页，并**明确写下哪些条目本轮没落地、为什么**——否则半年后没人知道 6/9/14 的圆角是有意保留还是忘了改。

**Files:**
- Create: `docs/design-system.md`
- Modify: `CLAUDE.md`（在「前端界面布局优化」小节加一行指向它）

**Interfaces:**
- Consumes: Task 1 的 `check:ui`（文档里要说明哪些规则已经可执行）
- Produces: `docs/design-system.md`，后续 task 的组件文档写在它的「控件清单」小节

- [ ] **Step 1: 写文档**

创建 `docs/design-system.md`，正文包含这些小节（内容取自附录 A，并按 Global Constraints 标注取舍）：

1. **这份文档管什么** —— 令牌已经统一、控件层是新建的；页面框看 `frontend-design` skill，不在这里重复。
2. **令牌** —— 现有 488 条令牌的分组说明（表面 `--bg`/`--surface-1..4`、文字 `--text`/`--text-dim`/`--text-faint`、描边、状态色、终端两块画布），以及「新增令牌必须 light / dark 都加」。
3. **尺度** —— 控件两档高度 32 默认 / 28 小；表格行高 40；间距沿用邻近区块节奏；圆角三档 `--radius-sm/--radius/--radius-lg`。
4. **控件清单** —— 每个控件一行：什么时候用、什么时候**不要**用。Task 3–6 逐个补进来。
5. **六条原则** —— 状态常驻 / 两层作用域 / 改动可见可撤 / 深色只给日志 / 术语说人话 / 危险操作有摩擦（附录 A 第 5 节原文）。
6. **一屏一个实心按钮** —— 单列一节，附现状：12 个组件违反，逐步收。
7. **本轮没落地的**（关键小节，逐条给理由）：
   - 圆角 4/6/8：现状 6/9/14，改动会波及全部页面，等控件层落地后单独一轮做，届时只需改令牌。
   - 按钮去渐变去阴影：同上。
   - 字号收到 6 档：现状 13 档，随控件迁移自然收敛，不单独做一轮。
   - **纯浅色单配色**：不采纳。仓库有 4 配色 × 明暗共 8 套，CLAUDE.md 硬性要求明暗对等；砍掉暗色是功能倒退。规范文稿的「深色只给日志」在本仓库的对应物是「终端两块画布在明暗下都保持深色且互相可分」。
8. **哪些规则已经能被 CI 执行** —— 列出 `check-ui.mjs` 当前的规则。

- [ ] **Step 2: 在 CLAUDE.md 里挂上入口**

在「前端界面布局优化」小节第一段之后加一行：

```markdown
令牌、控件清单和「一屏一个实心按钮」这类跨页面的约定写在 `docs/design-system.md`；其中能被机器检查的部分由 `npm --prefix web run check:ui` 执行（已接进 `build`）。
```

- [ ] **Step 3: 提交**

```bash
git add docs/design-system.md CLAUDE.md
git commit -m "$(cat <<'EOF'
写下设计规范，并记下哪些条目本轮不落地

令牌层其实早就统一了（组件区零裸 hex），缺的是控件层和一份说得清的约定。
文档同时记下四条**没有**采纳的：圆角 4/6/8、按钮去渐变、字号收档三条是
等控件层落地后改一处即可，现在改要动全部页面；纯浅色单配色直接不采纳，
仓库有 4 配色 × 明暗，砍掉暗色是倒退。

不写下「为什么没做」，半年后没人分得清 6/9/14 是有意保留还是忘了改。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
EOF
)"
```

---

### Task 3: Button 控件

391 个裸 `<button>` 里只有 217 个走 `.btn`。本 task 建立组件，并迁移 `.btn` 族的调用点；剩下那些用自有类名的按钮（`.link` 58 处等）不在本轮范围。

**Files:**
- Create: `web/src/components/Button.tsx`
- Modify: `web/scripts/check-ui.mjs`（加一条规则）
- Modify: 使用 `btn--primary` / `btn--danger` / `btn--small` 的组件

**Interfaces:**
- Consumes: Task 1 的 `.btn--small` CSS
- Produces:
  ```ts
  type ButtonVariant = 'default' | 'primary' | 'danger' | 'row'
  type ButtonSize = 'default' | 'small'
  interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
    variant?: ButtonVariant   // default: 'default'
    size?: ButtonSize         // default: 'default'
    icon?: boolean            // 方形图标按钮，必须同时给 aria-label
  }
  export function Button(props: ButtonProps): JSX.Element
  ```

- [ ] **Step 1: 写组件**

创建 `web/src/components/Button.tsx`：

```tsx
import type { ButtonHTMLAttributes } from 'react'

type Variant = 'default' | 'primary' | 'danger' | 'row'
type Size = 'default' | 'small'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** 'primary' is the one filled button a screen is allowed. */
  variant?: Variant
  size?: Size
  /** Square, icon-only. Callers must pass aria-label as well. */
  icon?: boolean
}

const VARIANT: Record<Variant, string> = {
  default: '',
  primary: 'btn--primary',
  danger: 'btn--danger',
  row: 'btn--row',
}

/**
 * Every button in the panel, so that a size or a variant is a value rather
 * than a string someone has to spell right.
 *
 * It was a string for a long time, and `btn--small` — a class that never
 * existed in the stylesheet — was spelled correctly fifteen times and styled
 * zero of them. `btn--sm` was a sixteenth spelling of the same intent. Types
 * are the fix; check-ui.mjs is the backstop for the call sites still passing
 * raw classNames.
 */
export function Button({ variant = 'default', size = 'default', icon, className, type, ...rest }: Props) {
  const classes = ['btn', VARIANT[variant]]
  if (size === 'small') classes.push('btn--small')
  if (icon) classes.push('btn--icon')
  if (className) classes.push(className)

  // A button inside a form defaults to submit, which has surprised this
  // codebase before — every caller here means an ordinary button.
  return <button type={type ?? 'button'} className={classes.filter(Boolean).join(' ')} {...rest} />
}
```

- [ ] **Step 2: 跑构建，确认组件本身编译通过**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 3: 迁移调用点**

按文件逐个替换，**一次一个文件，每个文件替换后立刻跑 `npm --prefix web run build`**。优先级顺序（按 `btn--primary` 密度，密度高的收益大）：

1. `PluginLibraryPage.tsx`（7 个 `btn--primary`）
2. `PluginLibraryDrawer.tsx`（4）
3. `NewInstanceWizard.tsx`（4）
4. `FileManager.tsx`（4）
5. `TerminalSettings.tsx`、`SchematicLibraryPage.tsx`、`DatabasePage.tsx`（各 3）
6. `PluginSourceSettings.tsx`、`InstancePlugins.tsx`（`btn--small` 的剩余调用点）

替换形如：

```tsx
// 之前
<button className="btn btn--small btn--primary" disabled={busy} onClick={onRepush}>重新推送</button>
// 之后
<Button variant="primary" size="small" disabled={busy} onClick={onRepush}>重新推送</Button>
```

图标按钮：

```tsx
// 之前
<button className="btn btn--icon" aria-label="刷新" onClick={reload}><Icon name="refresh" /></button>
// 之后
<Button icon aria-label="刷新" onClick={reload}><Icon name="refresh" /></Button>
```

- [ ] **Step 4: 加一条守卫规则——图标按钮必须有 aria-label**

在 `check-ui.mjs` 的 `RULES` 前加入并注册：

```js
/** Rule: an icon-only button carries no text, so it needs a label. */
function ruleIconButtonsAreLabelled() {
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    for (const m of src.matchAll(/<Button\b[^>]*\bicon\b[^>]*>/g)) {
      if (m[0].includes('aria-label')) continue
      problems.push(`图标按钮缺 aria-label  ←  ${path.relative(SRC, file)}`)
    }
  }
}
```

`const RULES = [ruleNoUndefinedClasses, ruleIconButtonsAreLabelled]`

- [ ] **Step 5: 跑守卫与构建**

Run: `npm --prefix web run build`
Expected: PASS（若报缺 aria-label，补上再跑）

- [ ] **Step 6: 人工核对**

`npm --prefix web run dev`，逐个打开上面迁移过的页面，确认按钮尺寸、间距、悬停态、禁用态与迁移前一致；明暗两种模式；1440 / 1024 / 390 三个宽度。

- [ ] **Step 7: 提交**

```bash
git add web/src/components web/scripts/check-ui.mjs
git commit -m "$(cat <<'EOF'
按钮收成一个组件，尺寸和变体从字符串变成值

.btn 的四个变体和两个尺寸此前靠拼类名表达，拼错没人拦——btn--small 拼对了
十五次、样式一次也没生效，btn--sm 是同一个意图的第十六种拼法。变成 props
之后这类错误由 tsc 挡住。

顺带给图标按钮加了一条 aria-label 守卫：图标按钮没有文字，漏了 label 屏幕
阅读器就只念得出 "button"。

本轮只迁移走 .btn 的调用点；另外那 174 个用自有类名的按钮（.link 一族占
58 处）留到后面按页面收。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
EOF
)"
```

---

### Task 4: Badge 与 StatusDot，并清掉重复选择器

`.badge` 在 `styles.css` 里被定义了**两次**（2939 行只有 transition、7055 行是本体），`.badge--warn` 也被定义了两次（9126 行带 border-color、9496 行不带）——后者胜出，所以所有警告徽章的描边着色一直是失效的。本 task 先清这个，再抽组件。

**Files:**
- Modify: `web/src/styles.css`（合并 `.badge` 两处、`.badge--warn` 两处；`.status__meta` 改为 `.badge--meta`）
- Create: `web/src/components/Badge.tsx`
- Create: `web/src/components/StatusDot.tsx`
- Modify: `web/scripts/check-ui.mjs`（加重复选择器规则）
- Modify: 28 个使用 `.badge` 的组件中，本轮先迁移 8 个高频文件

**Interfaces:**
- Consumes: 无
- Produces:
  ```ts
  type BadgeTone = 'neutral' | 'ok' | 'warn' | 'danger' | 'live' | 'muted'
  interface BadgeProps { tone?: BadgeTone; meta?: boolean; className?: string; children: React.ReactNode }
  export function Badge(props: BadgeProps): JSX.Element

  type DotState = 'stopped' | 'starting' | 'stopping' | 'running' | 'crashed'
  interface StatusDotProps { state: DotState; className?: string }
  export function StatusDot(props: StatusDotProps): JSX.Element
  ```

- [ ] **Step 1: 加重复选择器守卫规则（failing test）**

在 `check-ui.mjs` 加入并注册：

```js
/** Rule: one selector, one rule block.
 *
 *  `.badge--warn` was written twice with different declarations; the later
 *  block silently dropped the earlier one's border-color, so warning badges
 *  lost their tinted edge and nobody noticed. A second block for the same
 *  selector is either dead or an accidental override — both are bugs. */
const DUPLICATE_ALLOWED = new Set([
  // 有意的渐进定义写在这里，附一句为什么。
])

function ruleNoDuplicateSelectors() {
  const css = fs.readFileSync(CSS, 'utf8')
  const seen = new Map()
  for (const m of css.matchAll(/^(\.[a-zA-Z0-9_-]+(?:__[a-zA-Z0-9_-]+)?(?:--[a-zA-Z0-9_-]+)?) \{$/gm)) {
    seen.set(m[1], (seen.get(m[1]) ?? 0) + 1)
  }
  for (const [sel, n] of seen) {
    if (n > 1 && !DUPLICATE_ALLOWED.has(sel)) problems.push(`选择器重复定义 ${n} 次: ${sel}`)
  }
}
```

- [ ] **Step 2: 跑守卫，确认失败**

Run: `npm --prefix web run check:ui`
Expected: FAIL，报出 22 个重复选择器，其中包括 `.badge`、`.badge--warn`

- [ ] **Step 3: 合并 `.badge` 与 `.badge--warn`**

把 2939 行那段孤立的 `.badge { transition: ... }` 删掉，其内容并入 7055 行的本体：

```css
.badge {
  display: inline-flex;
  align-items: center;
  margin-left: 8px;
  padding: 1px 7px;
  background: var(--surface-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-pill);
  color: var(--text-dim);
  font-size: 10px;
  line-height: 1.7;
  white-space: nowrap;
  transition:
    background var(--dur-3) var(--ease),
    border-color var(--dur-3) var(--ease),
    box-shadow var(--dur-3) var(--ease),
    color var(--dur-3) var(--ease);
}
```

删掉 9496 行那个不带 `border-color` 的 `.badge--warn`，保留 9126 行带描边的版本——**这会让警告徽章重新出现描边**，是本轮唯一一处有意的视觉变化，记进 CHANGELOG。

- [ ] **Step 4: 处理其余 20 个重复选择器**

逐个看两处定义：内容相同 → 删后者；内容不同 → 判断哪个是意图，合并成一处；确属有意的渐进定义 → 加进 `DUPLICATE_ALLOWED` 并写明理由。清单：
`.wizard-task__note` `.topbar__toggle` `.sidebar__link` `.sidebar__group` `.select-sheet__note` `.ptable__row` `.preview` `.plugin-table__tags` `.picker__row` `.pdot--foreign` `.page` `.modal__card--wide` `.matrix__row` `.matrix__act` `.instance` `.editor__text` `.crumbs__here` `.chart__end-label` `.cfg__path` `.alloc__used`

- [ ] **Step 5: 跑守卫，确认通过**

Run: `npm --prefix web run check:ui`
Expected: `check-ui: 通过`

- [ ] **Step 6: 把 `.status__meta` 改名为 `.badge--meta`**

它的几何（`1px 8px` + pill + 11px）和 `.badge`（`1px 7px` + pill + 10px）是同一个东西，只是自成一族。在 `styles.css` 里改成：

```css
/* Facts that ride along with the state — pid, uptime, exit code. Boxed so a
   row of them reads as data rather than as a sentence. Numeric, so it is a
   pixel larger than a plain badge and lines its digits up. */
.badge--meta {
  padding: 1px 8px;
  background: var(--surface-2);
  border-color: var(--border);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
```

并把使用 `status__meta` 的调用点改成 `badge badge--meta`。

- [ ] **Step 7: 写 Badge 与 StatusDot 组件**

`web/src/components/Badge.tsx`：

```tsx
import type { ReactNode } from 'react'

type Tone = 'neutral' | 'ok' | 'warn' | 'danger' | 'live' | 'muted'

interface Props {
  /** Colour is a claim that something is wrong. Leave it neutral otherwise. */
  tone?: Tone
  /** Numbers that ride along with a state — pid, uptime, exit code. */
  meta?: boolean
  className?: string
  children: ReactNode
}

const TONE: Record<Tone, string> = {
  neutral: '',
  ok: 'badge--ok',
  warn: 'badge--warn',
  danger: 'badge--danger',
  live: 'badge--live',
  muted: 'badge--muted',
}

/**
 * The static state label. Not to be confused with Chip, which is a filter the
 * user clicks — the two looked adjacent enough that they were drifting toward
 * each other, and merging them would have made a control out of a label.
 *
 * The default tone is deliberately colourless: a list where every row carries
 * a coloured badge is a list where colour has stopped meaning anything.
 */
export function Badge({ tone = 'neutral', meta, className, children }: Props) {
  const classes = ['badge', TONE[tone]]
  if (meta) classes.push('badge--meta')
  if (className) classes.push(className)
  return <span className={classes.filter(Boolean).join(' ')}>{children}</span>
}
```

`web/src/components/StatusDot.tsx`：

```tsx
type State = 'stopped' | 'starting' | 'stopping' | 'running' | 'crashed'

interface Props {
  state: State
  className?: string
}

/**
 * The one thing on screen that changes without anyone touching it. The CSS
 * gives it a fifth-of-a-second transition on purpose: a dot that snaps from
 * grey to green is a change you only catch if you happened to be looking.
 *
 * Decorative — the state is always also written next to it in words, so this
 * is aria-hidden rather than an unlabelled colour swatch.
 */
export function StatusDot({ state, className }: Props) {
  const classes = ['status__dot', `status__dot--${state}`]
  if (className) classes.push(className)
  return <span className={classes.join(' ')} aria-hidden="true" />
}
```

- [ ] **Step 8: 迁移 8 个高频文件**

`InstanceList.tsx`、`InstanceCockpit.tsx`、`Sidebar.tsx`、`TopBar.tsx`、`JavaPage.tsx`、`DatabasePage.tsx`、`PluginLibraryPage.tsx`、`UpdatePanel.tsx`。一次一个文件，改完立刻 `npm --prefix web run build`。其余 20 个文件留到后续 task。

- [ ] **Step 9: 跑构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 10: 人工核对**

重点看警告徽章现在多了一圈描边是否可接受（这是本轮唯一有意的视觉变化）；状态点的过渡动画是否还在；明暗两种模式；侧栏折叠态。

- [ ] **Step 11: 提交**

```bash
git add web/src/components web/src/styles.css web/scripts/check-ui.mjs
git commit -m "$(cat <<'EOF'
徽章收成一个组件，并清掉 22 个重复定义的选择器

.badge 在 styles.css 里被写了两次，.badge--warn 也是——而且两次内容不同，
后一个把前一个的 border-color 悄悄覆盖掉了，所以警告徽章的描边着色一直没
生效。这是"同一个选择器写两遍"最典型的后果：不报错，只是有一半规则不算数。
加了守卫规则，22 个重复全部合并。

.status__meta 并进 .badge--meta：它的几何和 .badge 本来就是同一个东西，只是
自成一族。

.chip 不动。它有 cursor:pointer、有 --active 态、按下去会缩——那是可点的
筛选控件，不是徽章，并进来会把标签变成控件。

唯一的视觉变化：警告徽章恢复了描边。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
EOF
)"
```

---

### Task 5: Card 控件，合并 5 套卡片

`.card` / `.browse-card` / `.schemcard` / `.jvmcard` / `.kpi` 是同一个东西的五种写法：padding 分别 `16` / 无 / `12px 14px 10px` / `10px 12px` / `12px 14px`，圆角两个 `--radius-lg` 三个 `--radius`，表面四个 `--surface-1` 一个 `--surface-2`，五个里两个有 `--sheen` 渐变、两个有 `--shadow-sm`。

**注意 `.asset` 不在此列**——它是 `display: grid` + `border-bottom` + `min-height: 58px` 的表格行，归 Task 6。

**Files:**
- Create: `web/src/components/Card.tsx`
- Modify: `web/src/styles.css`
- Modify: `Dashboard.tsx` `PluginBrowse.tsx` `SchematicLibraryPage.tsx` `SchematicMarket.tsx` `JVMArgsEditor.tsx` `ResourcePanel.tsx`

**Interfaces:**
- Consumes: 无
- Produces:
  ```ts
  type CardPad = 'default' | 'tight' | 'none'   // 16px / 12px 14px / 0
  type CardTone = 'raised' | 'sunken'           // --surface-1 / --surface-2
  type CardAs = 'div' | 'article' | 'section'   // 调用点现有的三种宿主元素
  interface CardProps extends React.HTMLAttributes<HTMLElement> {
    pad?: CardPad; tone?: CardTone; interactive?: boolean; as?: CardAs
  }
  export function Card(props: CardProps): JSX.Element
  ```
  `as` 是必需的：`.schemcard` 和 `.browse-card` 的宿主是 `<article role="listitem">`，写死 `<div>` 会破坏这两处的列表语义。

- [ ] **Step 1: 决定收敛后的取值（观感不动 = 取多数）**

| 维度 | 五套现状 | 收敛取值 | 谁会变 |
| --- | --- | --- | --- |
| 圆角 | `--radius-lg` ×2、`--radius` ×3 | `--radius`（多数） | `.card`、`.browse-card` 变小 5px |
| 表面 | `--surface-1` ×4、`--surface-2` ×1 | `--surface-1`；`--surface-2` 做 `tone="sunken"` | 无 |
| padding | 16 / — / 12 14 10 / 10 12 / 12 14 | `default: 16px`、`tight: 12px 14px`、`none: 0` | `.schemcard` 底边 +2px、`.jvmcard` +2/+2 |
| sheen | ×2 | 保留在 `interactive` 上 | `.kpi` 失去 sheen |
| shadow | ×2 | `--shadow-sm` 全都有 | `.browse-card` / `.schemcard` / `.jvmcard` 多一层极淡阴影 |

`.card` 与 `.browse-card` 的圆角从 14px 变 9px 是本 task 最明显的一处变化。**若人工核对时觉得插件市场卡片变生硬，就反过来统一到 `--radius-lg`，并在文档里记下。** 这个决定留给 Step 6 的目视结果。

- [ ] **Step 2: 写组件**

创建 `web/src/components/Card.tsx`：

```tsx
import type { HTMLAttributes, ReactNode } from 'react'

type Pad = 'default' | 'tight' | 'none'
type Tone = 'raised' | 'sunken'

type As = 'div' | 'article' | 'section'

interface Props extends HTMLAttributes<HTMLElement> {
  pad?: Pad
  /** 'sunken' sits on --surface-2: a card inside a card. */
  tone?: Tone
  /** The whole card is the click target — gets the sheen and the hover lift. */
  interactive?: boolean
  /** The listing cards are <article role="listitem">; a hardcoded div would
   *  take the list semantics away from them. */
  as?: As
  children?: ReactNode
}

const PAD: Record<Pad, string> = { default: '', tight: 'card--tight', none: 'card--flush' }

/**
 * The surface every panel of content sits on.
 *
 * There were five of these — card, browse-card, schemcard, jvmcard, kpi — with
 * five paddings, two radii, two surfaces, and sheen and shadow on an arbitrary
 * two apiece. None of the differences meant anything; they are just the order
 * the pages were written in. Two of the five sat next to each other on the
 * same screen.
 */
export function Card({
  pad = 'default',
  tone = 'raised',
  interactive,
  as: Tag = 'div',
  className,
  children,
  ...rest
}: Props) {
  const classes = ['card', PAD[pad]]
  if (tone === 'sunken') classes.push('card--sunken')
  if (interactive) classes.push('card--interactive')
  if (className) classes.push(className)
  return (
    <Tag className={classes.filter(Boolean).join(' ')} {...rest}>
      {children}
    </Tag>
  )
}
```

- [ ] **Step 3: 改 `styles.css`**

把 `.card` 本体改成非交互的默认态，交互态拆到 `.card--interactive`：

```css
.card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  padding: 16px;
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow-sm);
  color: var(--text);
  text-align: left;
  font-size: 14px;
}

.card--tight { padding: 12px 14px; }
.card--flush { padding: 0; }
.card--sunken { background: var(--surface-2); }

/* The sheen and the lift are what tell you the whole card is a click target,
   so they belong to the interactive one only. A static panel that lifts on
   hover is a panel that lies about being clickable. */
.card--interactive {
  background-image: linear-gradient(180deg, var(--sheen), transparent 80px);
  cursor: pointer;
  font-family: inherit;
  transition:
    border-color var(--dur) var(--ease),
    transform var(--dur) var(--ease),
    box-shadow var(--dur) var(--ease);
}
```

然后删掉 `.browse-card` / `.schemcard` / `.jvmcard` / `.kpi` 的本体规则（各自的 `__` 子元素规则保留，只是父类换成 `.card`）。

三点必须注意：

1. **`.card--static` 整个删掉。** 它今天的存在是因为 `.card` 默认可点，静态卡片得反过来退订——`styles.css:10091` 起还为此写了 `.card--static:hover`、`.card--static:has(.card__open:hover)`、`.card--static:has(.card__open:active)` 三条抵消规则。默认改成静态之后这四条全部不需要，`Dashboard.tsx:244` 的 `card card--static card--${state}` 相应去掉 `card--static`。
2. **`.card--crashed` 及同族的 `card--${instance.state}` 保留。** 它们是状态着色，不是几何，与本 task 无关。仪表盘那行是模板字符串插值，`check-ui.mjs` 不校验，所以要人工确认这几个修饰符在 CSS 里都在。
3. **`.browse__rail-card`（`styles.css:11504`）本轮不动。** 它虽然也叫 card，但语义是插件市场左栏的分栏面板，不是内容卡片。记进 `docs/design-system.md` 的待办，不要顺手并进来。

- [ ] **Step 4: 迁移六个文件**

一次一个，改完立刻 `npm --prefix web run build`：
`ResourcePanel.tsx`（kpi）→ `JVMArgsEditor.tsx`（jvmcard）→ `SchematicLibraryPage.tsx` + `SchematicMarket.tsx`（schemcard）→ `PluginBrowse.tsx`（browse-card + card）→ `Dashboard.tsx`（card）

- [ ] **Step 5: 跑构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 6: 人工核对，并对圆角做最终决定**

并排看这几处：仪表盘、插件市场（卡片网格）、监控 KPI 行、蓝图库、JVM 参数编辑器。判断 9px 圆角是否可接受；不接受就把 `.card` 的 `border-radius` 改回 `--radius-lg`，在 `docs/design-system.md` 记一句为什么。明暗两种模式；1440 / 1200 / 1024 / 768 / 390 五个宽度，重点看卡片网格的 `auto-fill` 有没有在窄屏溢出。

- [ ] **Step 7: 提交**

```bash
git add web/src/components web/src/styles.css
git commit -m "$(cat <<'EOF'
五套卡片收成一个组件

card / browse-card / schemcard / jvmcard / kpi 是同一个东西的五种写法：五种
padding、两种圆角、两种表面，sheen 和 shadow 各自随机出现在其中两个上。
差异不表达任何东西，只是这五个页面被写出来的先后顺序。其中两个还并排出现
在同一屏上。

顺带把"整张卡可点"从基类拆到 card--interactive：sheen 和悬停抬升是在告诉
用户这里能点，静态面板带着它们就是在撒谎。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
EOF
)"
```

---

### Task 6: DataTable 控件，合并 5 套表格

`.ptable` / `.data-table` / `.plugin-table` / `.rows` / `.asset` 五套栅格表格，表头行高 34 / — / — / 36，数据行高 50 / — / — / 48 / 58，内边距 `0 14px` / `8px 12px` / `8px 14px` / `0 14px` / `10px 14px`，圆角 `--radius` / — / `--radius-lg` / `--radius-lg` / —。

**Files:**
- Create: `web/src/components/DataTable.tsx`
- Modify: `web/src/styles.css`
- Modify: `PluginLibraryDrawer.tsx` `PluginLibraryPage.tsx` `FileManager.tsx` `ResourcePanel.tsx` `InstancePlugins.tsx` `HostPage.tsx` `InstanceList.tsx` `Skeleton.tsx` `CoreCatalogue.tsx` `CoreLibraryPage.tsx` `DatabasePage.tsx` `JavaPage.tsx` `NewInstanceWizard.tsx` `SchematicMarket.tsx`

**Interfaces:**
- Consumes: 无
- Produces:
  ```ts
  type Density = 'default' | 'compact' | 'roomy'   // 40 / 34 / 48
  interface DataTableProps {
    /** grid-template-columns，直接透给 CSS 变量 --dtable-cols */
    columns: string
    density?: Density
    className?: string
    children: React.ReactNode
  }
  export function DataTable(props: DataTableProps): JSX.Element
  export function DataTableHead(props: { children: React.ReactNode }): JSX.Element
  export function DataTableRow(props: {
    selected?: boolean; onClick?: () => void; className?: string; children: React.ReactNode
  }): JSX.Element
  export function DataTableEmpty(props: { children: React.ReactNode }): JSX.Element
  ```

- [ ] **Step 1: 决定收敛后的取值**

| 维度 | 收敛取值 | 依据 |
| --- | --- | --- |
| 表头行高 | `34px` | 规范文稿；也是 `.ptable` 现值 |
| 数据行高 | `40px` 默认，`compact: 34px`，`roomy: 48px` | 规范文稿第 3 节 |
| 内边距 | `0 14px`（行高靠 min-height 撑） | 五套里 `0 14px` 占两套 |
| 圆角 | `--radius` | 与 Task 5 的 Card 对齐 |
| 列宽 | 由调用方传 `columns`，走 CSS 变量 `--dtable-cols` | 每张表列数不同，这是唯一必须外露的维度 |

**这是本计划里视觉变化最大的一步**：`.rows` 从 48px 降到 40px、`.asset` 从 58px 降到 40px。若核对时发现主机页和核心库的行挤了，就给这两处传 `density="roomy"`（48px），而不是把默认值改回去。

- [ ] **Step 2: 写组件**

创建 `web/src/components/DataTable.tsx`：

```tsx
import type { CSSProperties, ReactNode } from 'react'

type Density = 'default' | 'compact' | 'roomy'

interface Props {
  /** A grid-template-columns value. Each table's columns are its own; the
   *  row height, padding, radius and hover are not. */
  columns: string
  density?: Density
  className?: string
  children: ReactNode
}

const DENSITY: Record<Density, string> = {
  default: '',
  compact: 'dtable--compact',
  roomy: 'dtable--roomy',
}

/**
 * Every list of rows in the panel.
 *
 * There were five: ptable, data-table, plugin-table, rows, asset. Four header
 * heights, five row heights, three paddings, two radii — and no rule anywhere
 * for picking between them, so a new page picked whichever list it was copied
 * from. The columns are the only part that is genuinely per-table, so they are
 * the only prop; everything else is the same table.
 */
export function DataTable({ columns, density = 'default', className, children }: Props) {
  const classes = ['dtable', DENSITY[density]]
  if (className) classes.push(className)
  const style = { '--dtable-cols': columns } as CSSProperties
  return (
    <div className={classes.filter(Boolean).join(' ')} style={style} role="table">
      {children}
    </div>
  )
}

export function DataTableHead({ children }: { children: ReactNode }) {
  return (
    <div className="dtable__head" role="row">
      {children}
    </div>
  )
}

export function DataTableRow({
  selected,
  onClick,
  className,
  children,
}: {
  selected?: boolean
  onClick?: () => void
  className?: string
  children: ReactNode
}) {
  const classes = ['dtable__row']
  if (selected) classes.push('dtable__row--on')
  if (onClick) classes.push('dtable__row--clickable')
  if (className) classes.push(className)
  return (
    <div
      className={classes.join(' ')}
      role="row"
      aria-selected={selected}
      onClick={onClick}
      // A clickable row must be reachable and operable from the keyboard;
      // a div with an onClick is neither by default.
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={
        onClick
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                onClick()
              }
            }
          : undefined
      }
    >
      {children}
    </div>
  )
}

export function DataTableEmpty({ children }: { children: ReactNode }) {
  return <div className="dtable__empty">{children}</div>
}
```

- [ ] **Step 3: 写 CSS**

在 `styles.css` 里新增 `.dtable` 一族（放在原 `.ptable` 的位置，沿用它的注释）：

```css
.dtable {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}

.dtable__head,
.dtable__row {
  display: grid;
  grid-template-columns: var(--dtable-cols);
  align-items: center;
  gap: 0 12px;
  padding: 0 14px;
  /* Long names, long paths, long plugin descriptions — every one of these
     columns can hold text that would otherwise blow the grid open. */
  min-width: 0;
}

.dtable__head {
  min-height: 34px;
  background: var(--surface-2);
  border-bottom: 1px solid var(--border);
  color: var(--text-faint);
  font-size: 11px;
  letter-spacing: 0.06em;
}

.dtable__row {
  min-height: 40px;
  border-bottom: 1px solid var(--border);
  transition: background var(--dur-1) var(--ease);
}

.dtable__row:last-child { border-bottom: none; }
.dtable__row--clickable { cursor: pointer; }
.dtable__row--clickable:hover { background: var(--surface-2); }
.dtable__row--on { background: var(--accent-soft); }
.dtable__row:focus-visible { outline: 2px solid var(--ring); outline-offset: -2px; }

.dtable--compact .dtable__row { min-height: 34px; }
.dtable--roomy .dtable__row { min-height: 48px; }

.dtable__empty {
  padding: 22px 14px;
  color: var(--text-dim);
  font-size: 13px;
  text-align: center;
}
```

- [ ] **Step 4: 逐表迁移**

一张表一个提交单位内的一步，改完立刻 `npm --prefix web run build`。顺序从小到大：

1. `data-table`（2 文件）：`FileManager.tsx`、`ResourcePanel.tsx`
2. `plugin-table`（1 文件）：`InstancePlugins.tsx`
3. `rows`（3 文件）：`HostPage.tsx`、`InstanceList.tsx`、`Skeleton.tsx` —— 骨架屏的行高要跟着改，否则加载完会跳
4. `ptable`（2 文件）：`PluginLibraryDrawer.tsx`、`PluginLibraryPage.tsx`
5. `asset`（6 文件）：`CoreCatalogue.tsx`、`CoreLibraryPage.tsx`、`DatabasePage.tsx`、`JavaPage.tsx`、`NewInstanceWizard.tsx`、`SchematicMarket.tsx`

每张表迁移时把原来的 `grid-template-columns` 原样传进 `columns` prop，**列宽不动**——本 task 只统一行高、内边距、悬停与圆角。

- [ ] **Step 5: 删掉五套旧 CSS**

确认调用点全部迁完后，删掉 `.ptable` / `.data-table` / `.plugin-table` / `.rows` / `.asset` 的本体与行/表头规则。各自的 `__name`、`__tags` 这类内容级子元素规则保留，选择器父级改成 `.dtable`。

- [ ] **Step 6: 加守卫规则——旧表格类名不得复活**

在 `check-ui.mjs` 加入并注册：

```js
/** Rule: the five merged table families stay merged. */
const RETIRED = ['ptable', 'data-table', 'plugin-table', 'rows', 'asset',
                 'browse-card', 'schemcard', 'jvmcard', 'kpi']

function ruleNoRetiredBlocks() {
  for (const [cls, files] of usedClasses()) {
    const block = cls.split(/__|--/)[0]
    if (RETIRED.includes(block)) {
      problems.push(`已废弃的样式块 .${cls}（用 DataTable / Card）  ←  ${[...files].join(', ')}`)
    }
  }
}
```

- [ ] **Step 7: 跑构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 8: 人工核对（本计划最需要仔细看的一步）**

逐页看：插件库页、插件库抽屉、实例插件页、文件页、主机页、实例列表、核心库、Java 环境、数据库页、蓝图市场、新建实例向导。每一页确认：
- 行高变化后信息没有被挤断行；
- 长内容（六层深路径、很长的实例名、很长的插件描述）不撑破容器；
- 骨架屏行高与真实行高一致，加载完不跳动；
- 悬停态与选中态还在；
- 键盘能 Tab 进可点的行并用 Enter 触发；
- 明暗两种模式；1440 / 1200 / 1024 / 768 / 390 五个宽度，表格的「逐级丢列」媒体查询还生效。

觉得挤的表用 `density="roomy"`，不要改默认值。

- [ ] **Step 9: 提交**

```bash
git add web/src/components web/src/styles.css web/scripts/check-ui.mjs
git commit -m "$(cat <<'EOF'
五套表格收成一个组件

ptable / data-table / plugin-table / rows / asset：四种表头高度、五种行高、
三种内边距、两种圆角，而且没有任何地方写着该用哪一套——所以新页面用的是
它被复制自哪一页。

真正因表而异的只有列宽，所以列宽是唯一的 prop，其余（行高、内边距、悬停、
选中、圆角、空状态）都是同一张表。行高统一到 40，紧凑 34、宽松 48 两档留给
确实需要的表。

顺带给可点的行补了键盘操作：一个挂着 onClick 的 div 既 Tab 不到也按不动。
骨架屏的行高跟着一起改，否则加载完会跳一下。

加了守卫规则，防止这五个块名复活。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Nv69Xuhec4WXvPjz3u2Jyz
EOF
)"
```

---

### Task 7: 收尾——文档补齐、CHANGELOG、合并回 main

**Files:**
- Modify: `docs/design-system.md`（补「控件清单」四个条目与当前守卫规则）
- Modify: `CHANGELOG.md`
- Modify: `web/scripts/check-ui.mjs`（可选：加「一屏一个实心按钮」规则）

**Interfaces:**
- Consumes: Task 1–6 的全部产物
- Produces: 可合并的分支

- [ ] **Step 1: 补文档的控件清单**

在 `docs/design-system.md` 的「控件清单」小节，为 `Button` / `Badge` / `StatusDot` / `Card` / `DataTable` 各写一条：props 签名、什么时候用、**什么时候不要用**（例如：`Badge` 不是 `Chip`，后者是可点筛选；`Card` 的 `interactive` 只给整张卡可点的场合）。同时更新「哪些规则已经能被 CI 执行」小节，列出四条规则。

- [ ] **Step 2: 加「一屏一个实心按钮」守卫（报告模式）**

这条规则现在有 12 个组件违反，直接卡 CI 会挡住所有人。先做成只打印不失败的警告：

```js
/** Advisory: one filled button per screen. Not yet an error — twelve
 *  components still exceed it, and each needs a judgement call about which
 *  button is the primary one. Printed so the number goes down, not up. */
function adviseOnePrimaryPerFile() {
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    const n = (src.match(/variant="primary"/g) ?? []).length
    if (n > 1) console.warn(`  提示: ${path.relative(SRC, file)} 有 ${n} 个实心按钮`)
  }
}
```

在 `RULES` 跑完之后、判断 `problems.length` 之前调用它。

- [ ] **Step 3: 写 CHANGELOG**

在 `CHANGELOG.md` 的「未发布」小节加一条「修复」（只写用户看得见的部分，控件层重构对用户不可见）：

```markdown
### 修复

- **插件库、插件源设置里的一批小号按钮一直是全尺寸。** 它们要的样式类在样式表里
  根本不存在，所以那 16 个按钮比设计的大一圈，把所在的操作行撑高。同批修好的还有
  配置历史的差异行少了基础排版、警告徽章的描边着色失效、向导步骤说明文字字号不对。
  这类问题的共同原因是样式类名写错了不会报错，现在构建时会检查。
```

- [ ] **Step 4: 完整验证**

```bash
npm --prefix web run build
make lint
make test
```
Expected: 三条全过（后端未改动，`make lint && make test` 应当与改动前一致）

- [ ] **Step 5: 最后一次全量目视**

按 `frontend-design` skill 的自查清单走一遍：明暗两种模式 × 1440 / 1200 / 1024 / 768 / 390 五个宽度；折叠侧栏、打开抽屉、开着控制台的实例页这三处重点看。

- [ ] **Step 6: 推送功能分支**

```bash
git push -u origin claude/determined-ramanujan-a85yio
```

- [ ] **Step 7: 合并回 main（CLAUDE.md 工作流程第 4 条）**

```bash
git checkout main
git pull origin main
git merge claude/determined-ramanujan-a85yio
npm --prefix web run build && make lint && make test
git push origin main
git checkout claude/determined-ramanujan-a85yio
```

若 `main` 已经前进且有冲突，先把 `main` 合进功能分支解决完再来（第 5 条）；冲突无法自行判断就停下来说明，不要强推（第 6 条）。

---

## 附录 A：规范文稿要点（`System.dc.html`）

**颜色**——中性色带冷调；品牌绿只用于「主操作 / 当前位置 / 运行正常」三件事；语义色不做装饰，出现即代表状态。**一屏内彩色像素越少，状态越显眼；正常状态用中性色，只有异常才上色。**

**文字**——正文 13px（14px 会让表格撑得太散）；所有数字、路径、配置键一律等宽，便于纵向对齐比对。档位：16/600 页面标题、13/600 卡片标题、13/400 正文、12/400 说明、10.5/600/.08em 分组标签、12.5 mono、27/600 大数字。

**尺度**——4px 栅格（4·8·12·16·20·24·32）。圆角刻意偏小：4 徽章 / 6 控件 / 8 卡片 / 6 弹窗。控件高度全站只有 28 小 / 32 默认两档。表格行高 40（可切 34 紧凑 / 48 宽松）。侧栏 236 / 顶栏 56。阴影几乎不用，靠 1px 描边分层。

**组件**——同一区域最多一个实心按钮。破坏性操作在页面上只用红色描边；只有在二次确认弹窗里，最后那一下才允许实心红。空状态给虚线框 + 一句话 + 一个动作。骨架屏用于首屏；局部刷新只换数字，不闪整块。

**六条原则**——① 状态常驻：运行状态、TPS、内存、在线人数固定在顶栏，任何页面都能一眼看到、就地启停。② 两层作用域：侧栏先选实例、再选功能，「实例」与「面板/主机」两组分开。③ 改动可见、可撤：改了什么、原值多少、要不要重启全部就地标出，底部常驻保存条统一提交。④ 深色只给日志：终端是只读信息流，编辑器是工作区保持浅色，两者对比本身就是导航。⑤ 术语说人话：主标签中文、副行原始键名。⑥ 危险操作有摩擦：二次确认并说清后果，绝不与常规操作并排同样式摆放。

**本仓库的偏离**见 `docs/design-system.md`「本轮没落地的」小节。
