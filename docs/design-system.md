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

### `Select`

```tsx
<Select ariaLabel="Java 环境" value={java} options={opts} onChange={setJava} />
<Select allowCustom ariaLabel="服务端 jar" value={jar} placeholder="server.jar" … />
// options: { value, label, note?, disabled? }[]
```

面板里**唯一**的下拉。`note` 是第二行，放标签本身说不清的东西（文件大小、版本日期、令牌尾号）。

`allowCustom` 让值也能直接敲进去，列表退为建议：列表里没有的名字照样留得住。给「服务端 jar」这类
「通常从目录里挑，但也可能填一个还没下载的」的字段用。

**不要用原生 `<select>` 和 `<datalist>`**：它们的弹窗是平台画的，读不到任何一个令牌，也不跟着明暗
主题走 —— 一个精修到毫米的表单中间掉出一块 Windows 95 风格的列表，就是这么来的。守卫 `ruleDropdownsAreOurs`
直接卡。粗指针设备上 `Select` 自己会回落到真的 `<select>`（拇指要的是系统自带的选择器），那是它内部的事。

`<label class="field">` 包不住它 —— 非 `allowCustom` 时它渲染的是 `<button>`，而 `<label>` 标不了
按钮，所以 `ariaLabel` 实际上是必填的。

### `Badge`

```tsx
<Badge tone="warn">需重启</Badge>
// tone: BadgeTone = neutral | ok | warn | danger | alert | live | muted | update | changed
```

静态状态标签，默认无色。`BadgeTone` 是导出的——状态到色调的映射表（`SecurityPage` 的 `KIND_BADGE`、
`Sidebar` 的 `ALERT_BADGE`）标上它，拼错就编译不过。

**不要手写 `.badge` 和它的 `--tone` 修饰符**：守卫 `ruleBadgesAreComponents` 直接卡。这套 class 名曾经
散在 23 个文件里当裸字符串写，于是 `.badge--warn` 被定义了两遍、后一块丢掉 `border-color`，警告徽章的
描边一直没生效而没人发现——因为没有任何一处是这套色调词汇的家。现在有了。

**什么时候不用它**：

- `.chip` 是**可点的筛选控件**（有 `cursor: pointer`、`--active` 态、按下去会缩），不是徽章。两者看起来
  相邻，但把标签变成控件是错的。
- **画成徽章的控件**留着裸 class。插件列表那个「→ 新版本号」的升级键是 `<button className="badge
  badge--update">`：`Badge` 渲染的是 `<span>`，换过去等于把按钮变没了。这跟 `<a className="btn">` 对
  `Button` 的例外是同一件事，守卫只卡 `<span>`。

### `StatusDot`

```tsx
<StatusDot state={instance.state} />
```

实例状态圆点，`state` 就是 `InstanceState`。自带 `aria-hidden`——每一处它出现的地方，状态都同时写成了文字。

### `Section`

```tsx
<Section title="核心库" count={3} note="一句话说明这一段是干什么的。" meta="物理 15.7 GB"
         tools={<Button size="small">添加</Button>} tone="warn|danger" form>
```

**面板里每一个有标题的内容块都是它**，没有第二种写法。标题和说明在左，一句短事实和这一段
自己的控件在右，正文在下面。

`note` 是句子，`meta` 是**不带动词的短事实**（`linux/x64`、`物理 15.7 GB`、`还没建数据库`）。
分错的后果很具体：一句话放进 `meta` 会把控件挤出那一行。

`form` 让正文进 `.panel__body`（封顶 880px、字段间距 14px），设置类表单一律加。

**这一段以前有五种写法**：`.chart-head` 配一行 meta、`.panel__head` 配一个按钮、
`.update__head`、表单的 `.panel__aside`、以及裸 `<h3>`；长出自己写法的页面还有第六第七种
（`.dlqueue__title`、`.foreign__title`）。说明文字落在三个不同的位置，计数有两种，控件在
作者记得的那一边。一页页读下来，面板像七个人拼的。守卫 `ruleSectionsAreComponents` 卡住
`panel--form` / `panel__head` / `panel__body` / `panel__tools`，只许 `Section.tsx` 写。

### `Toolbar` / `ToolbarSearch`

```tsx
<Toolbar>
  <ToolbarSearch placeholder="按名称搜索" value={q} onChange={…} aria-label="…" />
  <div className="toolbar__chips">…chip…</div>
  <span className="toolbar__count">1 / 1</span>
  <div className="toolbar__tools"><Button size="small">…</Button></div>
</Toolbar>
```

**一个列表最多一条，就贴在它上面。** 搜索在左，筛选 chip 跟着，计数或一句判据在中间，
这个列表自己的按钮在最右。

替掉的七套：`.filters`、`.chart-filters`、`.chist__filters`、`.schemlib__bar`、
`.browse__search`、`.file-toolbar`、`.chips`。

文件页的查找框用 `.toolbar__search--end` 挪到最右端 —— 伸手去按「上传」不该落进过滤框里。

chip 的选中态**只有 `chip--on`**（`chip--active` 已经没了）；chip 里的计数用 `<b>`。

### `EmptyState`

```tsx
<EmptyState title="核心库还是空的。" action={<Button variant="primary">…</Button>}>
  下面挑一个下载，或者把自己的 jar 直接放进核心库目录。
</EmptyState>
<EmptyState inline title="没有匹配的插件。" />   {/* 表格/列表里代替行 */}
```

只有两种形态：**块**（虚线框，空间是「等着被填」而不是「渲染挂了」）和 **`inline`**
（列表表头下面的一行，表头不动，筛不到东西时列表不变形）。

第一句是「这里没有什么」，后面是建议。以前有六种写法（`.welcome__empty`、表格里的
`<p>`、`p.muted`、带字形的 `.file-empty`，加上两个页面各自的），拿到哪一种取决于页面而不是
取决于情况。守卫 `ruleEmptyStatesAreComponents` 卡住 `empty__*`。

### `.rowlist > .row`

卡片里的行列表：一个账号、一台配对设备、一个令牌、一个索引源、一条下载。

```html
<div class="rowlist">
  <div class="row">
    <div class="row__main">
      <span class="row__title">admin</span><Badge/><span class="row__sub">别名</span>
      <div class="row__actions"><button class="link">编辑</button></div>
    </div>
    <div class="row__meta">可以管理全部实例 · 没有配对设备</div>
  </div>
</div>
```

修饰符：`row--on`（选中，走描边不走填色）、`row--off`（停用）、`row--pick`（整行是
`<label>` 的单选行，横向）。

替掉的六套：`.device-row`、`.acct-row`（和前者逐字节相同）、`.tokenrow`、`.setting-row`、
`.schemsource`、`.dlrow`、`.foreign__row`。

### `Card`

```tsx
<Card as="article" pad="tight" tone="sunken" className="schemcard">
// pad:  'default'(16px) | 'tight'(12px 14px) | 'none'
// tone: 'raised'(--surface-1) | 'sunken'(--surface-2)
// as:   'div' | 'article' | 'section'
```

调用点保留自己的块类，「卡片里面放什么」的规则留在原处，只有容器是共享的。整张卡可点的场合，可点的部分是子元素 `.card__open`，卡片靠 `:has()` 跟着抬升——**一张自己带按钮的卡片不能自己是按钮**。

**`Card` 和 `Section` 的分工**：`Section` 是页面上一个有标题的内容块（段），`Card` 是栅格
里重复出现的一张卡（一个实例、一个建筑、一条 JVM 参数）。一段里装一排卡，不反过来。

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

## 3.5 · 页头

```tsx
<Page wide title="插件列表" count={n} lead="这一页是干什么的。"
      facts={<><span>12 个</span><span>3.5 GB</span></>}
      actions={<><Button>次要</Button><Button variant="primary">主要</Button><Menu…>⋯</Menu></>}>
```

- `facts` 是常驻事实（数量、体积、路径），渲染在 lead **下面**。它们以前在页头右半边，
  一条深路径会把整行挤到第二行、悬空在标题旁边。
- `actions` **最多两个按钮加一个溢出 `Menu`**，其中只有一个实心。多出来的进菜单，危险的在
  菜单里标红。插件页曾经一行四个按钮加一个链接，配置历史一行四个其中一个是危险操作 ——
  一眼要读五遍才找得到要按的那个。
- `count` 是标题后面那个数：`title="插件" count={n}`。

## 4 · 表单

**面板级表单一律是 `<Section form>`：卡片头 + 字段，跟面板里其他卡片一个形状。**

```tsx
<Section form title="段标题" note="这一段是干什么的，一句话。">
  {/* 字段 */}
</Section>
```

渲染出来是 `.panel.panel--form > .panel__head + .panel__body`。这几个类只许 `Section.tsx`
写（`ruleSectionsAreComponents`）：`.panel--form` 扛的是「字段停在阅读宽度」这个保证，
手写时**漏掉包裹层不会报错**，只会让字段失去自己的行长、一路铺到卡片边——那种「看起来
差不多对」正是这条规则要挡的。

正文封顶 `--content-max`（880px），宽屏上尾部的留白是有意的：宽度给导览，不给输入框。

**这一段曾经是左右两栏**，段标题站在字段左边。纸面上的理由是「宽窗口带来的宽度归导览，不归输入框」，
实际是一句两行的说明站在一个三百像素宽、四百像素高的格子里，那片空白读起来是卡片上的一个洞，不是留白。
而且那套算式并不成立：`flex: 999` 对 `flex: 1` 只在正文还能涨的时候管用，正文一撞上 `max-width` 就被
flexbox 冻结，剩余空间全部分给唯一还能伸的项——也就是左栏。实测卡片内宽 1186px 时左栏 282px，1440px 时
536px。给它封顶只是止住了增长，没有回答「这一栏本来就不该在那儿」。

所以头回到卡片头该在的位置。留白从「标题和字段之间」挪到了右边，那才是注释里一直声称的那个尾部留白。

这套排版替换掉的是一个 `repeat(auto-fit, minmax(340px, 1fr))` 栅格。它的列数由视口宽度决定——在 1440px 的实例 pane 里恰好算出三列，于是同一视觉行上会出现三种宽度的输入框，而没进整行白名单的 `.checkbox` 把六行说明塞进 300px 的格子。**列数浮动，字段宽度就无从谈起。**

### 字段宽度

**宽度即语义**——看一眼就知道该填多长。四档，没有第五档：

| 类 | 宽度 | 用于 |
| --- | --- | --- |
| `.field--num` | 120px | 内存 MB、超时秒数、端口 |
| `.field--sm` | 200px | 版本号一类的短标识 |
| `.field--md` | 380px | 名称、下拉、文件名、命令 |
| 不加类 | 整行（封顶 880px） | 路径、参数列表、文本域 |

数值写在自定义属性 `--field-w` 上，`max-width` 和 `.field-row` 的 `flex-basis` 共用它——**基准必须来自这张表，不能来自 `auto`**。`flex-basis: auto` 量的是内容，而内容的宽度和字段该有多宽无关，两种翻车都真实发生过：控件是 `width: 100%`，百分比不参与固有尺寸，字段塌成自己标签的宽度；字段里带一句 `<small>`，量的是那句话，行在还有富余时就提前换行。

两个字段并排用 `.field-row`（wrap flex，各自带自己的宽度，窄屏自动落行）。没有宽度类的字段兜底 200px。

### 说明文字

一句话以内用 `<small>` 常驻。**超过一句的折进 `<FieldHelp>`**：

```tsx
<small>一行结论。</small>
<FieldHelp summary="为什么？">…完整解释…</FieldHelp>
```

`<details>` 而不是气泡：无 JS、无焦点管理、键盘可达，也没有动效要被 `prefers-reduced-motion` 关掉。文字没有被藏起来，只是退后一格。

复选框折叠说明时，说明部分要包一层 `.checkbox__text`——`.checkbox` 是 `align-items: flex-start` 的 flex 行，直接塞 `<details>` 会被拉进勾选框那一列。

### 保存与危险操作

- 保存条 `.formbar` **只在表单脏了的时候出现**，sticky 贴在滚动容器底部（不是 `position: fixed`——那会脱离 pane，抽屉盖上来就错位）。
- 破坏性操作放页尾独立的 `.panel--danger`，**不与保存按钮同排**。
- 一屏一个实心按钮，那一个是「保存」。

---

## 5 · 只有异常才上色

**一屏内彩色像素越少，状态越显眼。**

正常状态用中性色，只有异常才上色。如果一个列表里每行都有彩色徽章，颜色就失去了报警能力。

这条不是审美偏好，是可以照着改代码的：`.pstate` 基类是中性的 `--text-dim`，「一致」这个状态**不该有 `--ok` 修饰符**——中性文字配一个绿点就够了。曾经有过一个 `pstate--ok`，它从未被定义过，而这恰好是对的。

## 6 · 一屏最多一个实心按钮

实心按钮是一句断言：**这一屏，你要做的是这件事。** 一屏两个就是两句断言，读的人两个都得看一遍——而那正是这套安静的配色花钱买来要避免的成本。

这条规则咬得最狠的是列表：**一个行内动作只要是实心的，它就每行实心一次**，而一屏里每行都实心，等于一个实心按钮都没有。

两个候选之间怎么选，两条：**重点给还没有重点的地方，且永远不给重复出现的东西。**

| 留实心 | 降成描边 |
| --- | --- |
| 表单的提交（`创建`、`保存`） | 主路径旁边的逃生口（`安装填写的版本`） |
| 向导页脚的「下一步／创建」 | 步骤内部的次要动作（`下载 XXX`） |
| 素色段头里那个唯一的主入口（`从插件库安装`） | 每行都有的动作（`对齐`、`升到 X`、`连接`） |
| 空状态的 CTA | **已经上了色的容器里的按钮**（告警横幅里的 `立即重启`） |

最后一条容易反着想：告警横幅本身就是重点——它有警示色的底、一圈描边、还占着整个面板的顶部。
再给它一个实心按钮，是同一句话说两遍，而且那一屏的第一个实心按钮已经在段头上了。

两个地方用了条件式：概览页的 `+ 新建实例` 在列表为空时降级（空状态的 CTA 干的是同一件事），
数据库页的 `新建数据库` 在表单展开后降级（此刻要你做的是 `创建`）。两种状态下都恰好一个。

破坏性操作在页面上**只用红色描边**；只有在二次确认弹窗里，最后那一下才允许实心红——那时用户已经知道自己在做什么。

守卫 `rulePrimaryButtons` 阻断构建。默认每个文件一个；对话框是独立的一屏、三元分支不可能同屏，这类情况在 `check-ui.mjs` 的 `PRIMARY_ALLOWED` 里按文件登记配额和理由，每一条都是「有人看过」的承诺。超配额直接失败；低于配额只打印一行提示，因为「删掉一个实心按钮就构建失败」等于在替保留它说话。

---

## 7 · 贯穿全局的六条

1. **状态常驻**——运行状态、TPS、内存、在线人数固定在顶栏，任何页面都能一眼看到、就地启停，不用退回首页。
2. **两层作用域**——侧栏先选实例、再选功能。「实例」与「面板/主机」两组分开，否则用户永远搞不清改的是哪一层。
3. **改动可见、可撤**——改了什么、原值是多少、要不要重启，全部就地标出，底部常驻保存条统一提交。
4. **深色只给日志**——终端是只读的信息流，深底更耐看；编辑器是工作区，保持随主题。两者的对比本身就是一种导航。
5. **术语说人话**——主标签写中文、副行写原始键名（`view-distance`），老玩家能搜到，新手看得懂。
6. **危险操作有摩擦**——强制终止、删除文件、卸载插件都要二次确认并说清后果；它们绝不与常规操作并排同样式摆放。

---

## 8 · 本轮没落地的

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

## 9 · 已经能被 CI 执行的规则

`web/scripts/check-ui.mjs`，接在 `npm run build` 最前面（几十毫秒，先失败先省一次类型编译），也可单独跑 `npm --prefix web run check:ui`。

| 规则 | 管什么 |
| --- | --- |
| `ruleNoUndefinedClasses` | tsx 里用到的每个 BEM 类名都在 `styles.css` 里存在。基类的修饰符若已定义则算它已定义（`.chist__line` + `--add`/`--delete` 是合法的 BEM，未改动的行没有背景是对的）。 |
| `ruleIconButtonsAreLabelled` | `<Button icon>` 必须带 `aria-label`。图标按钮没有文字，漏了 label 屏幕阅读器只念得出 "button"。 |
| `ruleSectionsAreComponents` | `panel--form` / `panel__head` / `panel__heading` / `panel__body` / `panel__tools` 只许 `Section.tsx` 写。`.panel--form` 扛的是「表单字段停在阅读宽度」这个保证，而保证依赖那两层包裹；手写时漏一层不会报错，只会让字段一路铺到卡片边——那种「看起来差不多对」正是这条要挡的。 |
| `ruleEmptyStatesAreComponents` | `empty__*` 只许 `EmptyState.tsx` 写。以前有六种「这里没有东西」的写法，拿到哪种取决于页面而不是取决于情况。 |
| `rulePrimaryButtons` | 一个文件最多一个 `variant="primary"`，`PRIMARY_ALLOWED` 里登记过的按登记的配额。见第 6 节。 |
| `ruleBadgesAreComponents` | `<span>` 不许手写 `.badge` / `.badge--*`，一律走 `<Badge>`。模板里的 `${…}` 先剥掉再分词，否则 `badge${TONE[x]}` 这种写法会整个溜过去。非 `<span>` 的放行——画成徽章的按钮不可能是 `Badge`。 |
| `ruleDropdownsAreOurs` | 组件里不许出现原生 `<select>` / `<datalist>`，一律走 `Select`。`Select.tsx` 自身豁免 —— 它拥有两个分支，包括粗指针设备上回落的那个真 `<select>`。扫描前先剥注释：这份代码库的注释大量在讨论这两个标签（`InstancePlugins` 和 `JVMArgsEditor` 各自长篇解释了为什么**不**用），不剥就全是误报。 |
| `ruleNoSilentOverrides` | 同一个选择器不许重复声明同一个**属性**。写两遍本身不算错——这份样式表是按叙述组织的；但同一个属性写两遍，就一定有一块在悄悄失效。有意的覆盖写进 `OVERRIDE_ALLOWED` 并附理由。 |

只校验 BEM 形状（含 `__` 或 `--`）的类名——单词形的 token 会被模板字符串里的对象键和状态名污染，全是误报。扫描 JSX 标签时先跳注释再跳字符串：属性之间的注释里有 "snapshot's copy"，把那个撇号当成字符串起始会吞掉整个文件。

**这些规则不是假想的洁癖。** 加上它们抓出的：9 个从未生效的类名（`btn--small` 被使用 15 次、定义 0 次，插件库的小号按钮一直全尺寸渲染）；`.badge--warn` 被后一块覆盖掉 `border-color`，警告徽章的描边一直没生效；`.preview` 是两个组件重名，插件源对话框的资源列表被塞进了文件预览的棋盘格和居中。

### 还没被守卫覆盖的

- **表格家族不得重新声明 `.dtable__*` 已有的属性**。目前靠人看。合并时留下的两处有意例外：`.rows__head` 和 `.plugin-table__head` 用小型大写，其余表头不用——这是观感差异不是结构差异，等观感那一轮统一。
- **`PRIMARY_ALLOWED` 的配额是按文件不是按屏**。脚本看不见「一屏」，所以对话框和互斥分支只能靠登记豁免。登记时看错了，规则就跟着错——这是这条规则唯一的软肋。
