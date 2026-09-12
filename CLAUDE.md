# CLAUDE.md

给 Claude Code 的项目说明。改动前先读这一页。

## 项目是什么

HyperCraft 是自托管的 Minecraft 服务器面板：Go 守护进程持有服务端进程，React 面板通过 HTTP/WebSocket 操作它。构建产物是**单文件二进制**——前端 `npm run build` 的结果被 `//go:embed` 进 `internal/webui/dist`，所以改了前端不重新构建前端，二进制里就还是旧界面。

核心不变量：服务端进程属于守护进程，不属于任何一个请求或连接。关标签页、断网、退出登录都不能影响它。

## 目录

| 路径 | 内容 |
| --- | --- |
| `cmd/hypercraft` | 入口 |
| `internal/` | 后端：实例管理、进程、WebSocket、鉴权、插件、Java 环境等 |
| `internal/webui/dist` | 前端构建产物的嵌入目录（生成物，不要手改） |
| `web/src` | 前端源码（React 18 + TypeScript + Vite） |
| `web/src/components` | 全部组件 |
| `web/src/styles.css` | **全部样式，唯一一个样式文件** |
| `deploy/` | systemd unit |
| `.claude/skills` | 随仓库走的 skill（本地和云端都自动加载），见该目录的 README |

## 常用命令

```bash
make build          # 构建前端 + 后端，产出 ./hypercraft
make build-go       # 只构建后端，复用已有的前端产物
make lint           # gofmt + go vet
make test           # go test -race ./...
npm --prefix web run build   # tsc -b + vite build（前端唯一的检查手段）
npm --prefix web run dev     # Vite 开发服务器 :5173，后端另起 go run ./cmd/hypercraft
```

CI（`.github/workflows/ci.yml`）跑的是 `make lint` → `make test` → `make build`。前端没有单测和 lint，**`tsc -b` 是唯一的自动检查**，所以样式改动必须靠人工在多个宽度和明暗两种模式下确认。

## Skill：动手之前先挑一个

`.claude/skills/` 里的 skill 跟着仓库走，本地和云端（claude.ai/code）都会自动加载——插件装在本地是没用的，云端每次从仓库重新克隆。

**任何任务开始前，先看有没有对得上的 skill；有就先调用它，再动手。** 包括「先问个澄清问题」「先翻一下代码」之前——skill 会告诉你该怎么翻。用之前宣告一句「用 X skill 来做 Y」，然后照着它走；发现不合适再放弃。

| Skill | 什么时候用 |
| --- | --- |
| `frontend-design` | 面板界面的布局、栅格、间距、响应式、主题令牌、动效 |
| `brainstorming` | 要做新功能／改行为，需求和设计还没定死 |
| `writing-plans` | 需求清楚了，多步骤改动，写代码之前先出方案 |
| `subagent-driven-development` / `executing-plans` | 按方案逐条实现（前者用子 agent，后者单线程） |
| `test-driven-development` | 实现功能或修 bug，写实现代码之前 |
| `systematic-debugging` | 遇到 bug、测试挂了、行为不符合预期 |
| `verification-before-completion` | 要说「做完了／修好了／过了」之前，先跑命令拿证据 |
| `requesting-code-review` / `receiving-code-review` | 提交或合并前自查；以及收到评审意见之后 |
| `finishing-a-development-branch` | 实现完成、测试通过，决定怎么合回去 |
| `dispatching-parallel-agents` | 有两件以上互不依赖的事可以并行 |
| `using-git-worktrees` | 需要跟当前工作区隔离的分支作业 |
| `writing-skills` | 新增或修改 skill 本身 |
| `using-superpowers` | 上面这套规矩的总纲 |

除 `frontend-design` 外都来自 [obra/superpowers](https://github.com/obra/superpowers)（MIT）。来源、引入版本和同步方法见 `.claude/skills/README.md`。

## 前端界面布局优化

**只要任务涉及界面布局、栅格、间距、响应式、导航壳、组件排布、主题令牌或动效，先调用 `frontend-design` skill**（`.claude/skills/frontend-design/SKILL.md`），按它的规则做，不要凭直觉改样式。

skill 里是完整规则，这里只列最容易踩的几条：

- **一个页面框**：所有面板级页面用 `components/Page.tsx`，只有三种形态——「散文」`--content-max`(880px)、「瓦片（`wide`）」`--content-max-wide`(1440px)、「全屏工作区（`full`）」不设上限。不要再造页面框，不要引第四种。`full` 严格限定于「一屏一件事的工具页」：里面装的是一块占满空间的画布（文件页的编辑模式），不是一段要读的内容。内容页一律在前两种里选。
- **一个样式文件**：所有样式在 `web/src/styles.css`。不引 CSS 框架、组件库、CSS-in-JS，不拆分文件。
- **只用令牌**：颜色、圆角、阴影、时长、缓动都从 `styles.css` 开头的令牌区取，不写裸 hex。新增令牌必须 light / dark 两个块都加。
- **栅格优先内在响应**：`repeat(auto-fill, minmax(<下限>, 1fr))`，能不加断点就不加断点。
- **`min-width: 0`**：任何可能装长文本或终端的 flex/grid 子项都要写（列方向写 `min-height: 0`）。这是本仓库最高频的布局 bug。
- **1024px 断点在两处**：`styles.css` 的媒体查询和 `App.tsx` 的 `DRAWER_QUERY`。改一处必须改另一处。
- **两块终端画布不能长得像**：`--term-*`（服务器控制台）和 `--shell-*`（主机 shell）在明暗两种模式下都保持深色且明显不同色——这是防止把危险命令敲进错误终端的唯一屏障。
- **手机是一等场景**：窄屏下优先保住状态、开关机、控制台。

改完自查：`npm --prefix web run build` 通过；明暗两种模式都看过；1440 / 1200 / 1024 / 768 / 390 宽度下无横向溢出和错位；折叠侧栏、打开抽屉、开着控制台的实例页这三处没被波及。

## 代码风格

- **代码注释用英文，文档（README / CHANGELOG / CI 步骤名）用中文。** 沿用所处文件的语言，不要混。
- 注释解释**为什么**，不解释代码在做什么。仓库里大量注释记录的是踩过的坑和仍然成立的约束——看起来可以简化的写法，先读注释再决定，别急着「清理」。
- Go 代码提交前必须 `gofmt`（CI 直接卡）。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节。把「未发布」改成 `## [x.y.z] - 日期` 并合进 main 会自动发版，所以除非明确要发版，只往「未发布」里加。

## 工作流程

1. 在指定的功能分支上开发（当前：`claude/frontend-layout-optimization-akalew`；没有就基于最新的 `main` 建）。
2. 改动完成后跑通对应的检查：前端 `npm --prefix web run build`，后端 `make lint && make test`。
3. 提交，写清楚**为什么改**而不只是改了什么。
4. **每次任务改完，主动把功能分支合并进 `main` 并推送**——不要停在分支上等人问。顺序是：推功能分支 → 切 `main` → `git pull origin main` → 合并 → 推 `main` → 切回功能分支。
5. 合并前若 `main` 已经前进，先把 `main` 合进功能分支解决冲突，别把冲突带上 `main`。
6. 合并被拒绝或出现无法自行判断的冲突时停下来说明，不要强推。
