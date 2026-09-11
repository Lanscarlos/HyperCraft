# `.claude/skills/`

这个目录里的 skill 会被 Claude Code 自动加载——本地 CLI 和云端（claude.ai/code）都一样。
云端容器每次都是从仓库重新克隆的，**skill 只有提交进仓库才在云端存在**，装在本地 `~/.claude/`
或者用 `/plugin` 装的插件，云端都看不到。这就是这些文件在仓库里的原因。

## 本仓库自己的 skill

| 目录 | 用途 |
| --- | --- |
| `frontend-design/` | HyperCraft 面板（`web/`）的布局与视觉规则 |

## Superpowers（外部引入）

其余 14 个目录来自 [obra/superpowers](https://github.com/obra/superpowers)，MIT 许可，
是一套通用的工作方法 skill（TDD、系统化排错、方案先行、代码评审、子 agent 编排等）：

`brainstorming` `dispatching-parallel-agents` `executing-plans`
`finishing-a-development-branch` `receiving-code-review` `requesting-code-review`
`subagent-driven-development` `systematic-debugging` `test-driven-development`
`using-git-worktrees` `using-superpowers` `verification-before-completion`
`writing-plans` `writing-skills`

- 引入版本：`v6.3.0`，上游提交 `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`
- 版权：Copyright (c) 2025 Jesse Vincent，MIT License（全文见上游仓库 `LICENSE`）

### 相对上游的唯一改动

上游是以插件形式分发的，skill 之间用插件命名空间互相引用（`superpowers:test-driven-development`）。
直接放进 `.claude/skills/` 后 skill 名不带命名空间，那个前缀会让 Skill 工具找不到目标，
所以**全量去掉了 `superpowers:` 前缀**（`superpowers:X` → `X`），其余内容逐字保留。

上游的 SessionStart hook 没有引入——它的作用是每次会话开头把 `using-superpowers` 的正文注入上下文。
这里改成在 `CLAUDE.md` 里写明「先挑 skill 再动手」，效果一样而且不需要仓库级 hook。

### 怎么同步上游

```bash
git clone --depth 1 https://github.com/obra/superpowers.git /tmp/sp
for d in /tmp/sp/skills/*/; do
  name=$(basename "$d"); rm -rf ".claude/skills/$name"; cp -a "$d" ".claude/skills/$name"
done
grep -rl 'superpowers:' .claude/skills | xargs sed -i 's/superpowers://g'
```

同步完更新上面的版本号和提交号，然后人工扫一眼 diff——上游可能新增或改名 skill。
