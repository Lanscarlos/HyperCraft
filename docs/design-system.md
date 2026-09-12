# HyperCraft 设计规范

面板（`web/`）的跨页面约定：令牌怎么取、控件用哪个、什么时候允许上色。

**这份文档不重复 `frontend-design` skill。** 页面框（`Page.tsx` 的三种形态）、栅格、断点、响应式、动效时长的选法在 `.claude/skills/frontend-design/SKILL.md` 里，改布局前看那份。这里管的是**控件和视觉约定**。

能被机器检查的部分由 `npm --prefix web run check:ui` 执行，已接进 `npm run build`——只写在文档里的规范会漂，见文末「已经能被 CI 执行的规则」。

---

## 1 · 令牌

全部令牌在 `web/src/styles.css` 开头（约 74–770 行）。**组件区一个裸 hex 都没有，保持这样。**

底座是 Material 3 的角色色（`--primary` / `--surface-*` / `--on-*` 等），上面盖一层这个仓库自己的**工作名**——日常写样式只用工作名，M3 角色只在令牌区内部互相引用。

### 表面阶梯

| 令牌 | 用途 |
| --- | --- |
| `--bg` | 页面底色 |
| `--surface-1` | 卡片、侧栏 |
| `--surface-2` | 卡片里的一层、表头、悬停底 |
| `--surface-3` | 压在卡片上的控件 |
| `--surface-4` | 再往上一级 |

需要读作「压在某物之上」就**往上走一级，不要发明新值**。浅色模式里页面是暖色调、卡片是白的、卡片里的东西更暖（显得凹陷）；深色模式整个反过来。所以同一个令牌在两种模式下取自阶梯的两端。

### 文字与描边

`--text` / `--text-dim` / `--text-faint`；`--border` / `--border-strong` / `--border-accent`。

### 状态色

`--ok`（运行中）、`--caution`（需注意）、`--danger`（危险）各有独立色相，且各自对所在表面校验过 3:1。**不要换成主色**——服务器在跑还是没跑，用主色画的「运行中」圆点会被读成警告。

### 终端两块画布

`--term-*`（服务器控制台）与 `--shell-*`（主机 shell）在**明暗两种模式下都保持深色，且两者一眼可分**。这是防止把 `rm -rf` 敲进错误终端的唯一屏障，不要统一它们的配色。

### 主题与配色

4 套配色（`樱花` / `松绿` / `杏黄` / `碧蓝`）× 明暗两种模式 = 8 个令牌块。

**新增令牌必须每个块都加**，否则某个组合下会是空值。

### 圆角、阴影、动效

```
--radius-sm  6px      --shadow-sm   --dur-1  90ms   指针反馈
--radius     9px      --shadow      --dur   140ms   原地变状态
--radius-lg 14px      --shadow-lg   --dur-3 220ms   原地出现/消失
--radius-pill 999px   --ring        --dur-4 300ms   横跨或覆盖屏幕
                      --ring-danger --dur-data 400ms 数据驱动的走位
```

缓动：进场/移动 `--ease`，退场 `--ease-in`，数据 `--ease-out`。

**动效时长按动作的性质选，不按距离选。** CSS 里的动效已被 `prefers-reduced-motion` 统一关掉；**JS 里写的动效（WAAPI、setTimeout）不受它管**，必须自己调 `motion.ts` 的 `reducedMotion()`。

### 版式

`--font-ui` 是 Inter + PingFang SC，`--font-mono` 是 ui-monospace / JetBrains Mono。**数字、路径、配置键一律等宽**，便于纵向对齐比对。

---

## 2 · 尺度

| | 取值 |
| --- | --- |
| 控件高度 | 两档：默认（`.btn` 约 34px）与小号（`.btn--small` 约 29px）。**只有这两档。** |
| 表格行高 | 48px 默认，40px 紧凑（`dtable--compact`），58px 宽松（`dtable--roomy`）；表头 34px |
| 侧栏 | `--sidebar-w` 240px，折叠 64px |
| 内容宽度 | `--content-max` 880px（散文）、`--content-max-wide` 1440px（瓦片） |

间距没有令牌，是直接写的偶数 px。**沿用邻近区块的节奏**（页面级 `gap: 16px`、`.main` `gap: 12px`、卡片内 6/8/10/14px），不要凭空引入 13px、17px 这种值。

---

## 3 · 控件清单

写新界面时先从这里挑，挑不到再考虑新增——新增之前想清楚它和已有的哪个是同一件事。

### `Button`

```tsx
<Button variant="primary" size="small" icon aria-label="刷新" />
// variant: 'default' | 'primary' | 'danger'
// size:    'default' | 'small' | 'row'
```

`size="row"` 是行内按钮，和 `variant` 正交——「行内的危险按钮」是 `variant="danger" size="row"`。`icon` 必须同时给 `aria-label`（守卫会检查）。`type` 默认 `'button'`；表单提交按钮显式写 `type="submit"`。

**什么时候不用它**：`<a className="btn">`（链接扮成按钮）和 `<Menu className="btn">`（Menu 自己渲染触发器）保持原样，它们不是 `<button>`。

### `Badge`

```tsx
<Badge tone="warn">需重启</Badge>
// tone: neutral | ok | warn | danger | alert | live | muted | update | changed
```

静态状态标签，默认无色。

**什么时候不用它**：`.chip` 是**可点的筛选控件**（有 `cursor: pointer`、`--active` 态、按下去会缩），不是徽章。两者看起来相邻，但把标签变成控件是错的。

### `StatusDot`

```tsx
<StatusDot state={instance.state} />
```

实例状态圆点，`state` 就是 `InstanceState`。自带 `aria-hidden`——每一处它出现的地方，状态都同时写成了文字。

### `Card`

```tsx
<Card as="article" pad="tight" tone="sunken" className="schemcard">
// pad:  'default'(16px) | 'tight'(12px 14px) | 'none'
// tone: 'raised'(--surface-1) | 'sunken'(--surface-2)
// as:   'div' | 'article' | 'section'
```

调用点保留自己的块类，「卡片里面放什么」的规则留在原处，只有容器是共享的。整张卡可点的场合，可点的部分是子元素 `.card__open`，卡片靠 `:has()` 跟着抬升——**一张自己带按钮的卡片不能自己是按钮**。

### `DataTable` / `DataTableHead` / `DataTableRow` / `DataTableEmpty`

```tsx
<DataTable className="ptable" role="table" aria-label="插件库">
  <DataTableHead className="ptable__head" role="row">…</DataTableHead>
  <DataTableRow className="ptable__row" role="row">…</DataTableRow>
</DataTable>
// density: 'default'(48) | 'compact'(40) | 'roomy'(58)
```

列定义是每张表唯一还自己声明的东西，写在它的块类里。

**什么时候不用它**：数据本身就是表格的时候用真正的 `<table>`（见 `.data-table`）。div 栅格加 `role="table"` 只是近似，屏幕阅读器的行列导航是真表格白给的。`.asset` 也不是表格——它的 `__head` 在自己内部，是「自成一卡的独立行」。

---

## 4 · 只有异常才上色

**一屏内彩色像素越少，状态越显眼。**

正常状态用中性色，只有异常才上色。如果一个列表里每行都有彩色徽章，颜色就失去了报警能力。

这条不是审美偏好，是可以照着改代码的：`.pstate` 基类是中性的 `--text-dim`，「一致」这个状态**不该有 `--ok` 修饰符**——中性文字配一个绿点就够了。曾经有过一个 `pstate--ok`，它从未被定义过，而这恰好是对的。

## 5 · 一屏最多一个实心按钮

破坏性操作在页面上**只用红色描边**；只有在二次确认弹窗里，最后那一下才允许实心红——那时用户已经知道自己在做什么。

现状：15 个组件有多于一个实心按钮，`PluginLibraryPage.tsx` 有 6 个。守卫脚本会把超标的文件打印成提示（不阻断构建），目标是让这个数字只减不增。

---

## 6 · 贯穿全局的六条

1. **状态常驻**——运行状态、TPS、内存、在线人数固定在顶栏，任何页面都能一眼看到、就地启停，不用退回首页。
2. **两层作用域**——侧栏先选实例、再选功能。「实例」与「面板/主机」两组分开，否则用户永远搞不清改的是哪一层。
3. **改动可见、可撤**——改了什么、原值是多少、要不要重启，全部就地标出，底部常驻保存条统一提交。
4. **深色只给日志**——终端是只读的信息流，深底更耐看；编辑器是工作区，保持随主题。两者的对比本身就是一种导航。
5. **术语说人话**——主标签写中文、副行写原始键名（`view-distance`），老玩家能搜到，新手看得懂。
6. **危险操作有摩擦**——强制终止、删除文件、卸载插件都要二次确认并说清后果；它们绝不与常规操作并排同样式摆放。

---

## 7 · 本轮没落地的

设计文稿（画布 artboard `System.dc.html`）里有几条**有意没有采纳或延后**。记在这里，免得以后分不清是有意保留还是忘了改。

| 文稿要求 | 现状 | 为什么 |
| --- | --- | --- |
| 圆角 4 徽章 / 6 控件 / 8 卡片 | 6 / 9 / 14 | 延后。现在改要动全部页面；等控件层落地后，改这三个令牌即可，是一处改动而不是一百处。 |
| 按钮纯色面 + 1px 描边，「阴影几乎不用」 | `.btn` 是渐变 + `--shadow-sm` | 延后，同上。有了 `Button` 组件之后这是改一个文件的事。 |
| 字号收到 6 档 | 13 档 | 延后。随控件迁移自然收敛，不单独做一轮全局改字号。 |
| 4px 栅格 | 25 个间距值里 15 个不在栅格上 | 延后。同上，随控件迁移收敛。 |
| IBM Plex Sans + Noto Sans SC | Inter + PingFang SC | 不采纳。换正文字体会重排每一个页面的换行位置，收益不抵风险。 |
| 纯浅色单配色，深色只留给终端 | 4 配色 × 明暗 = 8 套 | **不采纳。** `CLAUDE.md` 硬性要求新令牌明暗对等，砍掉暗色是功能倒退。文稿这条在本仓库的对应物是第 1 节的「终端两块画布在明暗下都保持深色且互相可分」。 |

**待办**：`.browse__rail-card` 虽然也叫 card，但语义是插件市场左栏的分栏面板，不是内容卡片，没有并进 `Card`。将来如果再出现第二个同类，再考虑给它一个名字。

---

## 8 · 已经能被 CI 执行的规则

`web/scripts/check-ui.mjs`，接在 `npm run build` 最前面（几十毫秒，先失败先省一次类型编译），也可单独跑 `npm --prefix web run check:ui`。

| 规则 | 管什么 |
| --- | --- |
| `ruleNoUndefinedClasses` | tsx 里用到的每个 BEM 类名都在 `styles.css` 里存在。基类的修饰符若已定义则算它已定义（`.chist__line` + `--add`/`--delete` 是合法的 BEM，未改动的行没有背景是对的）。 |
| `ruleIconButtonsAreLabelled` | `<Button icon>` 必须带 `aria-label`。图标按钮没有文字，漏了 label 屏幕阅读器只念得出 "button"。 |
| `ruleNoSilentOverrides` | 同一个选择器不许重复声明同一个**属性**。写两遍本身不算错——这份样式表是按叙述组织的；但同一个属性写两遍，就一定有一块在悄悄失效。有意的覆盖写进 `OVERRIDE_ALLOWED` 并附理由。 |

只校验 BEM 形状（含 `__` 或 `--`）的类名——单词形的 token 会被模板字符串里的对象键和状态名污染，全是误报。扫描 JSX 标签时先跳注释再跳字符串：属性之间的注释里有 "snapshot's copy"，把那个撇号当成字符串起始会吞掉整个文件。

**这些规则不是假想的洁癖。** 加上它们抓出的：9 个从未生效的类名（`btn--small` 被使用 15 次、定义 0 次，插件库的小号按钮一直全尺寸渲染）；`.badge--warn` 被后一块覆盖掉 `border-color`，警告徽章的描边一直没生效；`.preview` 是两个组件重名，插件源对话框的资源列表被塞进了文件预览的棋盘格和居中。

### 还没被守卫覆盖的

- **表格家族不得重新声明 `.dtable__*` 已有的属性**。目前靠人看。合并时留下的两处有意例外：`.rows__head` 和 `.plugin-table__head` 用小型大写，其余表头不用——这是观感差异不是结构差异，等观感那一轮统一。
- **一屏一个实心按钮**：脚本只打印提示，不阻断构建（见第 5 节）。
