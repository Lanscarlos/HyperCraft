---
name: frontend-design
description: HyperCraft 面板（web/）的界面布局与视觉改动指南。当任务涉及调整页面布局、栅格、间距、响应式断点、导航壳、卡片/表格排布、主题令牌、动效或新增前端组件时使用；也用于「界面太挤/太空」「手机上错位」「这一屏重新排一下」这类布局优化请求。
---

# HyperCraft 前端布局优化

面板是 React 18 + TypeScript + Vite，源码在 `web/src`，**全部样式集中在一个 `web/src/styles.css`**（约 9000 行，按区块组织）。没有 CSS 框架、没有 CSS-in-JS、没有 Tailwind——不要引入。

## 动手之前

1. **先读令牌区**：`styles.css` 开头到约 215 行是全部设计令牌（颜色、圆角、阴影、时长、缓动、内容宽度）。改布局前先确认要用的值是否已有令牌。
2. **先读目标区块的注释**：这份代码库的注释解释的是「为什么是这个值」，很多看起来可以简化的写法是踩过坑之后的结果（例如 `.page__head > div:first-child` 用 `:first-child` 而不是 `> div`）。删改之前先读懂注释，注释描述的约束仍然成立就别动。
3. **确认改的是布局还是别的**：只调间距/换行/断点的改动不要顺手改配色和文案。

## 布局硬规则

### 页面骨架

- 每个面板级页面都套 `web/src/components/Page.tsx`，**不要再造一个页面框**。历史上有过三个页面框、三种最大宽度、两套滚动条归属，改错一个看起来「差不多对」——这是最糟的错误形态。
- 页面只有两种形态，由 `Page` 的 `wide` 属性决定：
  - 默认（散文/表单）：子元素 `max-width: var(--content-max)`（880px）。
  - `wide`（卡片、图表、瓦片）：子元素 `max-width: var(--content-max-wide)`（1440px）。
- 宽度限制加在 `.page > *` 上而不是滚动容器上，这样滚动条贴着窗口边缘而不是浮在屏幕中间。新增页面级容器时保持这一点。
- 壳层结构固定：`.app`（grid：侧栏 + 内容）→ `.shell`（flex 列：TopBar + 主区）→ `.main`（唯一带 padding 的内容区，`overflow: hidden`，滚动交给 `.page`）。

### 尺寸与间距

- 用现有令牌：`--radius-sm|--radius|--radius-lg|--radius-pill`、`--shadow-sm|--shadow|--shadow-lg`、`--content-max|--content-max-wide`。
- 间距没有令牌，是直接写的偶数 px。沿用邻近区块的节奏（页面级 `gap: 16px`、`.main` `gap: 12px`、卡片内 6/8/10/14px），不要凭空引入 13px、17px 这种值。
- 栅格一律用内在响应：`grid-template-columns: repeat(auto-fill, minmax(<下限>, 1fr))`，下限取卡片真正还能读的宽度。**不要为了排布加媒体查询**——能用 `auto-fill`/`auto-fit` 解决的就不加断点。
- 任何 flex/grid 子项只要里面可能有长文本或终端，就要有 `min-width: 0`（列方向是 `min-height: 0`），否则内容会把容器撑破。这是本仓库最常见的布局 bug。

### 响应式

断点是有意少而克制的，**不要新增一档断点来解决单个组件的问题**——先试内在响应。现有的几档：

| 断点 | 作用 |
| --- | --- |
| `max-width: 1240px` | 侧栏收窄到 224px，`.main` padding 收到 14/16px |
| `max-width: 1024px` | 侧栏离开栅格，变成覆盖式抽屉；`DRAWER_QUERY` 在 `App.tsx:59` 与之一致 |
| `min-width: 1280px` | 控制台右侧才出现 280px 快捷面板 |
| 其余 | 表格逐级丢列（1100/820px）、单组件收窄（900/780px） |

- **1024px 这一档在 CSS 和 JS 两处**：`styles.css` 的媒体查询和 `App.tsx` 的 `DRAWER_QUERY` 常量。改一处必须改另一处，否则抽屉的焦点管理和视觉状态会对不上。
- 手机是一等场景（服主在外面收到告警要能开机/看日志），窄屏下优先保住：状态、开关机、控制台。次要信息可以隐藏，但不要让它挤成两行错位。
- 窄屏 padding 用 `max(<值>, env(safe-area-inset-*))`，沿用 `.main` 的写法。
- 表格在窄屏用「逐级丢列」而不是横向滚动：丢的必须是别处已经能看到的信息（参考 `.ptable__row` 的注释）。

### 动效

- 时长只从 `--dur-1`(90ms) / `--dur`(140ms) / `--dur-3`(220ms) / `--dur-4`(300ms) 里选，**按动作的性质选而不是按距离**：指针反馈 → `--dur-1`；原地变状态 → `--dur`；原地出现/消失（菜单、气泡）→ `--dur-3`；横跨或覆盖屏幕（抽屉、模态、侧栏折叠）→ `--dur-4`。数据驱动的条形走位用 `--dur-data`(400ms) + `--ease-out`。
- 缓动：进场/移动用 `--ease`，退场用 `--ease-in`，数据用 `--ease-out`。
- 布局动画优先动**一个数**（`.app` 动的是 `grid-template-columns`），不要动 `width` + 一堆重排。
- CSS 里的动效已被 `prefers-reduced-motion` 区块统一关掉；但**在 JS 里写的动效（WAAPI、setTimeout）不受它管**，必须自己调用 `motion.ts` 的 `reducedMotion()`。

### 颜色与主题

- 只用命名令牌，不写裸 hex。表面层级 `--bg` → `--surface-1`（卡片、侧栏）→ `--surface-2/3/4`：需要读作「压在某物之上」就往上走一级，不要发明新值。
- 文字 `--text` / `--text-dim` / `--text-faint`；描边 `--border` / `--border-strong` / `--border-accent`。
- 两套令牌块（light / dark）是唯一出现 hex 的地方。新增令牌必须两个块都加，否则暗色下是空值。
- 终端是例外：两块画布（`--term-*` 服务器控制台、`--shell-*` 主机 shell）在两种模式下都保持深色，且**两者必须一眼可分**——这是防止把 `rm -rf` 敲进错误终端的唯一屏障。别统一它们的配色。
- 状态色 `--ok`/`--caution`/`--state-*` 有独立色相，且各自对所在表面校验过 3:1，不要换成主色。

### 可访问性

- 焦点环用 `--ring`（危险操作 `--ring-danger`），不要 `outline: none` 了事。
- 抽屉打开时焦点进入、关闭时归还给触发按钮（`App.tsx` 已实现，新增覆盖层照此办理）；离屏内容必须 `visibility: hidden`，不能只靠 `transform` 挪走——否则 Tab 还能走进去。
- 图标按钮要有 `aria-label`；纯装饰元素 `aria-hidden="true"`。

## 验证

改完必须跑：

```bash
npm --prefix web run build   # tsc -b + vite build，类型和构建一起过
```

跑不动依赖就先 `npm --prefix web install`。涉及后端一并改的，`make lint && make test`。

样式改动无法靠类型检查兜底，所以自查这几条：

- 明暗两种模式都看过（新令牌是不是两个块都加了）。
- 1440 / 1200 / 1024 / 768 / 390 宽度下都没有横向溢出、没有挤成错位的行。
- 折叠侧栏、打开抽屉、开着控制台的实例页——这三处最容易被布局改动波及。
- 长内容（六层深的路径、几百行日志、很长的实例名）不会撑破容器。

## 不要做的事

- 不要引入 CSS 框架、UI 组件库、CSS-in-JS，或把 `styles.css` 拆成多文件。
- 不要为了一处样式加 `!important` 或行内 `style`（除非是必须由 JS 计算的动态值）。
- 不要在没读注释的情况下「简化」看起来冗余的选择器或媒体查询。
- 不要顺手扩大改动范围：只做被要求的那部分布局优化。
