# 资源库合并成一页 + 排版稿剩余五条 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Java 环境和数据库环境从「侧栏三个入口、每页一张卡」合回「一页叠三张卡」，并修掉上一轮重排在真实数据下暴露的四处排版问题。

**Architecture:** 路由层把 `LIBRARY_VIEWS` 里 java / database 的多个 view 收成一个，旧 URL 由既有的 `defaultView` 兜底重定向；两个页面组件去掉 `view ===` 分支，改成把各张卡按「你有什么 → 你能装什么 → 从哪里下」的顺序竖着排；卡片本身沿用上一轮已经落地的 `Shelf` / `.pick-grid` / `.chart-head__tools` 原语，只补一个紧凑分段控件和一个「路径独立成列」的行型。

**Tech Stack:** React 18 + TypeScript + Vite，样式全在 `web/src/styles.css`，无 CSS 框架。前端唯一自动检查是 `tsc -b`（`npm --prefix web run build`）。

**Spec:** 设计稿 canvas `HyperCraft 面板界面设计`（https://claude.ai/code/artifact/65792558-bd3b-4c9d-aa22-3aa26b2dbb71），本次依据 `Java.dc.html`、`Database.dc.html` 两块画板，以及用户 2026-09-12 对实际截图的评审意见（五条）。

## Global Constraints

- 只用 `styles.css` 开头令牌区的命名令牌，不写裸 hex；新增令牌必须 light / dark 两个块都加。
- 不新增断点：只能用既有的 1240 / 1024 / 900 / 780 / 560 档。栅格优先 `repeat(auto-fill, minmax(<下限>, 1fr))`。
- 任何可能装长文本的 flex/grid 子项写 `min-width: 0`。
- 代码注释英文，CHANGELOG 中文。注释解释「为什么」。
- 页面框只能用 `components/Page.tsx`，形态只有 `--content-max` / `wide` / `full` 三种。
- 1024px 断点在 `styles.css` 和 `App.tsx:DRAWER_QUERY` 两处，改一处必须改另一处（本计划不改这一档）。
- 旧 URL（`/library/java/install`、`/library/java/source`、`/library/database/engines`、`/library/database/install`）必须仍然能打开，不能 404。
- 验收：`npm --prefix web run build` 通过；`make lint && make test` 通过；截图样张在 1440/1200/1024/768/390 × 明暗两种模式下无横向溢出。

---

### Task 1: 紧凑分段控件 + 路径独立成列的行型（纯样式）

**Files:**
- Modify: `web/src/styles.css`（`.segmented` 区块之后；`.asset` 区块之内）

**Interfaces:**
- Produces: `.segmented--inline`（选项不再 `flex: 1 1 150px`，收成按内容宽度的一排小按钮，`small` 说明改成 `title`），供 Task 3 的镜像类型用；`.asset__sub--path`（把 vendor 收进 `.asset__label`、路径独立成列的行型）供 Task 3、Task 4 用。

- [ ] **Step 1: 加 `.segmented--inline`**

```css
/* A segmented control that is a property of the card rather than the page.
   .segmented sizes its options 1 1 150px, which is right for 启动设置 where
   the control *is* the question and gets a whole row — and wrong in a card
   head, where JRE / JDK is the smallest decision on screen and was coming out
   as the largest element on it. The note drops to a title: at this size there
   is no room for it, and it is the kind of thing you read once. */
.segmented--inline {
  padding: 3px;
  gap: 2px;
  align-self: start;
}

.segmented--inline .segmented__option {
  flex: none;
  padding: 5px 12px;
  font-size: 12px;
}
```

**不做的事（评审第 4 条）：** 设计稿的行是 50px，因为它把安装路径单独放一列、名字格只有一行；本仓库是 ~65px，因为名字下面还有一行 vendor + 路径。改成 7 轨要动 `.asset` 这套 Java / 数据库 / 服务端核心三页共用的网格，还要跟着改 1240 和 1024 两档媒体查询里它的两个塌缩形态，而收益是每行 15px。页面填满之后这不是问题，所以本计划不动它。若将来单独做，入口是 `styles.css` 的 `.asset` 及其两处 `grid-template-columns` 覆盖。

- [ ] **Step 3: 构建**

Run: `npm --prefix web run build`
Expected: 通过（纯 CSS，tsc 不受影响）

- [ ] **Step 4: 提交**

```bash
git add web/src/styles.css
git commit -m "样式：紧凑分段控件，和把安装路径拆成独立一列的行型"
```

---

### Task 2: 路由收成一个 view

**Files:**
- Modify: `web/src/routes.ts:30-47`（`LibraryView` 的注释）、`routes.ts:212-232`（`LIBRARY_VIEWS` 的 java / database）
- Modify: `web/src/components/Sidebar.tsx:580-620`（一个 view 时不渲染页面导航）

**Interfaces:**
- Consumes: 无
- Produces: `LIBRARY_VIEWS.java === [{ id: 'installed', label: 'Java 环境' }]`、`LIBRARY_VIEWS.database === [{ id: 'databases', label: '数据库环境' }]`；`defaultView('java') === 'installed'`、`defaultView('database') === 'databases'` 不变，所以 `parse()` 里认不出的 second 段（`install`/`source`/`engines`）自动落回 `defaultView`，旧 URL 变成重定向而不是 404。`LibraryView` 联合类型保留 `'install' | 'source' | 'engines'` 成员不删——`parse()` 的 `pick` 只按 `LIBRARY_VIEWS` 查，留着不影响，删了会牵动 cores/schematics 的同名 view。

- [ ] **Step 1: 改 `LIBRARY_VIEWS`**

```ts
  // 一页。这三件事频率不同（周看、月装、只设一次），2026-08 因此拆成三页；
  // 拆完每页只剩一张卡，1440 宽的屏幕有七成是空的。合回来的前提是后两件
  // 事都瘦了：可安装是一行一个版本的瓦片而不是当年那一屏大选择器，下载源
  // 收进「可安装」的卡头。已安装仍然在最上面——当初拆页要保的就是这一条。
  java: [{ id: 'installed', label: 'Java 环境' }],
  database: [{ id: 'databases', label: '数据库环境' }],
```

- [ ] **Step 2: 侧栏在只有一个 view 时不渲染那段导航**

`Sidebar.tsx` 的 `LibraryScope` 里，把 `<nav>` 包一层：

```tsx
        {LIBRARY_VIEWS[section].length > 1 && (
          <nav className="sidebar__nav" aria-label={`${entry?.label ?? '资源库'}页面`}>
            …既有内容…
          </nav>
        )}
```

理由注释：

```tsx
        {/* A section with one page has no page list: a single row repeating
            the section's own name under itself is a step that goes nowhere.
            Java 环境 and 数据库环境 are that shape now — see LIBRARY_VIEWS. */}
```

- [ ] **Step 3: 安装中的徽章改挂到 ScopeHead 的 meta 上**

`java.installing` 的「安装中」徽章原来挂在 `install` 那一行；那一行没了，徽章要移到 `libraryMeta()` 里，否则装 Java 时侧栏没有任何提示。找到 `libraryMeta`，在 java 分支里追加 `java.installing ? '安装中' : null`，database 分支同理用 `databases.installing`。

- [ ] **Step 4: 构建 + 手点旧 URL**

Run: `npm --prefix web run build`
Expected: 通过。另外确认 `parse('/library/java/install')` 落到 `{ section: 'java', view: 'installed' }`。

- [ ] **Step 5: 提交**

```bash
git add web/src/routes.ts web/src/components/Sidebar.tsx
git commit -m "Java 环境和数据库环境收成一页：侧栏不再拆三个入口"
```

---

### Task 3: JavaPage 合成一页

**Files:**
- Modify: `web/src/components/JavaPage.tsx`（整个 `JavaPage` 的 return；`TITLES` / `LEADS` 收成常量；`SourcePicker` 从整页降级成「可安装」卡头旁的一段）

**Interfaces:**
- Consumes: Task 1 的 `.segmented--inline`、`.asset__path`、`.asset--flat`；Task 2 的单 view 路由
- Produces: `JavaPage` 不再需要 `view` / `onOpenView` 两个 prop；`App.tsx` 传参要跟着改（同一 Task 内改完）

- [ ] **Step 1: 页面骨架**

一页三块，顺序即「你有什么 → 你能装什么 → 从哪里下」：

```tsx
<Page wide title="Java 环境" lead={JAVA_LEAD} aside={<p className="meta-chips"><span>{summary}</span></p>}>
  {platform.warning && <div className="alert alert--error">{platform.warning}</div>}
  {job && <InstallStatus … />}
  <section className="panel">…已安装…</section>
  <section className="panel">…可安装…</section>
</Page>
```

- [ ] **Step 2: 顶部 chip 合并成一枚**

四枚碎 chip（os/arch、已装几个、共多大、由谁提供）合成一句，照设计稿的「4 个运行时 · 占用 1.4 GB」：

```tsx
aside={
  <p className="meta-chips">
    <span>
      {runtimes.length} 个运行时 · 占用 {formatBytes(totalSize)}
    </span>
    {overview.platform.os && <span>{overview.platform.os}/{overview.platform.arch}</span>}
  </p>
}
```

- [ ] **Step 3: 「可安装」卡头收下发行版和下载源**

卡头右侧用 `.chart-head__tools`，装：`.segmented--inline` 的 JRE/JDK、发行版 `Select`、下载源 `Select`、以及 `显示全部 N 个版本` 链接。Zulu 的自定义镜像地址输入框留在卡片底部，只在 `distribution === 'zulu'` 时出现。

- [ ] **Step 5: 构建**

Run: `npm --prefix web run build`
Expected: 通过，且 `view` / `onOpenView` 的残留引用都已清掉（`noUnusedLocals` 会报）。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/JavaPage.tsx web/src/App.tsx web/src/styles.css
git commit -m "Java 环境合成一页：已安装 + 可安装，下载源收进卡头"
```

---

### Task 4: DatabasePage 合成一页

**Files:**
- Modify: `web/src/components/DatabasePage.tsx`

**Interfaces:**
- Consumes: Task 1、Task 2、Task 3 定下的 `Shelf` 表头列数
- Produces: `DatabasePage` 不再需要 `view` / `onOpenView`

- [ ] **Step 1: 三块竖排**

`我的数据库`（表 + 详情栏）→ `已装引擎`（表）→ `安装引擎`（引擎选择 + 版本瓦片）。`.dbsplit` 只包第一块。

- [ ] **Step 3: 构建**

Run: `npm --prefix web run build`

- [ ] **Step 4: 提交**

```bash
git add web/src/components/DatabasePage.tsx web/src/App.tsx
git commit -m "数据库环境合成一页：我的数据库 + 已装引擎 + 安装引擎"
```

---

### Task 5: 截图核对 + CHANGELOG + 合并

**Files:**
- Modify: `CHANGELOG.md`（「未发布 → 变更」，改写上一轮那条，不要再加一条）

- [ ] **Step 1: 跑样张**

用 `internal/webui/dist/assets/index-*.css` + 假数据的静态页，在 1440/1200/1024/768/390 × light/dark 下截图，脚本检查 `documentElement.scrollWidth === innerWidth`。

- [ ] **Step 2: 后端检查**

Run: `make lint && make test`

- [ ] **Step 3: 改 CHANGELOG**

上一轮那条「照着排版稿重排了一遍」要改写：现在的事实是「合成一页」，不是「三页各自重排」。

- [ ] **Step 4: 提交并按 CLAUDE.md 的顺序合并**

推功能分支 → 切 `main` → `git pull origin main` → 合并 → 推 `main` → 切回功能分支。
