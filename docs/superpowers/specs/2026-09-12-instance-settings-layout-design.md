# 实例设置页排版重构 设计文档

日期：2026-09-12
状态：已确认，待实施

## 背景

实例的「设置」页（`LaunchSettings.tsx`，1081 行）在宽屏上读起来是散的：输入框宽的宽、窄的窄，说明段落和控件平级混排，找不到「哪些要填、哪些要读」。这不是错觉，是当前栅格的确定性后果。

### 根因：表单栅格的列数是浮动的

`styles.css:3719`：

```css
.panel--form {
  grid-template-columns: repeat(auto-fit, minmax(min(340px, 100%), 1fr));
  gap: 14px 20px;
}
```

设置页跑在 `.instance__pane` 里，`.stack` 被抬到 `--content-max-wide`（1440px，`styles.css:3091`）。卡片内可用约 1290px，`auto-fit` 算下来正好 **3 列 × ~420px**：

- `启动方式`：Java 环境 / 服务端 jar / (最小内存+最大内存)
- `控制台`：输出编码 / 使用终端模式 / 强制彩色输出
- `进程管理`：面板启动时自动启动 / 崩溃后自动重启 / (停服命令+停服超时)

由此派生四个具体毛病：

1. **两套定宽系统嵌在一起。** `.field-row` 是写死的 `1fr 1fr`（`styles.css:2611`），但它自己只占 `auto-fit` 的一个格子。于是「最大内存」约 200px，隔壁「服务端 jar」约 430px —— 同一视觉行上三个输入框，三种宽度。

2. **`.checkbox` 和 `.field-row` 漏出了整行白名单。** `styles.css:3729-3738` 列了 10 个选择器吃 `grid-column: 1 / -1`，两者都不在其中。结果「强制彩色输出」那段六行、带三个 `<code>` 的说明被塞进 300px 的窄格 —— 这是截图里最刺眼的一块。

3. **输入框宽度和内容无关。** `.field input { width: 100% }` 是无条件的（`styles.css:2557`）。四位数的 MB 值和六十字符的路径拿到同一条宽度指令，谁宽谁窄纯看格子分到多少。

4. **说明文字和控件抢同一条视觉流。** 六行解释和一个复选框是平级的 grid item，没有主次。

### 结构性原因

`docs/design-system.md` 里**没有任何表单排版规则** —— 控件清单有、按钮规则有、表单没有。每加一个字段都是一次即兴发挥，所以它会持续变乱。

### 业界参照

设置页几乎没有主流 SaaS 用「有多宽就排多少列」。常见的是四种：单列固定测量（Stripe、Linear、Vercel、GitHub Settings）；左说明右表单（Shopify Polaris、Tailwind UI）；左标签右控件（AWS、GCP，已过时且窄屏差）；每段一卡各自保存（Vercel、Railway）。三条几乎人人遵守的细则正是本页缺的：**宽度即语义**（端口 ~100px、名称 ~320px、路径整行）、**长解释不常驻**、**危险操作单独隔离**。

## 目标

把实例设置页改成「左说明 / 右字段」的定宽表单：字段按内容定宽、左边缘全部对齐，长说明折叠，保存与删除分离。

**非目标**（明确不做）：

- 不引 CSS 框架、组件库、CSS-in-JS，不拆分 `styles.css`。
- 不新增媒体查询断点。分栏靠内在响应实现。
- 不新增页面宽度形态。沿用 `--content-max` / `--content-max-wide` 两个既有令牌。
- 不改文案的**实质内容**。折叠后留在外面的一行摘要允许从原文压缩而来，但折进 `FieldHelp` 的正文一字不动，不新增、不删减、不改立场。
- 不改后端、不改 API、不改保存语义。
- 不新增任何设计令牌。所需的颜色、时长、缓动全部已存在。
- 不动 `--term-*` / `--shell-*` 两块终端画布的配色。

## 已确认的四个决定

1. **方向：左说明栏 + 单列定宽字段**（而非钉死 N 列栅格）。
2. **范围全开**：栅格与字段宽度、长说明折叠、底部操作栏与 Danger Zone、段落顺序，四项都做。
3. **`InstanceCorePicker` 折进「启动方式」**，不再是独立卡片。
4. **表单排版规则写进 `docs/design-system.md`**，能机检的部分接进 `check:ui`。

## 架构

### 1. `.panel--form`：从 grid 改成两栏 flex

```css
.panel--form {
  display: flex;
  flex-wrap: wrap;
  gap: 14px 24px;
  align-items: start;
}

/* 左栏：段标题 + 一句话说明。不设 max-width —— 下面那个悬殊的 grow 比
   已经把它按在 flex-basis 上了，而换行之后它必须能独占整行。 */
.panel--form > .panel__head {
  flex: 1 1 200px;
}

/* 右栏：字段。999 的 grow 让剩余空间几乎全部归它，左栏因此稳定停在
   ~200px；480px 的 basis 决定了换行点。880px 封顶是阅读测量，不是
   容器宽度 —— 宽屏上尾部留白是有意的。 */
.panel--form > .form-body {
  flex: 999 1 480px;
  max-width: var(--content-max);
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
```

**换行点是算出来的，不是拍的**：`200 + 24 + 480 = 704px`。卡片内宽大于 704px 时两栏并列；小于时右栏折到下一行，左栏独占一行退化成普通标题。**一条媒体查询都不加**，符合 `frontend-design` 的「栅格优先内在响应」。

`min-width: 0` 在右栏是必需的：里面装长路径和 `<textarea>`，没有它会撑破卡片（仓库最高频的布局 bug）。

卡片本身仍是 pane 的 `--content-max-wide`，和控制台 / 文件 / 监控各页保持同一条边。右栏 880 封顶后尾部留约 100–250px 空白 —— 这是 Shopify Admin / Stripe Settings 的做法：宽屏用来放导览，不是用来拉长输入框。

**连带删除**：`styles.css:3729-3738` 那份 10 个选择器的 `grid-column: 1 / -1` 白名单整份删掉 —— 右栏是纵向 flex，一切天然整行。`.checkbox` 漏白名单的 bug 随之消失，不需要单独修。

### 2. 字段宽度：四档，声明式

```css
.field { max-width: 100%; }
.field--num { max-width: 120px; }   /* 内存 MB、停服超时秒 */
.field--sm  { max-width: 200px; }   /* 游戏版本 */
.field--md  { max-width: 380px; }   /* 名称、下拉、jar、停服命令 */
/* 不加类 = 整行：目录、参数文本域、说明段落 */
```

所有字段左边缘落在同一条线上，宽度只由「预期填多长」决定。`.field--full` 保留，语义收窄成「就是要整行」（它不再需要参与任何 grid 跨列计算）。

字段归档：

| 字段 | 档位 |
| --- | --- |
| 实例名称 | `--md` |
| 服务器目录 | 整行 |
| 服务端类型 / 游戏版本 | `--md` / `--sm` |
| Java 环境（含自定义路径输入） | `--md` |
| 服务端 jar | `--md` |
| 最小内存 / 最大内存 | `--num` |
| 参数文件 / JVM 参数 / 服务端参数 | 整行 |
| 输出编码 | `--md` |
| 停服命令 / 停服超时 | `--md` / `--num` |
| 选择核心 | `--md` |

### 3. `.field-row`：从 `1fr 1fr` 改成 wrap flex

```css
.field-row {
  display: flex;
  flex-wrap: wrap;
  gap: 14px 16px;
  align-items: start;
}
```

最小/最大内存变成两个 120px 的框并排，而不是各占容器一半 —— 这是「内存框和 jar 框一样宽」的直接修复。窄屏靠 `flex-wrap` 自动落行，`styles.css:11007` 那条 `.field-row { grid-template-columns: 1fr }` 媒体查询随之删除。

**影响面**：`.field-row` 另有 4 个消费方 —— `PluginSourceSettings`、`PluginSourceDialog`、`PluginLibraryDrawer`、`SchematicMarket`。这四处的字段没有 `--num` / `--md` 类，会退化成 `max-width: 100%` 的等宽并排，视觉上和现在接近但不再强制五五分。四处都要人工看过。

### 4. 长说明折叠：`FieldHelp`

新增 `web/src/components/FieldHelp.tsx`，一个 `<details class="field__help">` 薄封装：

```tsx
<FieldHelp summary="为什么？">…完整解释…</FieldHelp>
```

用 `<details>` 而不是气泡：无 JS、键盘可达、`prefers-reduced-motion` 天然无碍、不需要焦点管理。卡片里只留一行结论，展开才是全文。

改造对象（折进去的正文一字不动；「留在外面的一行」是从原文压缩的摘要，需逐条评审措辞）：

| 位置 | 留在外面的一行 | 折进 `FieldHelp` |
| --- | --- | --- |
| 服务端类型 | 「面板先从目录和 jar 名认，认不出来才用这里填的。」 | Forge 认不出来的后果那段 |
| 使用终端模式 | 「Tab 补全由服务端回答，进度条实时。」 | 伪终端原理与 stderr 代价 |
| 强制彩色输出 | 「仅在管道模式下有意义。」 | 两个 `-D` 参数与 `JAVA_TOOL_OPTIONS` 那段 |
| 输出编码 | 「按这个编码解读输出、发送命令。」 | UTF-8 / GBK 兜底那段 |
| 参数文件 | 「一行一个，从实例目录算起。」 | Forge 1.17 起没有可跑 jar 那段 |

`.checkbox` 的结构要跟着调：现在 `small` 是 `.checkbox` 的第三个 flex 子项，折叠后它变成「一行短说明 + 一个 `<details>`」，需要一个包裹 `<div>` 承载，避免 `align-items: flex-start` 把 `<details>` 拉到复选框那一列。

### 5. 底部：dirty 才出现的 sticky 保存条 + 独立 Danger Zone

**保存条。** 表单脏了才浮出一条贴底的条：「有未保存的改动　[放弃] [保存设置]」。

dirty 的判定：`toInput(instance)` 与 `form` 做结构比较，再加 `jvmText` / `serverText` / `argFileText` 三个文本态与其来源的比较。三个文本态已经存在于组件里，不需要新状态。

```css
.formbar {
  position: sticky;
  bottom: 0;
  /* 进场是「原地出现」而不是「横跨屏幕」，所以 --dur-3 而不是 --dur-4。 */
  animation: formbar-in var(--dur-3) var(--ease);
}
```

sticky 相对的是 `.instance__pane--scroll`（`styles.css:3080`，`overflow-y: auto`），不是窗口 —— 它本身就是 scrollport，所以 sticky 成立。`.stack` 的 `padding-bottom: 40px` 会留在保存条下面，这是对的：条是浮在内容上的，不是内容的最后一行。

**Danger Zone。** 「从面板移除」「删除实例及所有文件」从 `.actions` 里拿出来，成为页尾独立区块：

```css
.panel--danger {
  border-color: var(--danger-edge);
  background: var(--danger-soft);
}
```

`--danger-edge` 和 `--danger-soft` 在 light / dark 两个令牌块里都已存在（`styles.css:159/372`、`156/369`），**本次不需要新增任何令牌**。

理由：现在「保存设置」和「删除实例及所有文件」在同一行的两端，是本页最该修的一处。同时 `.actions__danger { margin-left: auto }` 这条规则在本页失去用途（其他页仍在用，规则本身保留）。

### 6. 段落顺序：6 张卡收成 4 张

| 现在 | 改后 |
| --- | --- |
| 基本信息 | ① **开服前检查** → 页头下一行状态条 |
| 从核心库安装 | ② 基本信息 |
| 开服前检查 | ③ 启动方式（内含折叠的「从核心库安装…」） |
| 启动方式 | ④ 控制台 |
| 控制台 | ⑤ 进程管理 |
| 进程管理 | ⑥ sticky 保存条 |
| 保存 + 危险按钮同排 | ⑦ Danger Zone |

**开服前检查 → 状态条。** 无问题时是 `PageHead` 下面的一行：绿点 +「核心和目录都对得上」+ 右侧「重新检查」链接按钮。有 issue 时展开成现在的 `.launchcheck` 列表（含 `needs-setup` 那段 `legacyCommand` 回显）。`LaunchCheckPanel` 的 issue 渲染逻辑原样保留，只换外壳。

**从核心库安装 → 折进「启动方式」。** 它写的就是「服务端 jar」这个字段，co-locate 是对的。做法：`InstanceCorePicker` 的 `<section className="panel">` 外壳换成 `<details className="corepicker">`，summary 一行「从核心库安装一个核心…」，默认收起；内部的选择器、复选框、`复制到实例` 按钮、错误/成功 alert 全部不动。

组件契约不变：`instance` / `cores` / `onApplied` / `onOpenLibrary` / `jarIgnored` 五个 prop 原样，`onCoreApplied`（`LaunchSettings.tsx:207`）的回调链不动 —— 复制成功后仍然刷新 jar 列表、回写 `form.jar` 和 `serverText`。

`管理核心库` 这个 link 从 `.chart-head` 挪到 details 内部第一行，否则收起时它不可达。

**这是本次风险最高的一条**：`InstanceCorePicker` 有自己的 409 覆盖确认流程和 `ask()` 对话框，外壳一换要确认收起状态下弹出的确认框仍然正常（`ask()` 是全局 portal，理论上不受影响，但要实测一次）。

### 7. 三个消费方的连带改动

`.panel--form` 共 8 处使用、跨 3 个文件。改成两栏 flex 后，每一处都要补 `.panel__head` / `.form-body` 两层包裹，否则会退化成一列平铺 —— 不难看，但不算做完。

| 文件 | 卡片数 | 说明 |
| --- | --- | --- |
| `LaunchSettings.tsx` | 4（重构后） | 本次主体 |
| `VelocityConfig.tsx` | 4（基本设置 / 玩家信息转发 / 高级设置 / Query） | 补包裹层 + 给字段归档宽度 |
| `NewInstanceWizard.tsx` | 2（服务器设置 / 代理端设置） | 同上。向导是窄容器，多半直接走换行分支，仍需实测 |

## 数据流

不变。这是一次纯排版重构：

- 保存仍然是 `form` → `api.updateInstance` → `onSaved`。
- 核心复制仍然是 `InstanceCorePicker` → `api.applyCore` → `onCoreApplied` → 回写 `form.jar` / `serverText`。
- 参数文件内存仍然是 `ArgFileMemory` → `api.saveJVMArgs`。
- `LaunchCheck` 仍由 `checkRev` 驱动重查。

唯一新增的派生状态是 `dirty`（纯计算，无 setState）。

## 错误处理

现有的 `error` / `status` alert 全部保留，位置从卡片底部移到对应卡片的右栏底部。`InstanceCorePicker` 收起时如果内部产生了 error，要**自动展开** details —— 否则错误信息藏在收起的抽屉里。用受控的 `open` prop 实现：`open={coreOpen || Boolean(error)}`。

## 测试与验证

前端没有单测，`tsc -b` 是唯一的自动检查，所以验证以人工为主。

```bash
npm --prefix web run build      # tsc -b + vite build + check:ui
```

人工清单：

- 明暗两种模式都看过（Danger Zone 用的 `--danger-edge` / `--danger-soft` 两块都已有值，仍需目视确认暗色下不刺眼）。
- 1440 / 1200 / 1024 / 768 / 390 五个宽度：无横向溢出、无错位。
- **704px 换行点两侧各测一次**（比如 690 / 720），确认左栏是干净地折上去而不是挤成两行。
- 三处易被波及的地方：折叠侧栏、打开抽屉、开着控制台的实例页。
- 长内容不撑破：六层深的目录路径、很长的实例名、几十行 JVM 参数。
- 连带页面：`VelocityConfig`（代理配置页）、`NewInstanceWizard`（新建向导全流程）、以及 `.field-row` 的四个消费方。
- 功能不回归：保存、复制核心（含同名 409 覆盖）、写入 `user_jvm_args.txt`、从启动脚本导入、删除实例。
- 键盘：Tab 能走到每个 `<details>` 的 summary；焦点环用 `--ring`，危险按钮用 `--ring-danger`。

## 风险

1. **`InstanceCorePicker` 折叠**（最高）。外壳换成 `<details>` 后，409 覆盖确认框和 error 自动展开两条路径必须实测。缓解：`open` 受控 + 逐条走一遍复制流程。
2. **sticky 保存条在窄屏遮住最后一个字段**。scrollport 已确认成立（`.instance__pane--scroll` 是 `overflow-y: auto`），风险只剩遮挡。缓解：390px 下实测，必要时给 `.stack` 的 `padding-bottom` 在有保存条时加量；不用 `position: fixed`（会脱离 pane，抽屉打开时错位）。
3. **`.field-row` 语义变化波及 4 个无关组件**。缓解：逐个截图对比，必要时给它们补 `.field--md`。
4. **`999` 这个 flex-grow 比看起来像魔法数**。缓解：CSS 里写注释解释它的作用（把左栏按在 flex-basis 上）和换行点的算法，符合仓库「注释解释为什么」的约定。
5. **向导变窄后两栏永远不并列**，左说明栏在窄容器里只是多一行标题。这是可接受的退化，但要确认它没有比现在更差。

## 文档

- `docs/design-system.md` 新增「表单」一节：两栏骨架、四档字段宽度、何时整行、说明文字何时折叠、危险操作隔离。
- 四档宽度类是否被滥用（例如 `.field--num` 套在 `<textarea>` 上）尝试接进 `npm --prefix web run check:ui`。
- `CHANGELOG.md`「未发布」小节记一条用户可见的行为变化：设置页排版重构 + 保存/删除分离。
