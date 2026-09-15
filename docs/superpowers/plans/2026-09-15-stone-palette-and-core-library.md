# 苔石配色与资源库核心页收口 实施方案

> **For agentic workers:** REQUIRED SUB-SKILL: 用 superpowers:executing-plans 逐条实现。步骤用 `- [ ]` 勾选框跟踪。

**Goal:** 新增一套按设计文件配色的「苔石」方案并设为出厂默认、像素字默认关，同时把资源库核心页的空态、标题区和「添加核心」弹窗按设计稿收口。

**Architecture:** 配色沿用仓库既有的「一套配色 = 一张令牌表」机制，不引入第二套机制：把 `:root` 基础块换成苔石，原来与 `:root` 共用选择器的樱花补成一个完整的 `[data-palette='sakura']` 覆盖块，其余三套一个字不动。界面改动全部落在 `web/src/styles.css` 和四个组件文件里，不新增样式文件、不引框架。

**Tech Stack:** React 18 + TypeScript + Vite，单一样式文件 `web/src/styles.css`，检查手段只有 `tsc -b` 和 `npm --prefix web run check:ui`。

## Global Constraints

- 样式只许写进 `web/src/styles.css`，不拆文件、不引 CSS 框架或 CSS-in-JS。
- 颜色/圆角/阴影/时长/缓动一律走令牌，不写裸 hex；新增令牌 light / dark 两个块都要有。
- 代码注释用英文，文档和 CHANGELOG 用中文，沿用所处文件的语言。
- 现有四套配色（樱花 / 松绿 / 杏黄 / 碧蓝）的**取值一个都不许改**，只允许把樱花从 `:root` 搬进它自己的覆盖块。
- `--ok` / `--caution` / `--state-*` / error 一族在任何配色下都不移动。
- 两块终端画布 `--term-*`（服务器控制台）与 `--shell-*`（主机 shell）在明暗两种模式下都必须深色且明显不同色。
- 一屏一个实心按钮；`check-ui` 的 `rulePrimaryButtons` 会卡。
- 1024px 断点同时写在 `styles.css` 的媒体查询和 `App.tsx` 的 `DRAWER_QUERY`，改一处必须改另一处（本方案不动断点）。
- 每个任务结束前跑 `npm --prefix web run build`（内含 `tsc -b` 与 `check:ui`）并提交。

## 设计依据

取自用户提供的设计画板 `CoreLib.dc.html` / `CoreAdd.dc.html`，关键取值：

| 角色 | 设计稿 | 说明 |
| --- | --- | --- |
| 页面底 | `#f5f6f8` | 冷中性灰，比现有四套的近白低约 1.5 个亮度档 |
| 卡片 | `#ffffff` | 纯白 |
| 卡内递进 | `#fafbfc` | 悬停/次级面 |
| 描边 | `#e3e6eb` | 卡片边 |
| 强描边 | `#d2d7df` | 控件边 |
| 正文 | `#161a21` / `#5a6371` / `#6e7686` | 三级文字 |
| 强调 | `#12a06c` | 亮绿，用于指示 |
| 强调（按钮面） | `#0b7650` | 设计稿 `.btn.p` 用的是这个深一档的绿，白字才够对比 |
| 强调软底 | `#e6f5ee` / 边 `#b6e2cf` | |

## File Structure

| 文件 | 改什么 |
| --- | --- |
| `web/src/styles.css` | `:root` 基础块换成苔石；新增 `[data-palette='sakura']` 明暗两块还原樱花；新增苔石的 chart 令牌；空态/标题区/弹窗的规则 |
| `web/src/palette.ts` | `Palette` 联合类型加 `'stone'`，`PALETTES` 加一项并排到第一位，`DEFAULT` 改成 `'stone'` |
| `web/index.html` | 首屏内联脚本的 `page` 映射加 `stone`、默认回退改成 `stone`；`theme-color` 初值改成 `#f5f6f8`；像素字默认改成关 |
| `web/src/pixelfont.ts` | 默认值从「开」翻成「关」，存储语义反转 |
| `web/src/components/CoreLibraryPage.tsx` | 空态保留表格骨架、实心按钮归属挪到空态、标题区改成一行 |
| `web/src/components/Page.tsx` | 新增 `titleHidden`，让标题可以只留给无障碍 |
| `web/src/components/AddCoreDialog.tsx` | 三个步骤标签；筛选控件与构建表头移进各自卡片内部 |
| `CHANGELOG.md` | 「未发布」小节记用户可见的变化 |

---

### Task 1: 苔石配色的令牌表

**Files:**
- Modify: `web/src/styles.css:75-300`（`:root, [data-theme='light'][data-palette='sakura']` 基础块）
- Modify: `web/src/styles.css:305-410`（`[data-theme='dark'][data-palette='sakura']` 块）
- Modify: `web/src/styles.css:6389` 前后（chart 令牌的配色覆盖区）

**Interfaces:**
- Produces: `data-palette` 的新取值 `stone`；`:root` 基础块自此是苔石；樱花改由 `[data-theme='light'][data-palette='sakura']` / `[data-theme='dark'][data-palette='sakura']` 两个完整块提供。后续任务 2 依赖这两个选择器已存在。

- [ ] **Step 1: 先把樱花原样抄成两个独立块**

在 palettes 区（现有 `/* 松绿 — seed #63A002 */` 之前）插入两个块。**取值逐字从当前 `:root` 基础块和 `[data-theme='dark'][data-palette='sakura']` 块里抄**，抄的范围与松绿/杏黄/碧蓝三块覆盖的键集合一致：`--chevron`、M3 角色（`--primary` `--on-primary` `--primary-container` `--on-primary-container` `--surface` `--on-surface` `--on-surface-variant` `--outline` `--outline-variant` `--surface-lowest` `--surface-low` `--surface-container` `--surface-high` `--surface-highest` `--inverse-surface` `--inverse-on-surface` `--inverse-primary`）、`--wash-1/2`、`--border-strong` `--border-accent`、`--accent-soft` `--accent-glow` `--accent-face` `--accent-face-hover` `--accent-face-off` `--accent-ink-off`、`--sunken` `--inset` `--scrim` `--brand-face` `--mark-1` `--mark-2`、`--selection` `--on-selection`（dark 块才有）、`--term-bg` `--term-selection` `--term-black` `--term-bright-black` `--term-edge`、`--shell-bg` 一族、`--shadow-sm` `--shadow` `--shadow-lg`、`--ring`。

块头注释写：

```css
/* 樱花 — seed #8F4C38。出厂配色从 :root 挪到这里：默认现在是苔石，而
   「默认 = 没有 data-palette」这条规矩没变，所以樱花需要一张自己的表。
   取值与它当基础块时逐字一致，没有一处重新调过。 */
```

- [ ] **Step 2: 确认樱花搬家后没掉值**

Run: `npm --prefix web run build`
Expected: 通过。**然后人工比对**：`data-palette="sakura"` 下的页面与改动前截图一致——此时 `:root` 还没换，两处取值相同，页面应当**毫无变化**。这一步的意义就是先把搬家和换色分成两次可回滚的改动。

- [ ] **Step 3: 提交搬家**

```bash
git add web/src/styles.css
git commit -m "配色: 樱花从 :root 搬进自己的覆盖块

默认配色要换成苔石，而「默认 = 没有 data-palette 属性」这条机制不动，
所以樱花得先有一张属于自己的令牌表。取值逐字照抄，页面此刻应当零变化——
换色是下一个提交的事，分开是为了出问题时能只回滚一半。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 4: 把 `:root` 基础块的颜色换成苔石**

只改颜色一族；`--ok` / `--caution` / `--state-*` / error 一族 / `--radius-*` / `--content-max*` / `--dur-*` / `--ease*` / `--font-*` 原地不动。

```css
  --primary: #0b7650;
  --on-primary: #ffffff;
  --primary-container: #b6e2cf;
  --on-primary-container: #084e36;
  --error: #ba1a1a;
  --on-error-container: #93000a;
  --surface: #f5f6f8;
  --on-surface: #161a21;
  --on-surface-variant: #5a6371;
  --outline: #6e7686;
  --outline-variant: #e3e6eb;
  --surface-lowest: #ffffff;
  --surface-low: #fafbfc;
  --surface-container: #eef0f3;
  --surface-high: #e7eaee;
  --surface-highest: #e0e4e9;
  --inverse-surface: #262b33;
  --inverse-on-surface: #eef0f3;
  --inverse-primary: #7fd9b4;
```

工作名一族：

```css
  --wash-1: rgba(18, 160, 108, 0.05);
  --wash-2: rgba(90, 99, 113, 0.045);

  --border-strong: #d2d7df;
  --border-accent: #b6e2cf;

  --accent-soft: rgba(18, 160, 108, 0.1);
  --accent-glow: rgba(18, 160, 108, 0.22);
  --accent-face: linear-gradient(180deg, #14ab73, #0b7650);
  --accent-face-hover: linear-gradient(180deg, #17b97d, #0d8a5d);
  --accent-face-off: #d5e7de;
  --accent-ink-off: #8aa89a;

  --sheen: rgba(255, 255, 255, 0.6);
  --sunken: rgba(22, 26, 33, 0.035);
  --inset: inset 0 1px 2px rgba(22, 26, 33, 0.09);
  --scrim: rgba(16, 19, 24, 0.44);
  --stripe: rgba(255, 255, 255, 0.24);
  --brand-face: linear-gradient(150deg, #17b97d, #0a5f41);
  --mark-1: #35c48d;
  --mark-2: #0a5f41;

  --shadow-sm: 0 1px 2px rgba(16, 24, 40, 0.05);
  --shadow: 0 1px 3px rgba(16, 24, 40, 0.06), 0 8px 22px -12px rgba(16, 24, 40, 0.22);
  --shadow-lg: 0 4px 10px rgba(16, 24, 40, 0.08), 0 26px 52px -20px rgba(16, 24, 40, 0.3);

  --ring: 0 0 0 3px rgba(18, 160, 108, 0.2);
```

`--chevron` 的描边色换成 `%235a6371`。

终端与 shell：`--term-bg: var(--inverse-surface)` 这行保持不动（light 模式下它解析成 `#262b33`）。其余：

```css
  --term-selection: #34503f;
  --term-black: #7d8894;
  --term-bright-black: #9aa4af;
  --term-edge: #3a4048;
```

shell 必须离开 slate：苔石的冷灰暗面和 `#101820` 太近，而强调色已经是绿，所以 shell 走深紫，panel 里没有第二处是这个色。

```css
  /* 苔石的暗面本身就是冷灰，原来的 slate shell 会和控制台撞成一对近亲色。
     强调色又已经被绿占了，所以这套的 shell 走深紫——panel 里没有第二处
     是这个颜色，两块画布还是一眼能分开。 */
  --shell-bg: #1b1526;
  --shell-fg: #d6cfe3;
  --shell-cursor: #a992d8;
  --shell-selection: #3a2d52;
  --shell-edge: #362b47;
  --shell-accent: #a992d8;
```

块头注释改写成苔石的来历：

```css
/* --- M3 roles, light ----------------------------------------------
   苔石。取值来自面板的界面设计稿：冷中性灰的页面、纯白的卡、比 M3 默认硬
   一档的描边。这三样才是「纸放在桌上」那种层次的来源——原来四套配色的页面
   底都在 98% 亮度上下，和纯白卡差不到两个百分点，所以谁都分不开。
   强调色是设计稿的绿，按钮面用它深一档的 #0b7650：#12a06c 上压白字只有
   3:1，达不到正文对比。 */
```

- [ ] **Step 5: 把 `:root` 的 dark 对照块换成苔石**

`[data-theme='dark']`（现有第 305 行起那块，它此刻还挂着 sakura 的选择器——Step 1 已经把 sakura 抄走了，这里把选择器里的 `[data-palette='sakura']` 去掉，只留 `:root[data-theme='dark']` 对应的那半）。取值：

```css
  --primary: #6fdcae;
  --on-primary: #003824;
  --primary-container: #005237;
  --on-primary-container: #8bf8c8;
  --surface: #121417;
  --on-surface: #e0e4e9;
  --on-surface-variant: #c2c7d0;
  --outline: #8b929d;
  --outline-variant: #3c424b;
  --surface-lowest: #0d0f11;
  --surface-low: #181b1f;
  --surface-container: #1c2025;
  --surface-high: #262b31;
  --surface-highest: #31363d;
  --inverse-surface: #e0e4e9;
  --inverse-on-surface: #2b3036;
  --inverse-primary: #0b7650;

  --wash-1: rgba(111, 220, 174, 0.07);
  --wash-2: rgba(138, 160, 190, 0.05);

  --border-strong: #4e5661;
  --border-accent: #3f7a62;

  --accent-soft: rgba(111, 220, 174, 0.12);
  --accent-glow: rgba(111, 220, 174, 0.26);
  --accent-face: linear-gradient(180deg, #6fdcae, #57c397);
  --accent-face-hover: linear-gradient(180deg, #7ee5ba, #63d0a4);
  --accent-face-off: #2a3a33;
  --accent-ink-off: #7d8f86;

  --scrim: rgba(4, 6, 8, 0.7);
  --brand-face: linear-gradient(150deg, #6fdcae, #3d8a68);
  --mark-1: #35c48d;
  --mark-2: #0a5f41;
  --selection: rgba(111, 220, 174, 0.28);
  --on-selection: #e8fff5;

  --term-bg: var(--surface-lowest);
  --term-selection: #2e4a3c;
  --term-black: #6b7580;
  --term-bright-black: #8b929d;

  --shell-bg: #150f1f;

  --ring: 0 0 0 3px rgba(111, 220, 174, 0.24);
```

`--chevron` 描边换成 `%23c2c7d0`。dark 块里凡是樱花原本重定义过的键都要给出苔石的值——照着改动前那块的键集合逐个过一遍，一个不漏。

- [ ] **Step 6: 补苔石的 chart 令牌**

`styles.css` 第 6389 行前后那组 `--chart-grid` / `--chart-reference` 覆盖块里，苔石是新的默认，所以它的值应当写在**基础** chart 令牌那里而不是覆盖块（与松绿/杏黄/碧蓝相反）。找到基础 `--chart-grid` / `--chart-reference` 的声明处，改成：

```css
  --chart-grid: #e3e6eb;
  --chart-reference: #b9c0ca;
```

dark 基础块对应改成：

```css
  --chart-grid: #2b3036;
  --chart-reference: #555f6a;
```

同时在覆盖区补上樱花的两块（原来的基础值）：

```css
[data-theme='light'][data-palette='sakura'] {
  --chart-grid: <改动前的 light 基础值>;
  --chart-reference: <改动前的 light 基础值>;
}

[data-theme='dark'][data-palette='sakura'] {
  --chart-grid: <改动前的 dark 基础值>;
  --chart-reference: <改动前的 dark 基础值>;
}
```

- [ ] **Step 7: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工确认清单（缺一不可）：
- 明暗两种模式下，卡片（白）与页面底（灰）分得开；
- 控制台 `--term-bg` 与主机 shell `--shell-bg` 两块画布在明暗下都深色且**明显不同色**；
- 外观设置里五个色板都画得出自己的颜色，切到樱花/松绿/杏黄/碧蓝与改动前一致；
- 实例页上「运行中」的绿点和主按钮的绿**能分开**（这是照搬设计稿绿的已知代价，先看实际效果）。

- [ ] **Step 8: 提交**

```bash
git add web/src/styles.css
git commit -m "配色: :root 换成苔石——冷灰页面、纯白卡、硬一档的描边

原来四套配色的页面底都压在 98% 亮度上下，和纯白卡片差不到两个百分点，
所以「纸放在桌上」的层次在哪套下都立不起来，空页面尤其糊成一片。苔石按
面板设计稿取值：页面 #f5f6f8、卡 #ffffff、描边 #e3e6eb。

按钮面用 #0b7650 而不是设计稿标的 #12a06c：后者压白字只有 3:1。
shell 画布这套走深紫，因为苔石的冷灰暗面和原来的 slate 已经是近亲色，
而绿被强调色占了——两块终端不能长得像，这是防止把命令敲错地方的唯一屏障。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: 把苔石注册成可选项与默认值

**Files:**
- Modify: `web/src/palette.ts:26-44`
- Modify: `web/index.html`（`theme-color` meta 与首屏内联脚本的 `page` 映射）

**Interfaces:**
- Consumes: Task 1 建立的 `data-palette='sakura'` 覆盖块。
- Produces: `Palette` 类型含 `'stone'`；`DEFAULT === 'stone'`；`PALETTES` 五项，苔石在首位。

- [ ] **Step 1: 改 `palette.ts`**

```ts
export type Palette = 'stone' | 'sakura' | 'green' | 'yellow' | 'blue'
```

```ts
/** Display order, default first. 苔石 is the design system's own scheme; the
 *  four after it are Material Theme Builder schemes grown from one seed each.
 *  Their token tables are in styles.css. */
export const PALETTES: PaletteInfo[] = [
  { id: 'stone', name: '苔石', note: '出厂的冷灰配绿。' },
  { id: 'sakura', name: '樱花', note: '暖粉色。' },
  { id: 'green', name: '松绿', note: '草木调的黄绿。' },
  { id: 'yellow', name: '杏黄', note: '五套里最亮的。' },
  { id: 'blue', name: '碧蓝', note: '冷调的蓝。' },
]

const DEFAULT: Palette = 'stone'
```

顺手把模块头注释里「四套」改成「五套」，并把「which of the four token tables」改成 five。碧蓝的 note 原本写「唯一一套冷色」——苔石也是冷的，所以这句必须改，否则设置页会说一句假话。

- [ ] **Step 2: 改 `index.html` 的首屏脚本**

`page` 映射加一项并让 stone 成为回退：

```js
        var page = {
          stone: ['#f5f6f8', '#121417'],
          sakura: ['#fff8f6', '#1a1110'],
          green: ['#f9faef', '#12140e'],
          yellow: ['#fff9ee', '#15130b'],
          blue: ['#f9f9ff', '#111318'],
        }
```

回退与写属性两行：

```js
        if (!Object.prototype.hasOwnProperty.call(page, palette)) palette = 'stone'
        if (palette !== 'stone') document.documentElement.dataset.palette = palette
```

把上面那条注释里的「Eight values」改成「Ten values」。

- [ ] **Step 3: 改 `theme-color` 初值**

```html
    <meta name="theme-color" content="#f5f6f8" />
```

- [ ] **Step 4: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工确认：
- 清掉 `localStorage` 的 `hypercraft.palette` 后首屏直接是苔石，**没有**先画一帧粉再跳；
- 外观设置里五个色板齐全，切换后刷新能记住；
- 切到樱花后刷新，浏览器地址栏颜色跟着变成 `#fff8f6`。

- [ ] **Step 5: 提交**

```bash
git add web/src/palette.ts web/index.html
git commit -m "配色: 苔石入列并接任出厂默认

「默认 = 没有 data-palette」这条机制没动，只是那个默认换了一套表。
首屏脚本里的页面底色映射跟着加一项——它是样式表到位之前唯一知道页面该是
什么颜色的地方，少一项就会在冷启动时闪一帧白。

碧蓝的说明从「唯一一套冷色」改掉：苔石也是冷的，留着就是句假话。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: 像素字默认改成关

**Files:**
- Modify: `web/src/pixelfont.ts:24-60`
- Modify: `web/index.html`（内联脚本尾部的像素字分支）

**Interfaces:**
- Produces: `readPref()` 在没有存储键时返回 `'off'`；存储键的语义从「我关掉了」翻成「我开起来了」。

- [ ] **Step 1: 改 `pixelfont.ts`**

```ts
export function readPref(): PixelFontPref {
  try {
    // Anything that is not the explicit opt-in reads as off, including a value
    // left behind by an older or newer build.
    return window.localStorage.getItem(STORAGE_KEY) === 'on' ? 'on' : 'off'
  } catch {
    // Private mode, or storage disabled by policy. The default is still off.
    return 'off'
  }
}
```

```ts
export function applyPref(pref: PixelFontPref): void {
  try {
    if (pref === 'off') window.localStorage.removeItem(STORAGE_KEY)
    else window.localStorage.setItem(STORAGE_KEY, 'on')
  } catch {
    /* nothing to remember it with; the session still switches */
  }
  // ... crossFade 部分不动
```

模块头注释里「On is the default, and it is stored as the *absence* of a key」那段改写成：

```
 * Off is the default now, and it is stored as the *absence* of a key. The face
 * is the panel's signature, but it is a display face: set as running prose it
 * loses the rhythm a sentence is read by, and most of this panel's prose is
 * CJK that Monocraft has no glyphs for anyway. So it is opt-in, and the stored
 * value only ever means "I went and turned this on".
```

- [ ] **Step 2: 改 `index.html`**

```js
        // The pixel face, resolved here for the same reason: it changes the
        // metrics of every line on the page, so deciding it after the bundle
        // loads means watching the whole panel re-typeset itself. Off is the
        // default and is stored as no key at all; the key and that rule match
        // pixelfont.ts.
        var pixel = false
        try {
          pixel = localStorage.getItem('hypercraft.pixelfont') === 'on'
        } catch (e) {
          /* storage disabled; the default stands */
        }
        if (pixel) document.documentElement.dataset.pixelFont = 'on'
```

- [ ] **Step 3: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工确认：清掉 `hypercraft.pixelfont` 后首屏是比例字；在外观设置里打开像素字 → 刷新 → 仍然是像素字；再关掉 → 刷新 → 比例字。

- [ ] **Step 4: 提交**

```bash
git add web/src/pixelfont.ts web/index.html
git commit -m "外观: 像素字改成默认关，开关本身不动

像素字是这个面板的辨识度，但它是个标题字：整句中文散文用等宽像素字排，
字距被强行撑成均匀的方块，句子就没有轻重了。而面板里绝大多数正文是 CJK,
Monocraft 本来也没有这些字形，落到 Fusion Pixel 上。

所以改成按需打开。存储语义跟着反转：键存在 = 我主动开了。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: 空态保留表格骨架，实心按钮归属挪到空态

**Files:**
- Modify: `web/src/components/CoreLibraryPage.tsx:181-330`
- Modify: `web/src/styles.css`（`.empty--inline` 邻近，新增表体内空态的排布）

**Interfaces:**
- Consumes: 既有的 `EmptyState`（`inline` 变体）、`ResourceTable`、`DataTableEmpty`、`ResourceHint`。
- Produces: 无新导出。

- [ ] **Step 1: 删掉整屏二选一的分支，让表格常驻**

把 `stored.length === 0 && !downloading ? (<EmptyState .../>) : (<>...</>)` 这个三元**整个拆掉**，改成：工具栏只在货架上有东西时出现（对着空货架筛选是噪音），表格框和表头永远在。

```tsx
      {cores.error && <div className="alert">{cores.error}</div>}

      {stored.length > 0 && (
        <Toolbar>
          {/* 原样保留：搜索框、chips、count、Select */}
        </Toolbar>
      )}

      <ResourceTable heads={{ compat: '支持 MC', requires: '运行要求' }} label="核心库">
        {downloading && job && (
          <ResourcePendingRow
            title={`${job.title}${job.subtitle ? ` ${job.subtitle}` : ''}`}
            fileName={job.fileName}
            downloaded={job.downloaded}
            total={job.total}
          />
        )}
        {stored.length === 0 && !downloading ? (
          <EmptyState
            inline
            title="还没有任何核心"
            action={
              <>
                <Button variant="primary" type="button" onClick={() => setAdding('catalogue')}>
                  添加核心
                </Button>
                <Button type="button" onClick={() => setAdding('upload')}>
                  上传 jar
                </Button>
              </>
            }
          >
            下载一个 Paper 或 Velocity，或者把自己的 jar（Forge、Fabric、整合包自带的服务端）上传进来。
            核心下好之后，新建实例时选它就行。
          </EmptyState>
        ) : shown.length === 0 ? (
          <DataTableEmpty>没有符合条件的核心。</DataTableEmpty>
        ) : (
          shown.map((core) => (
            <ResourceRow key={core.id} entry={entryOf(core, () => void remove(core))} />
          ))
        )}
      </ResourceTable>
```

`rescards` 那段（`StorageHygiene` + `ResourceHint`）从三元里提出来，放在表格之后常驻。`StorageHygiene` 本来就带 `idle.length > 0` 的条件；`ResourceHint` 解释的正是「手动丢进目录的 jar」，空货架上更该看见。

在这段上方补一条注释，说清为什么空的时候也留着表：

```tsx
      {/* The table frame stays when the shelf is empty. It used to be swapped
          out for a placard, which left a 1440px band with three lines floating
          in the middle of it and told nobody what this page will look like once
          it has something. The header is the page's promise: these are the
          columns a core is judged by. Only the toolbar goes — filtering nothing
          is noise. */}
```

- [ ] **Step 2: 头部的实心按钮降成描边**

`actions` 里的「添加核心」去掉 `variant="primary"`：

```tsx
          <Button type="button" onClick={() => setAdding('catalogue')}>
            添加核心
          </Button>
```

这正是 `check-ui` 的 `rulePrimaryButtons` 写明的分工——「an empty state's call to action」留实心，「the standing entrance in a page or card head」不是。全文件 `variant="primary"` 的计数仍为 1。

- [ ] **Step 3: 给表体里的空态一条样式**

`.empty--inline` 现在是左对齐、无框、`padding: 14px 2px`。落在表格体里需要更像「一行占位的内容」而不是贴边的小字。在 `.empty--inline` 之后新增：

```css
/* The inline form standing in for a table's rows, rather than for a form's
   fields. It gets the row padding the table's own rows have, so the sentence
   starts under the first column's heading instead of at the frame's edge. */
.rtable .empty--inline {
  padding: 26px 16px;
}
```

若 `ResourceTable` 的根类名不是 `.rtable`，先 `grep -n "className" web/src/components/ResourceTable.tsx` 确认真实类名再写——**不要凭记忆写**，`check-ui` 只查 tsx 里用到的类是否在 CSS 里有定义，反向的死规则查不出来，但写错了就是不生效。

- [ ] **Step 4: 验证**

Run: `npm --prefix web run build`
Expected: 通过，且 `check:ui` 不报 `rulePrimaryButtons`。

人工确认：
- 空货架下看得见表头「核心 / 版本 / 支持 MC / 运行要求 / 体积 / …」，下面一行提示与两个按钮，实心的是「添加核心」；
- 有核心时工具栏回来，行为与改动前一致；
- 下载进行中（`downloading`）时 `ResourcePendingRow` 仍在表里；
- 筛选到 0 条时走的是「没有符合条件的核心。」而不是空货架文案。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/CoreLibraryPage.tsx web/src/styles.css
git commit -m "资源库: 空货架也留着表格骨架，实心按钮归空态所有

空的时候整块结构被换成一个占满 1440px 的虚线盒，里面三行字浮在正中央,
左右各空四百像素——既没有信息，也没告诉人这页有东西之后长什么样。表头
就是这一页的承诺：这些是判断一个核心的列。所以框和表头留下,只有工具栏
走（对着空货架筛选是噪音）。

顺手把实心按钮还给空态。check-ui 那条规则本来就写着「空态的号召」留实心、
「页头那个常驻入口」不留,之前正好反着来。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: 标题区合并成一条

**Files:**
- Modify: `web/src/components/Page.tsx`（`HeadProps` 加 `titleHidden`）
- Modify: `web/src/components/CoreLibraryPage.tsx`（传 `titleHidden`）
- Modify: `web/src/styles.css`（`.page__head` 一族）

**Interfaces:**
- Produces: `Page` / `PageHead` 新增可选 `titleHidden?: boolean`。传了它，`<h1>` 仍然渲染但只留给读屏软件（`.sr-only`），视觉上的标题由顶栏面包屑承担。

- [ ] **Step 1: 给 `Page.tsx` 加 `titleHidden`**

`HeadProps` 里加：

```tsx
  /** The title is already on screen — the top bar's breadcrumb ends on this
   *  page's name — so the heading stays for the document outline and for a
   *  screen reader, and gives its row back to the page. A page that is not
   *  reached through a breadcrumb must not set this: it would then have no
   *  visible title at all. */
  titleHidden?: boolean
```

`PageHead` 签名接上它，`<h1>` 那行改成：

```tsx
        {title !== undefined && (
          <h1 className={titleHidden ? 'sr-only' : undefined}>
            {title}
            {count !== undefined && <span className="page__count">{count}</span>}
          </h1>
        )}
```

`Props extends HeadProps`，所以 `Page` 只要在解构和透传里各加一处 `titleHidden`。

- [ ] **Step 2: 让隐藏了标题的头部收成一行**

`.page__head` 现在是 `align-items: flex-start` 的换行 flex，左列 `.page__heading` 是 `flex-direction: column; gap: 8px`。标题隐藏之后，列里只剩 facts 一行，应当与右侧 actions 垂直居中对齐。新增：

```css
/* A head whose title is hidden is one row: the page's facts on the left, its
   actions on the right, centred against each other. The column's 8px gap and
   its top alignment are both for stacking a title over a lead, and neither
   applies when the title is not drawn. */
.page__head:has(> .page__heading > h1.sr-only) {
  align-items: center;
}
```

`:has()` 在目标浏览器里可用（Vite 的 target 是现代浏览器），但若要更保险，改成由 `PageHead` 在 `<header>` 上加一个 `page__head--bare` 修饰类，CSS 写 `.page__head--bare { align-items: center; }`。**选后者**——`check-ui` 能验到修饰类的存在，`:has()` 它验不到。

于是 `PageHead` 的 `<header>` 改成：

```tsx
    <header className={`page__head${titleHidden ? ' page__head--bare' : ''}`}>
```

CSS：

```css
/* A head whose title is hidden is one row: the page's facts on the left, its
   actions on the right, centred against each other. .page__heading's column
   gap and the head's top alignment are both for stacking a title over a lead,
   and neither applies when the title is not drawn. */
.page__head--bare {
  align-items: center;
}
```

- [ ] **Step 3: 核心页用上它**

`CoreLibraryPage.tsx` 的 `<Page>` 加一个 `titleHidden`：

```tsx
    <Page
      wide
      titleHidden
      title="服务端核心"
```

`title` **保留**——它是这一页的 `<h1>`，读屏软件和文档大纲都靠它，只是不再画第二遍。

- [ ] **Step 4: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工确认：
- 核心页顶部只剩一条：左边「还是空的」+ 路径 chip，右边两个按钮，垂直居中；
- 顶栏仍读「资源库 / 服务端核心」，这是现在唯一一处可见标题；
- 用读屏软件或 devtools 的 Accessibility 面板确认 `<h1>服务端核心</h1>` 仍在；
- 其它页（Java 运行时、数据库、插件、建筑与地图、主机、面板设置）**没传 `titleHidden`，一律不受影响**——逐个点开确认标题还在。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/Page.tsx web/src/components/CoreLibraryPage.tsx web/src/styles.css
git commit -m "资源库: 核心页的标题只说一遍

顶栏面包屑已经以「资源库 / 服务端核心」收尾,页面下面又来一个 23px 的 h1
说同一句话,两层加起来吃掉顶部约九十像素,而且两层都没填满。

h1 留着——读屏软件和文档大纲要它——只是不再画第二遍,头部收成一行:
左边这页的事实,右边这页的动作。titleHidden 是可选的,而且只有能从面包屑
走到的页面才该传:不然就成了一页没有可见标题。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: 「添加核心」弹窗按设计稿收口

**Files:**
- Modify: `web/src/components/AddCoreDialog.tsx:187-345`
- Modify: `web/src/styles.css:16942-17110`

**Interfaces:**
- Consumes: 无。
- Produces: 新类名 `addcore__step`（步骤标签）、`addcore__pane`（版本/构建各自的卡）、`addcore__panehead`（卡内的工具条与表头）。

- [ ] **Step 1: 给三块区域补上可见的步骤标签**

设计稿用 `1 · 选择核心` / `2 · 版本` / `3 · 构建` 三个小号大写标签立起阅读顺序，实现里三块只有 `aria-label`，屏幕上什么都没有——这是这屏「乱」的主因。

`addcore__kinds` 那个 `<section>` 改成：

```tsx
            <section className="addcore__kinds" aria-label="核心类型">
              <span className="addcore__step">1 · 选择核心</span>
              <div className="addcore__kindgrid">
                {/* 原来的 kindcard 列表原样搬进来 */}
              </div>
            </section>
```

版本一侧：

```tsx
                <section className="addcore__versions" aria-label="版本">
                  <span className="addcore__step">2 · 版本</span>
                  <div className="addcore__pane">
                    <div className="addcore__panehead">
                      {/* 原来的 addcore__tools 内容：input-slim + checkbox */}
                    </div>
                    <div className="addcore__vlist">
                      {/* 原样 */}
                    </div>
                  </div>
                </section>
```

构建一侧：

```tsx
                <section className="addcore__builds" aria-label="构建">
                  <span className="addcore__step">3 · 构建</span>
                  <div className="addcore__pane">
                    <div className="addcore__bhead">
                      {/* 原样：构建号 / 变更摘要 / 发布时间 / 体积 */}
                    </div>
                    <div className="addcore__blist">
                      {/* 原样 */}
                    </div>
                  </div>
                </section>
```

- [ ] **Step 2: 把工具条与表头收进卡片里**

这是「乱」的第二个来源：`.addcore__tools` 和 `.addcore__bhead` 现在是列表盒的兄弟，浮在弹窗背景上，两侧基线还对不齐。设计稿里两者都在卡片内部，带自己的底色和一条下边框。

`.addcore__pane` 接管原来 `.addcore__vlist` / `.addcore__blist` 的框：

```css
/* The version list and the build list each sit in a card, and their controls
   sit inside that card rather than beside it. The filter box and the build
   table's header used to be siblings of the list, floating on the dialog's
   own background with nothing to line up against — two panes whose tops
   disagreed by however tall each one's controls happened to be. */
.addcore__pane {
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}

.addcore__panehead {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: none;
  padding: 8px;
  border-bottom: 1px solid var(--border);
}
```

`.addcore__vlist` / `.addcore__blist` 去掉自己的 `background` / `border` / `border-radius`，只留滚动与内边距：

```css
.addcore__vlist,
.addcore__blist {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-height: 0;
  padding: 4px;
  overflow-y: auto;
}
```

（`max-height: 300px` 从这里移到 `.addcore__pane`，否则框会被内容撑破。）

`.addcore__bhead` 补上表头该有的底色与下边框，并去掉它作为独立块的外边距：

```css
.addcore__bhead {
  flex: none;
  padding: 0 14px;
  background: var(--surface-2);
  border-bottom: 1px solid var(--border);
  color: var(--text-faint);
  /* 原有的 grid-template-columns / height / font-size 一族保持不动 */
}
```

`.addcore__tools` 这条规则删掉——它的职责被 `.addcore__panehead` 接走了。**删之前先 `grep -n "addcore__tools" web/src/` 确认没有第二个使用处。**

- [ ] **Step 3: 步骤标签与类型网格的样式**

```css
/* The three steps, named. Small caps-ish, quiet, and above each pane — the
   dialog is one decision made in three places and they have to read in an
   order. */
.addcore__step {
  display: block;
  margin-bottom: 7px;
  color: var(--text-faint);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
}

.addcore__kindgrid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 8px;
}
```

`.addcore__kinds` 自己不再是网格（网格移进了 `.addcore__kindgrid`），改成：

```css
.addcore__kinds {
  min-width: 0;
}
```

`.addcore__versions, .addcore__builds` 的 `gap: 8px` 去掉（标签自己有 `margin-bottom`），其余不动。

- [ ] **Step 4: 窄屏那段跟着改**

`styles.css:17248` 起的媒体查询里有 `.addcore__pick` 与 `.addcore__bhead` 的窄屏规则。`.addcore__bhead > :nth-child(3)` 那条（窄屏隐藏「发布时间」列）保持不动；确认 `.addcore__pick` 在窄屏改成单列后，两个 `.addcore__pane` 仍各自有高度上限而不是无限长。

- [ ] **Step 5: 验证**

Run: `npm --prefix web run build`
Expected: 通过，`check:ui` 不报未定义类名（新加的 `addcore__step` / `addcore__pane` / `addcore__panehead` / `addcore__kindgrid` 都已在 CSS 里定义）。

人工确认：
- 三个步骤标签在屏幕上可见，阅读顺序立得住；
- 筛选框与「显示预览版与快照」在版本卡**内部**，上方带下边框；
- 构建表头在构建卡**内部**，有 `--surface-2` 底色和下边框，与左侧版本卡的顶边**齐平**；
- 明暗两种模式各看一遍；
- 1440 / 1200 / 1024 / 768 / 390 五个宽度下弹窗无横向溢出，窄屏下两栏堆叠且各自能滚。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/AddCoreDialog.tsx web/src/styles.css
git commit -m "添加核心: 控件收进各自的卡,三个步骤给出名字

筛选框和构建表头原本是列表盒的兄弟节点,浮在弹窗背景上没有任何东西可以
对齐,于是左右两栏的顶边随各自控件的高度错开——这是这屏看着乱的主因。
设计稿里这两样都在卡片内部,带自己的底色和一条下边框,所以照做。

三块区域此前只有 aria-label,屏幕上什么标记都没有。一个「哪个核心、哪个
版本、哪个构建」的决定摊在三处,没有序号就没有阅读顺序。

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: CHANGELOG 与全局回归

**Files:**
- Modify: `CHANGELOG.md`（「未发布」小节）

- [ ] **Step 1: 写 CHANGELOG**

往「未发布」小节加（**不要**新建版本号小节——改成 `## [x.y.z] - 日期` 并进 main 会自动发版）：

```markdown
- 新增「苔石」配色并设为出厂默认：冷灰页面、纯白卡片、比原来硬一档的描边，
  卡片和页面终于分得开。原有的樱花、松绿、杏黄、碧蓝四套取值一个没动，
  在外观设置里照常可选。
- 像素字体改成默认关闭。开关仍在外观设置里，想要的随时开回来。
- 服务端核心页在没有核心时也保留表格框和表头，不再整块换成一个占满宽度的
  空盒子；「添加核心」的实心按钮移到空状态里。
- 服务端核心页的标题不再和顶栏面包屑重复说一遍，页头收成一行。
- 「添加核心」对话框：版本筛选和构建表头移进各自的卡片内部，两栏顶边对齐，
  并给「选择核心 / 版本 / 构建」三步加上了可见的序号标签。
```

- [ ] **Step 2: 全局回归**

Run: `npm --prefix web run build`
Expected: 通过。

Run: `make lint && make test`
Expected: 通过（后端未改动，这一步是确认没有连带破坏）。

人工走查（CLAUDE.md 指定的三处高危区 + 本次涉及的页面），明暗两种模式 × 1440 / 1200 / 1024 / 768 / 390 五个宽度：
- 折叠侧栏；
- 打开抽屉（<1024px）；
- 开着控制台的实例页——顺带确认 `--term-*` 与 `--shell-*` 两块画布在苔石下仍是深色且不同色；
- 资源库五个页面（服务端核心、Java 运行时、数据库、插件、建筑与地图）；
- 外观设置的五个色板。

- [ ] **Step 3: 提交**

```bash
git add CHANGELOG.md
git commit -m "文档: 记下苔石配色、像素字默认与核心页的改动

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage**

| 用户的要求 | 对应任务 |
| --- | --- |
| 新增一套配色，不动现有四套 | Task 1（樱花先原样搬家，再换 `:root`） |
| 新配色设为默认 | Task 2 |
| 强调色照搬设计文件的绿 | Task 1 Step 4（按钮面取深一档的 `#0b7650`，理由已写明） |
| 像素字默认关 | Task 3 |
| 空态保留骨架、实心按钮归空态 | Task 4 |
| 标题区二选一 | Task 5 |
| 弹窗「特别乱」 | Task 6 |

**已知遗留**

- 照搬设计稿的绿意味着强调色与 `--ok`（运行中）同族。这是用户在知情下选的，Task 1 Step 7 把「两者能不能分开」列进了人工确认项。若实际看下来分不开，后续单独提一个改强调色的改动，不在本方案范围内。
- 本方案只给资源库核心页传 `titleHidden`。其余页面的标题区仍是两层。是否推广到全部页面，等这一页的效果确认之后再定。

**类型一致性**

- `Palette` 联合类型（Task 2）与 `page` 映射的键（Task 2 Step 2）都是五个且拼写一致：`stone` `sakura` `green` `yellow` `blue`。
- `titleHidden` 在 `HeadProps` 定义、`PageHead` 解构、`Page` 解构与透传、`CoreLibraryPage` 传入，四处同名（Task 5）。
- 新类名 `addcore__step` / `addcore__pane` / `addcore__panehead` / `addcore__kindgrid` 在 Task 6 的 tsx 与 css 两侧成对出现。
