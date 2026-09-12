# 文件页编辑模式 设计文档

日期：2026-09-12
状态：已确认，待实施

## 背景

实例的「文件」页现在是三栏：目录树（220px）/ 文件列表（`minmax(340px, 1fr)`）/ 编辑器（`minmax(0, 1fr)`），外面套着 240px 的左侧导航和 `--content-max-wide`（1440px）的宽度上限。

这套排版在「管文件」时是对的：上传、新建、批量勾选、按大小和时间排序、目录内搜索、越界目录的只读提示，都需要那一栏列表。问题出在「读文件、改文件」的时候：

- **宽度被摊薄三次**。1440 里先扣 240 侧栏，再扣 220 树 + 340 列表，编辑器实际只剩 600–680px。配置文件里一行中文注释 40 多个字就触发横向滚动。
- **空间按「可能有多少」分配，不按「现在在干什么」分配**。一个只有一个文件的目录，列表照样占 1fr，而此刻焦点在右边那个打开的文件上。
- **树和列表信息重复**，两栏都在回答「我在哪」。
- **垂直空间被 PageHead 和工具栏吃掉**，一屏只剩二十几行正文。
- **没有语法高亮**。YAML 的缩进错、JSON 少一个逗号全靠肉眼；整段中文注释和实际的值是同一个颜色。
- **1024–1200px 是最难受的区间**：三栏都在，编辑器不到 500px。1024 以下靠 `narrowPane` 单栏切换反而更舒服。

结论不是「三栏不好」，而是「管文件」和「改文件」是两种模式，现在被压在同一屏里，谁都不舒服。

## 目标

在文件页加一个**编辑模式**：进入后内容区变成「目录树（含文件）+ 编辑器」两栏铺满，并给编辑器加语法高亮。

**非目标**（明确不做）：

- 不做全局 IDE 模式，不跨实例打开文件，不动路由。
- 不换编辑器内核（不引 CodeMirror / Monaco）。现有 textarea + 镜像滚动行号栏的那套代码全部保留。
- 不做代码折叠、括号匹配、查找替换、语法校验。
- 不做树的拖拽移动、拖拽调宽。
- 编辑模式不持久化：每次进文件页都是普通模式。
- 后端零改动。

## 已确认的三个决定

1. **边界：页内全屏**。只在实例的「文件」页生效，不做全局 IDE 模式。
2. **高亮：Prism 叠层**。在 textarea 下面垫一层只读高亮层，复用现有行号栏的滚动镜像办法；现有光标、选区、脏点、`Ctrl+S`、标签页、`beforeunload` 全部不动。
3. **列表去向：树吸收文件，列表隐藏**。排序、批量勾选、按大小和时间看这些「管文件」的活留在普通模式。

## 架构

### 1. 第三种页面形态：`full`

`CLAUDE.md` 和 `frontend-design` skill 现在写的是「只有散文（`--content-max` 880px）和瓦片（`--content-max-wide` 1440px）两种形态，不要引第三种宽度」。编辑模式需要取消宽度上限铺满屏幕，所以**给这条规则加一个有名有姓的第三种**，而不是偷偷开例外。

新形态叫**全屏工作区**（`full`）：

- 语义严格限定为「一屏一件事的工具页」——一块画布占满可用空间，而不是一段要读的内容。目前只有编辑模式用；以后可能的全屏终端是同一类。
- 内容页永远不准用。散文用 `--content-max`，瓦片用 `--content-max-wide`，这两条不变。

落地：

- `Page.tsx` 的 props 加 `full?: boolean`，与 `wide` 互斥（同时给时 `full` 优先），渲染 `page page--full`。
- `styles.css` 加 `.page--full`：不设 `max-width`，`flex: 1`，`min-height: 0`，`overflow: hidden`，`padding-bottom: 0`。
- 实例页的 `FileManager` 不在 `Page` 里，而在 `InstanceView` 的 `Pane` 里，宽度上限来自 `.instance__pane .stack { max-width: var(--content-max-wide) }`。所以同一种形态在这里的写法是 `.stack--full`：FileManager 在编辑模式下把根节点的 class 从 `stack` 换成 `stack stack--full`，样式表里给 `.instance__pane .stack--full` 同样的解除。
- `.instance__pane--scroll` 自己带 `overflow-y: auto`。编辑模式要的是「外层不滚、编辑器内部滚」，所以加一条 `.instance__pane--scroll:has(.stack--full) { overflow: hidden }`。用 `:has()` 是为了不给 `InstanceView` 加一个只为布局存在的状态；面板支持的浏览器都实现了它。
- 同步更新 `CLAUDE.md` 的「一个页面框」条目和 `.claude/skills/frontend-design/SKILL.md`，把 `full` 写成第三种形态并注明它的使用边界。

### 2. 外壳：复用现有的 rail

`App.tsx` 已经有折叠状态 `railed`（`.app[data-rail='on']`，侧栏 240 → 64px）。编辑模式**不新造外壳概念**，只是强制它：

- `App` 加一个 `workspace` 布尔 state。`data-rail` 的判断从 `!compact && railed` 变成 `!compact && (railed || workspace)`，传给 `Sidebar` 的 `railed` 同理。
- **用户自己的 `railed` 不被改写**，所以退出编辑模式时自动回到他原本的折叠状态，不需要记「进来之前是什么样」。
- 编辑模式下侧栏的折叠按钮和 `[` 快捷键要锁住，否则点了没反应会让人以为坏了：`Sidebar` 加 `railLocked` prop，按钮 `disabled` 并把 title 换成「编辑模式下侧栏保持图标条」；`App` 的 `[` 快捷键在 `workspace` 为真时直接返回。
- 状态怎么从 `FileManager` 传到 `App`：一路 props（`App` → `InstanceView` → `FileManager` 的 `onWorkspaceChange?: (full: boolean) => void`）。两层，显式，不引新的全局 store。

**顶栏保留**。面包屑、状态灯、启动按钮都不藏——「改完配置立刻重启」正是面板比通用编辑器强的地方。

**`PageHead` 在编辑模式下不渲染**（「文件」大标题和那句说明），换回一屏八到十行正文。

**离开即退出**。实例的 section 切换只是把 `Pane` 隐藏，不卸载 `FileManager`，所以「切到控制台」不会触发卸载。`FileManager` 要接收 `active` prop（`ResourcePanel` 已经是这个写法），`active` 变 false 时退出编辑模式并 `onWorkspaceChange(false)`；组件卸载时的 effect cleanup 也要做同一件事。**漏掉任何一处，侧栏都会永久卡在图标条**，这是本次改动最容易出的 bug。

### 3. 左侧树吸收文件

`FileTree` 加一个 `showFiles` 模式：

- `Node` 从 `{ name, path }` 扩成带 `isDir`。`read()` 不再在写入缓存时 `filter(isDir)`，改成存下全部 entries，**渲染时**按 `showFiles` 过滤。这样两种模式共用同一份缓存，来回切模式不重新请求。
- 排序自己做：目录在前，同类按名称。不依赖接口返回顺序。
- 文件行显示类型图标、当前打开的高亮、未保存的脏点。点文件调 `onOpenFile(path)`，`FileManager` 拿它去开标签页——复用已有的「按路径开文件」入口（`jump.file` 走的就是这条）。
- 右键菜单用现有的 `Menu.tsx`：重命名、删除、下载、复制路径。只读目录里这些项 `disabled`，理由跟列表工具栏一致。
- **过滤框**：只在已加载的节点上筛名称，不递归请求未展开的目录。命中的路径自动展开。这个语义要写在组件注释里，否则「筛不到」会被当成 bug。
- 树顶的工具栏（上传 / 新建文件 / 新建文件夹 / 刷新 / 折叠树）放在 `FileManager` 渲染的容器里，不进 `FileTree`——那些动作的实现都在 `FileManager`，树只管画树。

**顺手的重构**：`FileManager.tsx` 现在 1731 行，其中 1378–1660 是一段自成一体的图标模块（`GlyphName` / `GLYPHS` / `Glyph` / `Kind` / `KIND_BY_EXT` / `GLYPH` / `TONE` / `kindOfName` / `extensionOf`）。树要用同一套图标，所以把它抽成：

- `web/src/components/Glyph.tsx`：`Glyph` 组件和它的字形表。
- `web/src/components/FileIcon.tsx`：`kindOfName`、`FileIcon`（按扩展名给出带色调的图标）。

`FileManager` 和 `FileTree` 都从这里引。不做别的重构。

### 4. 高亮叠层

新增 `web/src/highlight.ts`：

- 依赖 `prismjs` + `@types/prismjs`。从 `prismjs/components/prism-core` 引核心（不带默认语言），按需引 yaml / json / toml / markdown / properties / ini / bash 七个语法。
- 导出 `highlight(code, lang)`，和一张扩展名 → Prism 语言 id 的表。现有的 `languageOf()` 只返回给人看的名字，扩成同时给出 Prism id，两处共用一张表。
- 未知扩展名返回 null，按纯文本处理（不挂高亮层）。

编辑器 DOM：

```
.editor                     flex 容器，不变
  .editor__gutter           行号栏，不变
  .editor__wrap             新增：position: relative; flex: 1; min-width: 0
    pre.editor__hl          新增：绝对定位铺满，overflow: hidden，pointer-events: none，aria-hidden
    textarea.editor__text   position: relative; color: transparent; caret-color: var(--text)
```

对齐的三个硬约束：

- `.editor__gutter` / `.editor__hl` / `.editor__text` 共用同一条 `padding` / `font-family` / `font-size` / `line-height` / `tab-size` / `white-space: pre` 声明。现在样式表里那条 `.editor__gutter, .editor__text` 扩成三个选择器，别再写第二份。
- 软换行继续关着（`wrap="off"`）。一条软换行在屏幕上占两行、在行号栏占一个号，三层就全错开了。
- **高亮层的内容末尾补一个换行符**。`<pre>` 会吞掉末尾换行，不补的话文件最后一行的着色会往上错一行。

滚动：textarea 现有的 `onScroll` 已经在同步行号栏，同一个 handler 里再同步高亮层的 `scrollTop` 和 `scrollLeft`。

安全：`Prism.highlight()` 的输出里，文本内容已经做过 HTML 转义，只有 Prism 自己生成的 `<span class="token …">` 是标签，所以用 `dangerouslySetInnerHTML` 挂上去是安全的。这一点要写进注释——否则下一个读到的人有理由怀疑这里能被一个恶意的 `config.yml` 注入。

性能：

- tokenize 走 `useDeferredValue(editor.content)`，不要每个按键全量重算。
- 沿用现有的 40 万字符阈值（现在用它决定要不要渲染行号）。超过阈值就不挂高亮层，textarea 的 `color` 恢复成 `var(--text)`，状态栏加一句「文件过大，已关闭高亮」。两万行的日志照样能开，只是没颜色。

颜色**全部走令牌**，light / dark 两个块都加，一行裸 hex 都不写：

`--code-comment` / `--code-key` / `--code-string` / `--code-number` / `--code-bool` / `--code-punct` / `--code-heading` / `--code-selection`。

映射到 Prism 的 class：`.token.comment/.prolog` → comment；`.token.key/.property/.attr-name` → key；`.token.string/.attr-value` → string；`.token.number` → number；`.token.boolean/.null/.keyword/.important` → bool；`.token.punctuation/.operator` → punct；`.token.title/.bold` → heading。**不引 Prism 自带的主题 CSS**。

`color: transparent` 的 textarea 里选区仍然可见（选区画的是背景），但要给它一个明确的 `::selection` 背景令牌，不能靠浏览器默认色——深浅两套里它都得压得住高亮层的字。

新令牌不准碰 `--term-*`（服务器控制台）和 `--shell-*`（主机 shell）。那两块画布在明暗两种模式下都要保持深色且明显不同色，这是防止把危险命令敲进错误终端的唯一屏障。

### 5. 编辑器铺满高度

普通模式下 `.editor` 是 `height: min(58vh, 640px)` 且可纵向 resize，这个不变。编辑模式下：

- `.fm--editing .editor-pane`：`display: flex; flex-direction: column; flex: 1; min-height: 0`。
- `.fm--editing .editor`：`height: auto; flex: 1; min-height: 0; resize: none`。
- 从上到下每一层都要 `min-height: 0`，否则 flex 子项按内容撑开，编辑器会把页面顶出一条纵向滚动条。这是本仓库最高频的布局 bug。
- 编辑模式下不显示「← 文件列表」按钮（那是 1024 以下窄屏用的）。
- 空态：编辑模式下一个文件都没开时，右侧占满的位置提示「从左边的树里点一个文件」。

### 6. 响应式

| 宽度 | 行为 |
| --- | --- |
| ≥1200 | `.fm--editing` 栅格 `260px minmax(0, 1fr)`，树常驻 |
| 1024–1200 | 进入时树默认折叠；展开时绝对定位盖在编辑器上，不挤压它 |
| <1024 | **不提供编辑模式**，按钮不渲染 |

<1024 本来就是 `narrowPane` 单栏来回切，已经等价于全屏编辑器，再加一个模式只会多一个状态。已经在编辑模式时把窗口缩到 1024 以下，要自动退出——复用 `useMediaQuery(DRAWER_QUERY)`。

**1024 这个断点在两处**：`styles.css` 的媒体查询和 `App.tsx` 的 `DRAWER_QUERY`。改一处必须改另一处。本次新增的 1200 断点只在 `styles.css` 和 `FileManager` 各出现一次，同样要成对。

## 数据流

```
App(workspace) ──props──> InstanceView ──props──> FileManager(editing)
  │                                                   │
  │  data-rail / railLocked                           │ onWorkspaceChange(bool)
  ▼                                                   ▼
Sidebar（图标条，折叠按钮锁住）              FileTree(showFiles) / FileEditor(高亮)
```

编辑模式的真值只有 `FileManager` 里的 `editing` 一个。`App` 的 `workspace` 是它的镜像，唯一用途是决定外壳形态。

## 错误处理

- 树里读不动的目录仍然塌成叶子，不报错——真正的失败由中间列表报告，两份同样的话是一份太多。编辑模式下列表不在场，所以这类目录直接显示为空。
- 右键删除走现有的 `ask()` 确认。删掉的文件如果开着标签页，标签页要一起关掉，不能留一个指向不存在文件的编辑器。
- 保存失败、上传失败的提示照旧走 `toast` 和 `alert--error`，编辑模式不另起一套。
- 只读目录（角色范围之外）里的工具栏按钮 `disabled`，title 说明原因，跟列表工具栏一致。

## 测试与验证

前端没有单测，`tsc -b` 是唯一的自动检查，所以：

1. `npm --prefix web run build` 必须通过。
2. 人工在 **1440 / 1200 / 1024 / 768 / 390** 五个宽度 × **明暗两套主题**下确认：无横向溢出、无错位、高亮层和行号栏与文字三者严丝合缝对齐（拿一个有中文注释的 YAML 和一个缩进很深的 JSON 对着看）。
3. 重点回归三处：折叠侧栏、打开抽屉、开着控制台的实例页。
4. 专项确认：
   - 进入编辑模式 → 切到控制台 → 侧栏恢复成用户原本的宽度（不卡在图标条）。
   - 进入编辑模式 → 窗口缩到 1024 以下 → 自动退出。
   - 开一个两万行的日志 → 状态栏说高亮已关闭，滚动不卡。
   - 编辑模式下改一个文件不保存 → 退出模式 → 内容和光标都还在。
   - 两块终端画布的配色没被新的 `--code-*` 影响。
5. `CHANGELOG.md` 的「未发布」小节加一条用户可见的行为变化。

## 风险

| 风险 | 应对 |
| --- | --- |
| 侧栏卡在图标条 | `active` 变化和卸载两条路径都要归还，专项验证里单列一条 |
| 三层文字对齐漂移 | 字体行高声明只写一份，三个选择器共用；验证时拿中文注释的 YAML 实测 |
| Prism 体积进单文件二进制 | 用 `prism-core` 按需引七个语法，构建后核对产物增量 |
| `:has()` 不被支持 | 面板支持的浏览器都已实现；真出问题时退路是给 `InstanceView` 加一个 `full` prop |
| 大文件卡顿 | 沿用 40 万字符阈值 + `useDeferredValue` |
