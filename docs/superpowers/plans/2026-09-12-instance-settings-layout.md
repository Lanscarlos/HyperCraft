# 实例设置页排版重构 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把实例设置页从「列数随窗口浮动的 auto-fit 栅格」改成「左说明 / 右字段两栏 + 按内容定宽的字段」，并把长说明折叠、保存与删除分离。

**Architecture:** `.panel--form` 从 CSS Grid 改成两栏 flex，靠 `flex: 999` 的悬殊 grow 比和 480px 的 flex-basis 实现无断点换行（换行点 704px）。字段宽度由四档修饰符类声明，不再由栅格分配。`LaunchSettings` 的 6 张卡收成 4 张，加一条 dirty 时才出现的 sticky 保存条和一个独立的 Danger Zone。

**Tech Stack:** React 18 + TypeScript 5.7 + Vite 6。样式全部在单文件 `web/src/styles.css`。无 CSS 框架、无 CSS-in-JS。

**Spec:** `docs/superpowers/specs/2026-09-12-instance-settings-layout-design.md`

## Global Constraints

这些约束适用于**每一个** Task，不再逐条重复：

- **样式只写在 `web/src/styles.css`**。不引 CSS 框架、组件库、CSS-in-JS，不拆分文件，不加 `!important`，不加行内 `style`（除非是必须由 JS 计算的动态值）。
- **不新增任何设计令牌。** 颜色、圆角、阴影、时长、缓动全部从 `styles.css` 开头令牌区取，不写裸 hex。本次用到的 `--danger-edge`、`--danger-soft`、`--dur-3`、`--ease`、`--ring`、`--ring-danger`、`--content-max` 均已在 light / dark 两块中存在。
- **不新增媒体查询断点。** 所有响应式靠内在响应（flex-wrap / auto-fit）实现。
- **不改文案的实质内容。** 折进 `FieldHelp` 的正文一字不动；留在外面的一行摘要允许压缩，措辞见各 Task 给出的确切字符串。
- **不改后端、不改 API、不改保存语义。** 本次是纯前端排版重构。
- **不动 `--term-*` / `--shell-*` 两块终端画布的配色。**
- **注释用英文，文档用中文。** 沿用所处文件的语言。注释解释**为什么**，不解释代码在做什么。
- **`min-width: 0`**（列方向 `min-height: 0`）加在任何可能装长文本的 flex/grid 子项上。这是本仓库最高频的布局 bug。
- **提交信息写为什么改**，并以下面两行结尾：

  ```
  Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
  ```

## 关于「测试」：这个仓库没有前端测试运行器

`web/package.json` 里没有 vitest、没有 jest、没有 testing-library，`web/src` 下没有任何 `*.test.*`。**本计划不引入测试运行器** —— 那是一次独立的、比本次重构更大的决定，不该夹带。

所以每个 Task 的验证由三层组成，按可靠性排序：

1. **`node scripts/check-ui.mjs`** —— 唯一能「先失败后通过」的自动检查。它是一个真实的规则引擎（`ruleNoUndefinedClasses` / `ruleIconButtonsAreLabelled` / `ruleNoSilentOverrides`）。Task 1 会给它加一条新规则，那条规则就是本次重构的回归测试。
2. **`tsc -b`** —— 类型、prop、未使用变量。
3. **人工观察** —— 每个 Task 给出**具体到「看哪里、期待看到什么」**的清单。样式改动无法靠类型检查兜底，这一层不能省。

命令：

```bash
npm --prefix web run check:ui   # 只跑守卫规则，快
npm --prefix web run build      # check:ui + tsc -b + vite build，完整
npm --prefix web run dev        # 起 :5173 看效果；后端另起 go run ./cmd/hypercraft
```

## 关于计划里的行号

**所有行号都是重构开始之前的**，取自 `main` 上 `eceec28` 那一版。每个 Task 都会让后面的行号平移，
所以行号只用来**定位大致位置**，真正的锚点是每步给出的代码内容 —— 按内容搜索，不要按行号跳。
若某处内容对不上，说明前一个 Task 改过它，先读当前文件再动手。

## File Structure

| 文件 | 责任 | 动作 |
| --- | --- | --- |
| `web/scripts/check-ui.mjs` | 设计系统守卫 | 修改：加 `ruleFormPanelsHaveColumns` |
| `web/src/styles.css` | 全部样式 | 修改：`.panel--form`、`.field*`、`.field-row`，新增 `.panel__aside` / `.panel__body` / `.field__help` / `.formbar` / `.panel--danger` / `.corepicker` / `.launchstrip` |
| `web/src/components/FieldHelp.tsx` | 折叠长说明的 `<details>` 封装 | **新建** |
| `web/src/components/LaunchSettings.tsx` | 实例设置页 | 修改：主体 |
| `web/src/components/InstanceCorePicker.tsx` | 从核心库复制核心 | 修改：外壳换成 `<details>` |
| `web/src/components/VelocityConfig.tsx` | 代理配置页 | 修改：4 张卡补包裹层 |
| `web/src/components/NewInstanceWizard.tsx` | 新建实例向导 | 修改：2 张卡补包裹层 |
| `docs/design-system.md` | 设计系统文档 | 修改：新增「表单」一节 |
| `CHANGELOG.md` | 更新日志 | 修改：「未发布」加一条 |

---

### Task 1: `.panel--form` 两栏骨架 + check-ui 守卫

把栅格换成两栏 flex，并加一条守卫规则钉住「每个 `.panel--form` 都必须有 `.panel__aside` 和 `.panel__body`」。这条规则就是本次重构唯一的自动回归测试，所以它先写、先失败。

**Files:**
- Modify: `web/scripts/check-ui.mjs`（在 `RULES` 数组前加新规则函数，`RULES` 在第 207 行）
- Modify: `web/src/styles.css:3719-3740`（`.panel--form` 及其整行白名单）
- Modify: `web/src/components/LaunchSettings.tsx`（5 处：413、487、716、779、882）
- Modify: `web/src/components/VelocityConfig.tsx`（4 处：423、437、502、516）
- Modify: `web/src/components/NewInstanceWizard.tsx`（2 处：1397、1532）

**Interfaces:**
- Produces：CSS 类 `.panel__aside`（左栏）与 `.panel__body`（右栏），后续所有 Task 都往 `.panel__body` 里放字段。
- Produces：check-ui 规则 `ruleFormPanelsHaveColumns`，Task 7 重排段落时会再次依赖它。

> **命名警告**：不要用 `.panel__head`。它已经是 `UsersPage` 在用的横向标题行（`styles.css:8570`，`display:flex; align-items:center; flex-wrap:wrap; gap:10px`）。复用会被 `ruleNoSilentOverrides` 判为重复声明 `display`/`flex-wrap`/`gap` 而使构建失败。

- [ ] **Step 1: 写守卫规则（这是本 Task 的「失败的测试」）**

在 `web/scripts/check-ui.mjs` 里，`ruleNoSilentOverrides` 函数之后、`adviseOnePrimaryPerFile` 之前，插入：

```js
/** Rule: a .panel--form declares both of its columns.
 *
 *  The panel is a two-column flex box — an aside carrying the section's title
 *  and one sentence of why, and a body carrying the fields. A section that
 *  forgets the wrappers does not break: it degrades into one flat column of
 *  full-width controls, which looks close enough to right that it survives
 *  review. That is exactly the failure this refactor set out to remove, so it
 *  is checked rather than remembered.
 *
 *  Counting occurrences per file rather than parsing JSX nesting: the three
 *  files that use .panel--form write one aside and one body per section, so
 *  the counts match when every section is wrapped and diverge the moment one
 *  is missed. A nesting parser would catch more and cost far more. */
function ruleFormPanelsHaveColumns() {
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    const panels = (src.match(/panel--form/g) ?? []).length
    if (panels === 0) continue
    const asides = (src.match(/panel__aside/g) ?? []).length
    const bodies = (src.match(/panel__body/g) ?? []).length
    if (asides === panels && bodies === panels) continue
    problems.push(
      `${path.relative(SRC, file)} 有 ${panels} 个 .panel--form，` +
        `但 ${asides} 个 .panel__aside、${bodies} 个 .panel__body —— 每个都要两栏包裹`,
    )
  }
}
```

再把它挂进 `RULES`（第 207 行）：

```js
const RULES = [
  ruleNoUndefinedClasses,
  ruleIconButtonsAreLabelled,
  ruleNoSilentOverrides,
  ruleFormPanelsHaveColumns,
]
```

- [ ] **Step 2: 跑守卫，确认它失败**

Run: `npm --prefix web run check:ui`

Expected: FAIL，`check-ui: 3 处问题`，三行分别指出 `components/LaunchSettings.tsx 有 5 个 .panel--form，但 0 个 .panel__aside、0 个 .panel__body`、`components/VelocityConfig.tsx …4…`、`components/NewInstanceWizard.tsx …2…`。

如果它**没有**失败，说明规则没挂进 `RULES` 或 `tsxFiles` 路径不对，先修规则再往下走。

- [ ] **Step 3: 改 CSS**

把 `web/src/styles.css` 第 3710–3740 行（从 `/* Given the extra width, a form's fields lay out in columns…` 那段注释开始，到整行白名单规则块结束）整体替换为：

```css
/* A settings form is two columns: an aside saying what this section is, and a
   body holding the fields. The width a wide window brings goes to the aside —
   it does not go into making a text input longer, which is what made this page
   read like a narrow form someone had dragged sideways.

   The wrap is intrinsic, not a breakpoint. 999 against 1 means the body takes
   essentially all the free space, which pins the aside at its 200px basis
   without a max-width it could not shed once wrapped; the body's 480px basis
   is what decides when to wrap, at 200 + 24 + 480 = 704px of card. Below that
   the body drops to its own line and the aside becomes an ordinary heading. */
.panel--form {
  display: flex;
  flex-wrap: wrap;
  gap: 14px 24px;
  align-items: start;
}

.panel--form > .panel__aside {
  flex: 1 1 200px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

/* Capped at the reading measure rather than at the card: past ~880px a row of
   fields stops being a form and becomes a search for the next control. The
   trailing space on a wide monitor is deliberate. */
.panel--form > .panel__body {
  flex: 999 1 480px;
  max-width: var(--content-max);
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* The aside's one sentence: why this section exists, not what the fields do. */
.panel__aside .panel__note {
  color: var(--text-faint);
  font-size: 11px;
  line-height: 1.6;
}
```

**注意**：被删掉的是那份 10 个选择器的 `grid-column: 1 / -1` 白名单（`.panel--form > .panel__title, > .panel__path, > .alert, > .chart-note, > .actions, > .field--full, > .segmented, > .launchcheck, > p, > .props-grid`）。右栏是纵向 flex，这些元素天然整行，白名单没有存在意义 —— 顺带修掉了 `.checkbox` 和 `.field-row` 漏出白名单导致被塞进窄格的 bug。

`.props-grid` 自己的 auto-fill 栅格（`styles.css:3753` 起）**保留不动**：server.properties 那几十条同形状的键值对是真正适合多列的内容。

- [ ] **Step 4: 包裹 11 个 section**

对全部 11 处做同一个机械变换。以 `LaunchSettings.tsx:413` 为例，改前：

```tsx
<section className="panel panel--form">
  <h3 className="panel__title">基本信息</h3>

  <label className="field">
```

改后：

```tsx
<section className="panel panel--form">
  <div className="panel__aside">
    <h3 className="panel__title">基本信息</h3>
    <p className="panel__note">这台服务器叫什么、文件放在哪、是什么服务端。</p>
  </div>

  <div className="panel__body">
    <label className="field">
```

并在该 `</section>` 之前补一个 `</div>` 关掉 `.panel__body`。

11 处的标题与该写的 `.panel__note` 一句话（**这些是新写的文案，不是从原文压缩的** —— 原文里没有段落级说明）：

| 文件:行 | 标题 | `.panel__note` |
| --- | --- | --- |
| `LaunchSettings.tsx:413` | 基本信息 | 这台服务器叫什么、文件放在哪、是什么服务端。 |
| `LaunchSettings.tsx:487` | 启动方式 | 面板拼出来的那条命令行：用哪个 Java、跑哪个 jar、给多少内存。 |
| `LaunchSettings.tsx:716` | 控制台 | 网页控制台怎么读服务端的输出、怎么把命令送回去。 |
| `LaunchSettings.tsx:779` | 进程管理 | 面板什么时候替你开服、什么时候替你重启、怎么停。 |
| `LaunchSettings.tsx:882` | 开服前检查 | 按下「启动」之前，面板能先看出来的问题。 |
| `VelocityConfig.tsx:423` | 基本设置 | 代理端监听在哪、对外显示什么。 |
| `VelocityConfig.tsx:437` | 玩家信息转发 | 子服看到的 IP 和 UUID 从哪来。 |
| `VelocityConfig.tsx:502` | 高级设置 | 默认值适用于绝大多数服。 |
| `VelocityConfig.tsx:516` | Query | 给服务器列表查询用的那个端口。 |
| `NewInstanceWizard.tsx:1397` | 服务器设置 | 开服前要先定下来的几项。 |
| `NewInstanceWizard.tsx:1532` | 代理端设置 | 代理端和普通服务端要填的不是一套。 |

**两处已有的重复文案要删**，否则同一句话在左栏和右栏各出现一次：

- `VelocityConfig.tsx:504`（高级设置里的 `<p className="muted">默认值适用于绝大多数服。不清楚作用的就别动。</p>`）—— 左栏 note 取前半句，这里改成只留 `<p className="muted">不清楚作用的就别动。</p>`。
- `VelocityConfig.tsx:439` 起那段「子服看到的 IP 和 UUID 从哪来。用 `modern` 的话…」—— 左栏 note 取第一句，这里删掉第一句、保留 `用 modern 的话…` 之后的全部内容。

**不要改标签层级**：`NewInstanceWizard` 的 `.panel__title` 是 `<h2>`，另两个文件是 `<h3>`。照原样搬进 `.panel__aside`。

- [ ] **Step 5: 跑守卫，确认它通过**

Run: `npm --prefix web run check:ui`

Expected: `check-ui: 通过`（可能带一行 `check-ui 提示: N 个组件有多于一个实心按钮`，那是既有的 advisory，不是本次引入的）。

- [ ] **Step 6: 跑完整构建**

Run: `npm --prefix web run build`

Expected: check-ui 通过 → `tsc -b` 无错 → vite build 成功。若 `tsc` 报 JSX 标签不匹配，是 Step 4 漏了某个 `</div>`。

- [ ] **Step 7: 人工看三处**

起 `npm --prefix web run dev`，看：

1. **实例 → 设置**：每张卡左边是标题 + 一行灰字，右边是字段。字段全部左对齐在同一条线上。宽屏右侧有留白 —— **这是对的，不要去填满它**。
2. **窗口从 1440 拖到 390**：在卡片内宽约 704px 处，右栏干净地折到左栏下面。左栏不应该在折之前挤成两行。
3. **代理配置页 + 新建实例向导**：两栏结构生效，没有出现左栏说明和右栏正文重复同一句话。

- [ ] **Step 8: 提交**

```bash
git add web/scripts/check-ui.mjs web/src/styles.css web/src/components/LaunchSettings.tsx web/src/components/VelocityConfig.tsx web/src/components/NewInstanceWizard.tsx
git commit -m "$(cat <<'EOF'
refactor(web): 表单卡片改成左说明 / 右字段两栏

.panel--form 的 auto-fit 栅格在 1440px 的实例 pane 里恰好算出 3 列 ×
~420px，于是「最大内存」和「服务端 jar」在同一视觉行上宽度差一倍；
.checkbox 和 .field-row 又漏出了 grid-column: 1 / -1 白名单，六行说明
被塞进 300px 窄格。列数由窗口宽度决定，字段宽度就无从谈起。

改成两栏 flex：flex: 999 对 1 的悬殊 grow 比把左栏按在 200px 的 basis
上（不需要 max-width，换行后它还得能独占整行），右栏 480px 的 basis
决定换行点 200 + 24 + 480 = 704px，不加断点。右栏封顶 --content-max，
宽屏尾部的留白是有意的。

整行白名单整份删除 —— 右栏是纵向 flex，一切天然整行。

check-ui 加 ruleFormPanelsHaveColumns 钉住两栏包裹：漏掉包裹不会报错，
只会退化成一列平铺，那种「看起来差不多对」正是这次要消灭的东西。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 2: 字段宽度四档

**Files:**
- Modify: `web/src/styles.css:2530`（`.field` 规则块之后插入四档）
- Modify: `web/src/components/LaunchSettings.tsx`（按下表给字段加类）

**Interfaces:**
- Consumes：Task 1 的 `.panel__body`（字段现在都住在里面）。
- Produces：`.field--num` / `.field--sm` / `.field--md` 三个类，Task 6 给「选择核心」用。

- [ ] **Step 1: 加 CSS**

在 `web/src/styles.css` 的 `.field-row` 规则块（第 2611 行）**之前**插入：

```css
/* How wide a field is says how much is expected in it. A four-digit megabyte
   count and a sixty-character filesystem path had the same instruction here —
   width: 100% on the input, and whatever the grid handed the cell — so the
   two came out the same size and the form read as noise. Four steps is the
   whole vocabulary; a field with no modifier takes the full measure, which is
   right for paths, argument lists and text areas. */
.field--num { max-width: 120px; }
.field--sm { max-width: 200px; }
.field--md { max-width: 380px; }
```

`.field` 本身不加 `max-width` —— 它的默认就是撑满 `.panel__body`，而 `.panel__body` 已经封顶在 880px。

- [ ] **Step 2: 给字段加类**

在 `LaunchSettings.tsx` 里按下表改 `className`：

| 字段（搜索锚点） | 现在 | 改成 |
| --- | --- | --- |
| 实例名称 | `className="field"` | `className="field field--md"` |
| 服务端类型 | `className="field"` | `className="field field--md"` |
| 游戏版本 | `className="field"` | `className="field field--sm"` |
| Java 环境 | `className="field"` | `className="field field--md"` |
| 服务端 jar | `className="field"` | `className="field field--md"` |
| 最小内存 (MB)（两处：`.field-row` 里和 `ArgFileMemory` 里） | `className="field"` | `className="field field--num"` |
| 最大内存 (MB)（同样两处） | `className="field"` | `className="field field--num"` |
| 输出编码 | `className="field"` | `className="field field--md"` |
| 停服命令 | `className="field"` | `className="field field--md"` |
| 停服超时 (秒) | `className="field"` | `className="field field--num"` |

**不加类**（保持整行）：服务器目录（`DirectoryField`，已有 `field--full`）、参数文件、JVM 参数、服务端参数。

**注意「最小内存 / 最大内存」在文件里出现两次**：一次在主表单的 `.field-row`（约 622 行），一次在 `ArgFileMemory` 组件里（约 1010 行）。两处都要改。

- [ ] **Step 3: 构建**

Run: `npm --prefix web run build`

Expected: 通过。`.field--num` / `--sm` / `--md` 都已在 CSS 中定义，`ruleNoUndefinedClasses` 不会报。

- [ ] **Step 4: 人工看**

实例 → 设置：

- 「停服超时」和「最大内存」是窄框（约 120px），不再和「服务端 jar」一样宽。
- 所有输入框**左边缘对齐在同一条线**。
- 390px 窄屏下 `--md` 的 380px 会被 `.panel__body` 的宽度截断，框不应该顶出卡片。若顶出，说明 `.panel__body` 的 `min-width: 0` 没生效。

- [ ] **Step 5: 提交**

```bash
git add web/src/styles.css web/src/components/LaunchSettings.tsx
git commit -m "$(cat <<'EOF'
refactor(web): 表单字段按内容定宽，不再由栅格分配

.field input 的 width: 100% 是无条件的，所以一个四位数的 MB 值和一条
六十字符的路径拿到同一条宽度指令，谁宽谁窄纯看格子分到多少。宽度本
该是语义：看一眼就知道这里该填多长。

四档就是全部词汇 —— num 120 / sm 200 / md 380 / 不加类整行。整行留给
路径、参数列表和文本域，它们是真的需要。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 3: `.field-row` 改成 wrap flex

**Files:**
- Modify: `web/src/styles.css:2611-2615`（`.field-row`）
- Modify: `web/src/styles.css:11007-11009`（1024px 媒体查询里的 `.field-row`）

**Interfaces:**
- Consumes：Task 2 的 `.field--num` / `--md`（并排的两个框现在自己有宽度了）。

- [ ] **Step 1: 改 CSS**

把 `web/src/styles.css:2611` 的：

```css
.field-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
```

替换为：

```css
/* Two fields that belong together on one line — a heap floor and its ceiling,
   a stop command and its timeout. It used to be a 1fr 1fr grid, which made
   both halves as wide as half the container no matter what went in them: the
   megabyte box came out the width of a filesystem path. Now each field brings
   its own width from the scale above and the row only decides they sit side by
   side, wrapping to a line each when there is no room. */
.field-row {
  display: flex;
  flex-wrap: wrap;
  gap: 14px 16px;
  align-items: start;
}
```

- [ ] **Step 2: 删掉那条媒体查询**

`web/src/styles.css:11007` 附近，在 `max-width: 1024px` 的块里删除：

```css
  .field-row {
    grid-template-columns: 1fr;
  }
```

`flex-wrap` 已经处理了窄屏落行，这条规则现在既无效（没有 grid 了）又误导。

- [ ] **Step 3: 构建**

Run: `npm --prefix web run build`

Expected: 通过。

- [ ] **Step 4: 人工看五处 —— 这一条改动波及最广**

`.field-row` 有 5 个消费方，逐个看：

1. **`LaunchSettings`**（实例 → 设置）：最小/最大内存是两个 120px 窄框并排；停服命令（380px）+ 停服超时（120px）并排且不再五五分。
2. **`PluginSourceSettings`**（设置 → 插件源）：字段没有宽度类，会撑满各自内容宽度后并排。确认没有挤成一行错位。
3. **`PluginSourceDialog`**（插件源对话框）：同上，注意对话框容器窄，多半直接落行。
4. **`PluginLibraryDrawer`**（插件库抽屉）：抽屉更窄，应该落行。
5. **`SchematicMarket`**（蓝图市场）：确认筛选行没有变形。

**2–5 这四处若视觉上比改动前更差**，给它们的字段补 `.field--md`，不要回退 `.field-row` 的定义。

- [ ] **Step 5: 提交**

```bash
git add web/src/styles.css
git commit -m "$(cat <<'EOF'
refactor(web): .field-row 从 1fr 1fr 改成 wrap flex

1fr 1fr 让两个字段各占容器一半，与它们装什么无关 —— 内存那个只需要
四位数字的框，被撑成一条文件系统路径的宽度。现在行只决定「并排」，
宽度由字段自己带来。

窄屏靠 flex-wrap 落行，1024 那条 grid-template-columns: 1fr 随之删除：
没有 grid 了，它既不生效又会误导下一个读到它的人。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 4: `FieldHelp` 组件 + 五处长说明折叠

**Files:**
- Create: `web/src/components/FieldHelp.tsx`
- Modify: `web/src/styles.css`（在 `.checkbox` 规则块之后，第 2650 行附近）
- Modify: `web/src/components/LaunchSettings.tsx`（五处）

**Interfaces:**
- Produces：`<FieldHelp summary?: string; children: ReactNode />`，默认 summary 是 `'为什么？'`。Task 6 不用它，Task 8 会在 design-system 里记它。

- [ ] **Step 1: 建组件**

Create `web/src/components/FieldHelp.tsx`:

```tsx
import type { ReactNode } from 'react'

interface Props {
  /** The one line that stays visible. Defaults to the question the body answers. */
  summary?: string
  children: ReactNode
}

/**
 * The paragraph behind a one-line hint.
 *
 * Several settings on this page need a paragraph to be settable safely — why
 * terminal mode changes what Tab completion answers, why the two colour flags
 * are wrong under it, what Forge does instead of a runnable jar. Left inline
 * they were six-line blocks sitting beside the control they described, and the
 * eye could not tell what was a field from what was a footnote.
 *
 * A <details> rather than a popover: no JS, no focus management, reachable by
 * keyboard, and unaffected by prefers-reduced-motion because there is nothing
 * animating. The text is not hidden — it is one keypress away, in the place it
 * belongs.
 */
export function FieldHelp({ summary = '为什么？', children }: Props) {
  return (
    <details className="field__help">
      <summary>{summary}</summary>
      <div className="field__help-body">{children}</div>
    </details>
  )
}
```

- [ ] **Step 2: 加样式**

在 `web/src/styles.css` 的 `.checkbox small` 规则块（第 2646 行）之后插入：

```css
/* The disclosure that a long hint folds into. Styled as a quiet link rather
   than a control: it is a footnote you may open, not a setting you must
   decide. */
.field__help > summary {
  cursor: pointer;
  color: var(--text-dim);
  font-size: 11px;
  width: fit-content;
  list-style: none;
}

.field__help > summary::-webkit-details-marker {
  display: none;
}

.field__help > summary::before {
  content: '›';
  display: inline-block;
  margin-right: 5px;
  transition: transform var(--dur) var(--ease);
}

.field__help[open] > summary::before {
  transform: rotate(90deg);
}

.field__help > summary:hover {
  color: var(--text);
}

.field__help > summary:focus-visible {
  outline: none;
  border-radius: var(--radius-sm);
  box-shadow: var(--ring);
}

.field__help-body {
  margin-top: 6px;
  color: var(--text-faint);
  font-size: 11px;
  line-height: 1.6;
}
```

- [ ] **Step 3: 折叠五处**

在 `LaunchSettings.tsx` 顶部加 `import { FieldHelp } from './FieldHelp'`，然后：

**3a. 服务端类型下面那段 `<p className="muted">`（约 467 行）** —— 改成：

```tsx
<p className="muted">
  面板先从目录和 jar 名认，认不出来才用这里填的。
  <FieldHelp summary="哪些认不出来？">
    <strong>Forge 这类认不出来</strong> —— 没有 jar 名可读，
    <code>version_history.json</code> 也只有 Paper 系才写。认不出来的后果很具体：
    mod 会被装进 <code>plugins/</code> 而不是 <code>mods/</code>，插件市场里每一条也都标成「未知」。
  </FieldHelp>
</p>
```

**3b. 参数文件的 `<small>`（约 570 行）** —— 改成：

```tsx
<small>
  一行一个，路径从实例目录算起，面板会按顺序拼成
  <code> java @第一个 @第二个 …</code>。
</small>
<FieldHelp summary="为什么 Forge 没有 jar？">
  Forge 和 NeoForge 从 1.17 起就没有可以直接跑的 jar 了，安装器留下的就是这两个文件
  —— 照 <code>run.sh</code> 里那行抄过来即可。
</FieldHelp>
```

**3c. 输出编码的 `<small>`（约 730 行）** —— 改成：

```tsx
<small>控制台按这个编码解读服务器输出、并按同样的编码发送命令。</small>
<FieldHelp summary="乱码了怎么办？">
  「自动」会让 JVM 用 UTF-8 输出，同时对不是 UTF-8 的行按系统编码兜底。用自己的脚本启动时，
  「让 JVM 用 UTF-8」这半件事要靠上面那个 <code>JAVA_TOOL_OPTIONS</code> 开关；
  那个关着、中文 Windows 上又乱码的话，这里改成 GBK 通常就好了。
</FieldHelp>
```

**3d. 使用终端模式的 checkbox（约 745 行）** —— `.checkbox` 是 flex 行，`align-items: flex-start`，直接塞 `<details>` 会被拉到复选框那一列。所以说明部分要包一层 `<div>`：

```tsx
<label className="checkbox">
  <input
    type="checkbox"
    checked={form.tty && ttySupported}
    disabled={!ttySupported}
    onChange={(e) => update('tty', e.target.checked)}
  />
  <div className="checkbox__text">
    <span>使用终端模式（推荐）</span>
    {ttySupported ? (
      <>
        <small>Tab 补全由正在运行的服务端回答，进度条不用等换行就能看到。</small>
        <FieldHelp>
          把服务器跑在伪终端上，就像你自己在 SSH 里开着它一样。这样 Tab 补全由
          <strong>正在运行的服务端</strong>回答（插件命令、真实玩家名都算数），
          进度条不用等换行就能看到，颜色也不需要强制。代价是终端只有一条流，
          stderr 不再单独标红。关掉则回到管道模式。
        </FieldHelp>
      </>
    ) : (
      <small>本系统没有可用的伪终端（Windows 需要 ConPTY），所有实例都以管道模式运行。</small>
    )}
  </div>
</label>
```

**3e. 强制彩色输出的 checkbox（约 762 行）** —— 同样的包裹：

```tsx
<label className="checkbox">
  <input
    type="checkbox"
    checked={form.forceColor}
    disabled={form.tty && ttySupported}
    onChange={(e) => update('forceColor', e.target.checked)}
  />
  <div className="checkbox__text">
    <span>强制彩色输出（推荐）</span>
    <small>仅在管道模式下有意义，终端模式下这两个参数不会被加上。</small>
    <FieldHelp>
      服务端只在检测到终端时才上色，所以管道模式会加上
      <code> -Dterminal.jline=false -Dterminal.ansi=true</code>，让网页控制台和
      cmd 里一样有颜色。终端模式下服务端本来就看得到终端，这两个参数不会被加上
      —— <code>terminal.jline=false</code> 恰好会关掉终端模式想要的那个补全。
      用自己的脚本启动时，这两个参数走
      <code> JAVA_TOOL_OPTIONS</code> 送进去，要在上面把那个开关留着。
    </FieldHelp>
  </div>
</label>
```

- [ ] **Step 4: 给 `.checkbox__text` 加样式**

在 `.checkbox small`（第 2646 行）之后、Step 2 加的 `.field__help` 之前插入：

```css
/* A checkbox whose explanation folds: the tick box stays on the left and
   everything that describes it — the label, the one-line hint, the disclosure
   — stacks in a column beside it. Without this wrapper the .checkbox row's
   align-items: flex-start pulls the <details> up into the tick box's column. */
.checkbox__text {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
```

- [ ] **Step 5: 构建**

Run: `npm --prefix web run build`

Expected: 通过。`.checkbox__text`、`.field__help`、`.field__help-body` 都已定义。若 `ruleNoUndefinedClasses` 报 `.field__help-body` 未定义，检查 Step 2 是否真的写进去了。

- [ ] **Step 6: 人工看**

实例 → 设置 → 控制台：

- 两个复选框各自只有一行说明，下面是一个「› 为什么？」。
- 点开后正文出现，箭头转 90°。**核对展开后的正文和改动前逐字一致。**
- Tab 能走到 summary，`Enter`/`Space` 能展开，焦点环可见。
- 「基本信息」段的「哪些认不出来？」、「启动方式」段（参数文件模式下）的「为什么 Forge 没有 jar？」同样可用。
- 切到暗色模式再看一遍：`--text-faint` 在暗色下仍然可读。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/FieldHelp.tsx web/src/styles.css web/src/components/LaunchSettings.tsx
git commit -m "$(cat <<'EOF'
feat(web): 长说明折进 FieldHelp，卡片里只留一行结论

「强制彩色输出」的解释有六行、带三段 <code>，和一个复选框平级摊在
卡片里。控件和脚注长得一样，眼睛就分不出哪些是要填的、哪些是要读的。

用 <details> 而不是气泡：无 JS、无焦点管理、键盘可达，也没有动效要
被 prefers-reduced-motion 关掉。文字没有被藏起来，只是退后一格。

复选框那两处要包一层 .checkbox__text —— .checkbox 是 align-items:
flex-start 的 flex 行，直接塞 <details> 会被拉进勾选框那一列。

折进去的正文一字未改。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 5: 开服前检查 → 页头下的状态条

**Files:**
- Modify: `web/src/components/LaunchSettings.tsx:882-920`（`LaunchCheckPanel`）
- Modify: `web/src/components/LaunchSettings.tsx:481-485`（调用点，移到 `PageHead` 之后）
- Modify: `web/src/styles.css`（`.launchcheck` 规则附近新增 `.launchstrip`）

**Interfaces:**
- Consumes：Task 1 的 `.panel__aside` / `.panel__body`（无问题分支不再是 `.panel--form`，所以这个 section 从守卫规则的计数里退出）。
- Produces：`.launchstrip` 类。

> **守卫规则联动**：本 Task 把 `LaunchCheckPanel` 的无问题分支从 `.panel--form` 改成 `.launchstrip`，有问题分支仍是 `.panel--form`。`ruleFormPanelsHaveColumns` 按**文件内计数**工作，所以 `LaunchSettings.tsx` 的 `panel--form` 计数会从 5 降到 5（有问题分支仍在）。**不要**让两个分支里出现不同数量的 `panel--form` 字面量 —— 计数规则数的是字符串出现次数，不是运行时渲染次数。下面的写法只保留一处 `panel--form` 字面量。

- [ ] **Step 1: 重写 `LaunchCheckPanel`**

把 `LaunchSettings.tsx:882-920` 的 `return (...)` 整体替换为：

```tsx
  // No findings is the normal case and does not deserve a card: one line under
  // the page head says the launch target is there, and gets out of the way.
  if (check.issues.length === 0) {
    return (
      <div className="launchstrip">
        <span className="launchstrip__dot" aria-hidden="true" />
        <span>
          没发现问题。
          {check.mode === 'argfile' ? '参数文件都在目录里。' : '核心和目录都对得上。'}
        </span>
        <button className="link" type="button" onClick={onRecheck}>
          重新检查
        </button>
      </div>
    )
  }

  return (
    <section className="panel panel--form">
      <div className="panel__aside">
        <h3 className="panel__title">开服前检查</h3>
        <p className="panel__note">按下「启动」之前，面板能先看出来的问题。</p>
      </div>

      <div className="panel__body">
        <ul className="launchcheck">
          {check.issues.map((issue) => (
            <li
              key={issue.code}
              className={`launchcheck__item launchcheck__item--${issue.level}`}
            >
              <strong className="launchcheck__level">{LEVEL_LABELS[issue.level]}</strong>
              <p className="launchcheck__text">{issue.message}</p>
              {/* The retired argv, shown only where it is the answer to the
                  issue above: somebody has to retype it into the form, and
                  this is the only place it still exists. */}
              {issue.code === 'needs-setup' &&
                legacyCommand.map((arg, at) => (
                  <code className="launchcheck__line" key={`${at}-${arg}`}>
                    {arg}
                  </code>
                ))}
            </li>
          ))}
        </ul>

        <div className="actions">
          <Button size="row" type="button" onClick={onRecheck}>
            重新检查
          </Button>
        </div>
      </div>
    </section>
  )
```

**不要用 `StatusDot`。** 它的 props 只有 `state: InstanceState` 和 `className?: string`（`web/src/components/StatusDot.tsx`）—— 没有 `tone`，而它的颜色由实例状态决定：`state="stopped"` 渲染的是灰点，不是这里要的绿点。这条状态条说的是「检查通过」，与实例开没开机无关，所以用一个纯装饰的 span，颜色在 Step 3 的 CSS 里给。

- [ ] **Step 2: 把调用点移到页头下**

`LaunchSettings.tsx:481` 那段 `<LaunchCheckPanel … />` 目前在 `InstanceCorePicker` 之后。剪切它，粘到 `<PageHead … />`（第 411 行）**之后、第一个 `<section className="panel panel--form">`（基本信息）之前**。

- [ ] **Step 3: 加 `.launchstrip` 样式**

在 `web/src/styles.css` 的 `.launchcheck` 规则块之前插入：

```css
/* The pre-flight check when it has nothing to report, which is most of the
   time. A card with a heading for "everything is fine" is a card the eye has
   to process on every visit to learn nothing; one line under the page head
   says the same thing and leaves the width to the form. */
.launchstrip {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--text-dim);
  font-size: 12px;
}

.launchstrip__dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: var(--radius-pill);
  background: var(--ok);
}

.launchstrip > span {
  min-width: 0;
}

.launchstrip > .link {
  margin-left: auto;
}
```

- [ ] **Step 4: 构建**

Run: `npm --prefix web run build`

Expected: 通过，`check-ui: 通过`。

- [ ] **Step 5: 人工看两种状态**

1. **正常实例**：页头「实例设置」下面一行灰字 + 绿点 +「没发现问题。核心和目录都对得上。」右端是「重新检查」。**不再有那张空卡片。**
2. **制造一个问题**：把「服务端 jar」改成一个不存在的文件名并保存 —— 状态条应该变回一张完整的两栏卡片，左边标题、右边问题列表 + 「重新检查」按钮。
3. 点「重新检查」两种状态下都要生效（`checkRev` 自增触发重查）。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/LaunchSettings.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
refactor(web): 开服前检查无问题时收成页头下的一行

绝大多数时候这张卡片说的是「没发现问题」，却占着和「启动方式」一样
的版面。一张标题写着「一切正常」的卡，每次进页面都要被眼睛处理一遍
才能得知它没有信息。

无问题 → 页头下一行状态条；有问题 → 仍然是完整的两栏卡片，问题列表
和 needs-setup 的 legacyCommand 回显一行没动。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 6: `InstanceCorePicker` 折进「启动方式」

本次风险最高的一条。组件的 props 契约、`onApplied` 回调链、409 覆盖确认流程都不动，只换外壳。

**Files:**
- Modify: `web/src/components/InstanceCorePicker.tsx`（`return` 的外壳）
- Modify: `web/src/components/LaunchSettings.tsx`（调用点移进「启动方式」的 `.panel__body`）
- Modify: `web/src/styles.css`（新增 `.corepicker`）

**Interfaces:**
- Consumes：`InstanceCorePicker` 的五个 prop 保持不变 —— `instance: InstanceStatus`、`cores: CoreController`、`onApplied: (fileName: string, instance: InstanceStatus, setAsJar: boolean) => void`、`onOpenLibrary: () => void`、`jarIgnored?: boolean`。
- Consumes：Task 2 的 `.field--md`（给「选择核心」用）。
- Consumes：Task 4 的 `.checkbox__text`（「复制后设为启动 jar」那个复选框要用同一套包裹）。

- [ ] **Step 1: 换外壳**

在 `InstanceCorePicker.tsx` 里，把 `const selected = …` 之后的整个 `return (...)` 替换为：

```tsx
  const selected = available.find((core) => core.id === coreId)

  return (
    // Controlled rather than a bare <details>: an error raised while it is
    // shut would otherwise be reported into a drawer nobody can see.
    <details
      className="corepicker"
      open={open || Boolean(error)}
      onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}
    >
      <summary>从核心库安装一个核心…</summary>

      <div className="corepicker__body">
        <div className="actions">
          <button className="link" type="button" onClick={onOpenLibrary}>
            管理核心库
          </button>
        </div>

        {available.length === 0 ? (
          <p className="chart-note">
            核心库还是空的。去「资源库 → 服务端核心」下载一个 Paper 或 Velocity，下载后在这里就能选；
            也可以自己把 jar 传到实例目录，在下面的「服务端 jar」里填文件名。
          </p>
        ) : (
          <>
            <p className="chart-note">
              从核心库挑一个复制到本实例目录 —— 新服装核心、老服换版本或者修一个坏掉的 jar，都走这里。
              核心只在核心库下载一次，开多少个服就复制多少份。
            </p>

            <label className="field field--md">
              <span>选择核心</span>
              <Select
                ariaLabel="选择核心"
                value={coreId}
                disabled={busy}
                options={available.map((core) => ({
                  value: core.id,
                  label: coreLabel(core),
                }))}
                onChange={setCoreId}
              />
              {selected && <small>将写入 <code>{selected.fileName}</code></small>}
            </label>

            <label className="checkbox">
              <input
                type="checkbox"
                checked={setAsJar}
                onChange={(e) => setSetAsJar(e.target.checked)}
                disabled={busy}
              />
              <div className="checkbox__text">
                <span>
                  复制后设为启动 jar
                  {selected?.kind === 'proxy' && '（代理端不吃 --nogui，会一并清空服务端参数）'}
                </span>
                {jarIgnored && (
                  <small>
                    这个实例用自己的脚本启动，「启动 jar」没人读 —— 勾了也只是记下来，
                    真正启动什么由脚本决定。
                  </small>
                )}
              </div>
            </label>

            {error && <div className="alert alert--error">{error}</div>}
            {status && <div className="alert alert--ok">{status}</div>}

            <div className="actions">
              <Button
                type="button"
                onClick={() => void apply(false)}
                disabled={busy || !coreId}
              >
                {busy ? '复制中…' : '复制到实例'}
              </Button>
              {selected?.kind !== 'proxy' && (
                <span className="file-toolbar__hint">
                  别忘了去「服务器配置」同意 EULA，否则服务端启动后会立刻退出。
                </span>
              )}
            </div>
          </>
        )}
      </div>
    </details>
  )
```

三处实质变化，都是有意的：

1. `<section className="panel">` → `<details className="corepicker">`，`.chart-head` 那行标题没有了，`管理核心库` 移进 body 顶部（收起时它本来就不该可达）。
2. **`复制到实例` 从 `variant="primary"` 降级为普通按钮** —— 这一屏的实心按钮现在是「保存设置」（Task 7），一屏一个实心按钮是 `docs/design-system.md` 的规矩，`check-ui` 的 `adviseOnePrimaryPerFile` 也在数它。
3. 复选框补 `.checkbox__text` 包裹层（Task 4 引入的），和页面其余部分一致。

- [ ] **Step 2: 加 `open` state**

在 `InstanceCorePicker.tsx` 的 `const [busy, setBusy] = useState(false)` 之后加：

```tsx
  // Shut by default: copying a core is a thing you do once, and this page is
  // mostly visited to change something else.
  const [open, setOpen] = useState(false)
```

- [ ] **Step 3: 加样式**

在 `web/src/styles.css` 的 `.field__help` 规则块（Task 4 加的）之后插入：

```css
/* Installing a core is a one-off action living inside the section whose field
   it writes — 服务端 jar. Shut by default, because a settings page is mostly
   opened to change something else. */
.corepicker {
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius);
  padding: 10px 12px;
}

.corepicker > summary {
  cursor: pointer;
  color: var(--text-dim);
  font-size: 12px;
  width: fit-content;
  list-style: none;
}

.corepicker > summary::-webkit-details-marker {
  display: none;
}

.corepicker > summary::before {
  content: '+';
  display: inline-block;
  margin-right: 6px;
}

.corepicker[open] > summary::before {
  content: '−';
}

.corepicker > summary:hover {
  color: var(--text);
}

.corepicker > summary:focus-visible {
  outline: none;
  border-radius: var(--radius-sm);
  box-shadow: var(--ring);
}

.corepicker__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  margin-top: 12px;
}
```

- [ ] **Step 4: 移动调用点**

在 `LaunchSettings.tsx` 里，把 `<InstanceCorePicker … />`（现在约 473–479 行，`基本信息` 那个 `</section>` 之后）整段剪切，粘到「启动方式」section 的 `.panel__body` 内、**「服务端 jar」那个 `<label>` 之后**（jar 字段是它写的东西，紧挨着放）。

注意「服务端 jar」在 `argFileMode === false` 的分支里。`InstanceCorePicker` 在两种模式下都有意义（`jarIgnored={argFileMode}` 就是为此存在的），所以**不要**把它放进那个三元分支里 —— 放在三元表达式 `{argFileMode ? (…) : (…)}` **之后**，`{importing && …}` 之前。

- [ ] **Step 5: 构建**

Run: `npm --prefix web run build`

Expected: 通过。若 `check-ui` 报 `LaunchSettings.tsx` 的 `panel--form` 计数对不上，说明 Step 4 剪切时误伤了 section 结构。

- [ ] **Step 6: 走一遍完整的复制流程 —— 这是本 Task 的验证重点**

1. 实例 → 设置 → 启动方式：「服务端 jar」下面有一个虚线框「+ 从核心库安装一个核心…」，**默认收起**。
2. 点开，选一个核心，勾「复制后设为启动 jar」，点「复制到实例」。成功后：绿色 alert 出现、上面的「服务端 jar」字段**自动变成新文件名**（`onCoreApplied` 回写）。
3. **再复制一次同一个核心** → 应该弹出「实例目录里已经有同名文件」确认框（409 分支）。点「覆盖」→ 成功。点取消 → 无事发生、无报错。
4. **测 error 自动展开**：收起抽屉，然后制造一次失败（例如停掉后端再点「复制到实例」）。抽屉应该**自动弹开**并显示红色 alert。
5. 切到「参数文件」启动方式，抽屉仍在，勾选框下面出现「这个实例用自己的脚本启动…」那段说明。
6. 「管理核心库」链接点得动，跳到核心库页。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/InstanceCorePicker.tsx web/src/components/LaunchSettings.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
refactor(web): 从核心库安装折进「启动方式」

它是一次性动作，不是一项设置，却占着一张和「控制台」同等份量的卡片，
夹在「基本信息」和「开服前检查」中间。而它真正写的就是下面那个
「服务端 jar」字段 —— 放在一起，收起来。

details 用受控的 open：收起时抛出的错误否则会被报进一个没人看得见的
抽屉里，所以 error 一出现就自动弹开。

「复制到实例」从实心降为普通按钮：这一屏的实心按钮是「保存设置」。

props 契约、onApplied 回调链、409 覆盖确认流程一行未动。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 7: sticky 保存条 + Danger Zone

**Files:**
- Modify: `web/src/components/LaunchSettings.tsx`（新增 dirty 计算；替换文件末尾的 `.actions` 块）
- Modify: `web/src/styles.css`（新增 `.formbar` / `.panel--danger`）

**Interfaces:**
- Consumes：`toInput(instance)`（第 33 行）、`fromLines`（第 64 行）、`form` / `jvmText` / `serverText` / `argFileText` / `argFileMode` 五个 state。

- [ ] **Step 1: 加 dirty 计算**

在 `LaunchSettings.tsx` 的 `const aikarNeedsEqualHeap = …`（约 293 行）之后插入：

```tsx
  // What 保存 would send, compared against what is stored. Built the same way
  // save() builds its payload, so the bar cannot claim there is nothing to
  // save while the button would still write something.
  //
  // Field by field rather than JSON.stringify on the two objects: the payload
  // is a spread with three keys re-assigned, and relying on a spread to
  // preserve key order for a string comparison is a bug waiting for someone to
  // reorder toInput().
  const stored = toInput(instance)
  const pending: InstanceInput = {
    ...form,
    jvmArgs: fromLines(jvmText),
    serverArgs: fromLines(serverText),
    argFiles: argFileMode ? fromLines(argFileText) : [],
  }
  const dirty = (Object.keys(stored) as (keyof InstanceInput)[]).some((key) => {
    const a = stored[key]
    const b = pending[key]
    if (Array.isArray(a) && Array.isArray(b)) {
      return a.length !== b.length || a.some((item, at) => item !== b[at])
    }
    return a !== b
  })
```

- [ ] **Step 2: 加「放弃」的重置函数**

紧接着插入：

```tsx
  // Back to what is stored. The same four setters the instance-change effect
  // uses, so 放弃 and switching instances land in exactly the same state.
  const revert = () => {
    setForm(toInput(instance))
    setJvmText(toLines(instance.jvmArgs ?? []))
    setServerText(toLines(instance.serverArgs ?? []))
    setArgFileText(toLines(instance.argFiles ?? []))
    setArgFileMode((instance.argFiles?.length ?? 0) > 0)
    setError(null)
    setStatus(null)
  }
```

- [ ] **Step 3: 替换文件末尾的操作行**

把 `LaunchSettings.tsx` 里这一段（约 822–845 行，`{error && …}` 到 `</div>` 结束）：

```tsx
      {error && <div className="alert alert--error">{error}</div>}
      {status && <div className="alert alert--ok">{status}</div>}

      <div className="actions">
        <Button variant="primary" type="submit" disabled={busy}>
          保存设置
        </Button>
        <div className="actions__danger">
          …
        </div>
      </div>
```

替换为：

```tsx
      {error && <div className="alert alert--error">{error}</div>}
      {status && <div className="alert alert--ok">{status}</div>}

      {dirty && (
        <div className="formbar">
          <span className="formbar__note">有未保存的改动</span>
          <Button size="row" type="button" onClick={revert} disabled={busy}>
            放弃
          </Button>
          <Button variant="primary" size="row" type="submit" disabled={busy}>
            {busy ? '保存中…' : '保存设置'}
          </Button>
        </div>
      )}

      <section className="panel panel--danger">
        <h3 className="panel__title">危险操作</h3>
        <p className="muted">
          这两个都不可撤销，面板没有为它们留回收站。服务器运行时都不可用。
        </p>
        <div className="actions">
          <Button
            type="button"
            onClick={() => remove(false)}
            disabled={busy || isLive(instance.state)}
          >
            从面板移除
          </Button>
          <Button
            variant="danger"
            type="button"
            onClick={() => remove(true)}
            disabled={busy || isLive(instance.state)}
          >
            删除实例及所有文件
          </Button>
        </div>
      </section>
```

**`.panel--danger` 不带 `panel--form`**，所以它不进 `ruleFormPanelsHaveColumns` 的计数，不需要两栏包裹。

- [ ] **Step 4: 加样式**

在 `web/src/styles.css` 的 `.actions__danger` 规则块（第 3769 行）之后插入：

```css
/* The save bar, which exists only while there is something to save. Sticky
   against .instance__pane--scroll (overflow-y: auto, so it is the scrollport)
   rather than fixed: fixed would leave the pane and sit wrong the moment the
   navigation drawer opens over it. */
.formbar {
  position: sticky;
  bottom: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius);
  box-shadow: var(--shadow-lg);
  /* Appearing in place, not crossing the screen. */
  animation: formbar-in var(--dur-3) var(--ease);
}

.formbar__note {
  flex: 1;
  min-width: 0;
  color: var(--text-dim);
  font-size: 12px;
}

@keyframes formbar-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
}

/* Destructive operations, in their own block at the foot of the page rather
   than sharing a row with 保存. They used to sit at the two ends of one
   .actions row — the hand going for 保存 and the hand going for 删除实例及
   所有文件 started from the same place. */
.panel--danger {
  border-color: var(--danger-edge);
  background: var(--danger-soft);
  background-image: none;
}
```

`background-image: none` 是必要的：`.panel` 有一层 `linear-gradient(180deg, var(--sheen), transparent 96px)` 的顶部高光，压在 `--danger-soft` 上会把红底洗掉。

- [ ] **Step 5: 确认 `prefers-reduced-motion` 覆盖了新动画**

`styles.css:11151` 的区块是通配覆盖（`*, *::before, *::after { animation-duration: 0.01ms !important; … }`），所以 `.formbar` 的 `formbar-in` 自动被关掉，**不需要额外改动**。

Run: `sed -n '11151,11160p' web/src/styles.css`

Expected: 看到 `*,` / `*::before,` / `*::after {` 三行和 `animation-duration: 0.01ms !important`。若它已经变成逐个选择器点名的写法（有人改过），就把 `.formbar` 加进去。

- [ ] **Step 6: 构建**

Run: `npm --prefix web run build`

Expected: 通过。`adviseOnePrimaryPerFile` 现在应该**不再**把 `LaunchSettings.tsx` 列进去（唯一的 `variant="primary"` 是保存按钮，Task 6 已经把「复制到实例」降级了）。若它仍在列表里，搜一下文件里还有几个 `variant="primary"`。

- [ ] **Step 7: 人工看**

1. **进页面不改任何东西** → 底部**没有**保存条，只有页尾的「危险操作」红框。
2. **改一个字段**（比如内存 +256）→ 保存条从下方浮出，停在窗口底部。往上滚，它跟着贴底。
3. **点「放弃」** → 字段回到原值，保存条消失。
4. **点「保存设置」** → 保存成功、绿色 alert 出现、保存条消失（因为 `onSaved` 更新了 `instance`，`dirty` 变 false）。
5. **服务器运行中**：「危险操作」里两个按钮都置灰。
6. **暗色模式**：`--danger-soft` 的红底不刺眼，`--danger-edge` 的边可见。
7. **390px**：保存条不遮住最后一个字段。若遮住，给 `.stack` 在窄屏下加底部内边距 —— **不要**改成 `position: fixed`。

- [ ] **Step 8: 提交**

```bash
git add web/src/components/LaunchSettings.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
feat(web): 保存条只在有改动时出现，危险操作单独成区

「保存设置」和「删除实例及所有文件」原本在同一个 .actions 行的两端，
两只手从同一个地方出发。GitHub 那种独立 Danger Zone 是对的。

保存条 sticky 在 .instance__pane--scroll 上而不是 fixed：fixed 会脱离
pane，导航抽屉盖上来的时候位置就错了。

dirty 逐字段比而不是 JSON.stringify 两个对象：payload 是一个 spread
加三个键重新赋值，指望 spread 保住键序来做字符串比较，是在等下一个
人重排 toInput() 的时候出 bug。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

### Task 8: 文档

**Files:**
- Modify: `docs/design-system.md`（新增「表单」一节）
- Modify: `CHANGELOG.md`（「未发布」小节）

- [ ] **Step 1: 给 design-system 加「表单」一节**

在 `docs/design-system.md` 里，紧接在讲按钮的那一节之后插入：

```markdown
## 表单

面板级表单一律是 `.panel.panel--form`，卡片内两栏：

```html
<section class="panel panel--form">
  <div class="panel__aside">
    <h3 class="panel__title">段标题</h3>
    <p class="panel__note">这一段是干什么的，一句话。</p>
  </div>
  <div class="panel__body">
    <!-- 字段 -->
  </div>
</section>
```

两个包裹层都是必需的，`npm --prefix web run check:ui` 的
`ruleFormPanelsHaveColumns` 会检查。漏掉不会报错，只会退化成一列平铺 ——
那种「看起来差不多对」正是这条规则要挡的。

**不要用 `.panel__head`**：那是 `UsersPage` 在用的横向标题行，另一回事。

分栏靠 `flex: 999` 对 `flex: 1` 的悬殊 grow 比实现，换行点是
`200 + 24 + 480 = 704px`，**没有媒体查询**。右栏封顶 `--content-max`
(880px)，宽屏上尾部的留白是有意的：宽度给导览，不给输入框。

### 字段宽度

宽度即语义 —— 看一眼就知道该填多长。四档，没有第五档：

| 类 | 宽度 | 用于 |
| --- | --- | --- |
| `.field--num` | 120px | 内存 MB、超时秒数、端口 |
| `.field--sm` | 200px | 版本号一类的短标识 |
| `.field--md` | 380px | 名称、下拉、文件名、命令 |
| 不加类 | 整行（封顶 880px） | 路径、参数列表、文本域 |

两个字段并排用 `.field-row`（wrap flex，各自带自己的宽度）。

### 说明文字

一句话以内的用 `<small>` 常驻。超过一句的折进 `<FieldHelp>`：

```tsx
<small>一行结论。</small>
<FieldHelp summary="为什么？">…完整解释…</FieldHelp>
```

复选框折叠说明时，说明部分要包一层 `.checkbox__text`，否则
`.checkbox` 的 `align-items: flex-start` 会把 `<details>` 拉进勾选框那一列。

### 保存与危险操作

- 保存条 `.formbar` 只在表单脏了的时候出现，sticky 贴底。
- 破坏性操作放页尾独立的 `.panel--danger`，**不与保存按钮同排**。
- 一屏一个实心按钮，那一个是「保存」。
```

- [ ] **Step 2: 加 CHANGELOG**

在 `CHANGELOG.md` 的 `## 未发布` 下面找到（或新建）`### 变更` 小节，加一条：

```markdown
- **实例设置页重排。** 每张卡片现在是左边一句「这一段是干什么的」、右边一列字段，字段按内容定宽 ——
  内存那种只填四位数的框不再和文件路径一样宽，所有输入框左边缘对齐在同一条线上。原先的排版把列数
  交给窗口宽度决定（1440px 下正好三列），于是同一行里会出现三种宽度的输入框，六行的说明被塞进
  三百像素的窄格。

  长解释折进「为什么？」，点开才展开，正文一字未改。「开服前检查」没问题时收成页头下的一行，不再
  占一张卡；「从核心库安装」折进「启动方式」，就在它写的那个「服务端 jar」字段旁边。

  「保存设置」改成只在有改动时浮出的贴底条，「从面板移除」和「删除实例及所有文件」挪进页尾独立的
  「危险操作」区 —— 它们原先和保存按钮在同一行的两端。
```

**只往「未发布」里加**，不要把它改成 `## [x.y.z] - 日期`（那会触发自动发版）。

**关于「四档宽度被滥用」的机检**：spec 的「文档」一节提到「尝试」把
`.field--num` 套在 `<textarea>` 上这类滥用接进 `check:ui`。**本计划不做这条。**
判断一个类挂在哪种元素上需要解析 JSX 嵌套，而 `check-ui.mjs` 现有的两个 JSX
辅助函数只能扫单个开标签；为一条低频规则引入一个嵌套解析器，成本远高于它挡住的
bug。`ruleFormPanelsHaveColumns`（Task 1）已经覆盖了本次真正会静默退化的那个失败。
这是一个有意的取舍，不是遗漏。

- [ ] **Step 3: 全量构建**

Run: `npm --prefix web run build && make lint && make test`

Expected: 三个都通过。后端一行未改，`make lint && make test` 应该和改动前一样。

- [ ] **Step 4: 最终人工验收 —— 五个宽度 × 两种模式**

按 `frontend-design` 的清单走完：

- [ ] 1440 / 1200 / 1024 / 768 / 390 五个宽度，无横向溢出、无错位
- [ ] 明暗两种模式都看过
- [ ] 折叠侧栏 → 设置页两栏比例正常
- [ ] 打开导航抽屉 → 保存条没有被盖住或错位
- [ ] 开着控制台的实例页 → 切到设置再切回来，控制台没被波及
- [ ] 长内容：六层深的目录路径、很长的实例名、几十行 JVM 参数，都不撑破卡片
- [ ] 连带页面：代理配置页、新建实例向导全流程
- [ ] `.field-row` 的四个消费方：插件源设置、插件源对话框、插件库抽屉、蓝图市场
- [ ] 功能不回归：保存、放弃、复制核心（含 409 覆盖）、写入 `user_jvm_args.txt`、从启动脚本导入、从面板移除、删除实例

- [ ] **Step 5: 提交**

```bash
git add docs/design-system.md CHANGELOG.md
git commit -m "$(cat <<'EOF'
docs: design-system 补表单排版规则，CHANGELOG 记设置页重排

design-system 此前有控件清单、有按钮规则，就是没有表单 —— 所以每加
一个字段都是一次即兴发挥，页面会持续变乱。把两栏骨架、四档字段宽度、
说明文字何时折叠、危险操作何时隔离写下来，能机检的那条已经在
check-ui 的 ruleFormPanelsHaveColumns 里。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01UmBjT4kw9XJ152TksQFnZs
EOF
)"
```

---

## 收尾

八个 Task 全部完成、`npm --prefix web run build` 与 `make lint && make test` 全绿、Step 4 的人工清单逐条走过之后，按 CLAUDE.md 的工作流程合并：

```bash
git push -u origin claude/keen-brown-wawwr0
git checkout main && git pull origin main
git merge claude/keen-brown-wawwr0
git push origin main
git checkout claude/keen-brown-wawwr0
```

若 `main` 已经前进，先把 `main` 合进功能分支解决冲突，别把冲突带上 `main`。合并被拒绝或出现无法自行判断的冲突时停下来说明，不要强推。
