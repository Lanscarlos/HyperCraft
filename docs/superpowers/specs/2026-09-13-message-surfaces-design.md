# 消息、状态与任务进度：三个面的分工 设计文档

面板里「给人看一句话」这件事，目前由一个 CSS 类 `.alert` 兼职了五种语义。这份文档把它拆成
各自有名字、有组件、有守卫的三个面，并把 toast 升级成能承载「必须被读到」的消息。

范围是 A+B 两步（toast 升级 + 消息迁移 + 条件内容改名）。任务进度的去重（C）和下载队列
泛化成任务队列（D）不在本文档内，但本文档为它们留好位置。

## 背景

### 现状一：`.alert` 一个类兼职五种语义

`.alert` 定义在 `web/src/styles.css:2920`，四个修饰符 `--error / --warn / --ok / --dismiss`，
在 45 个文件里被用了 143 次。读完全部 143 处之后，它们其实是五种不同的东西：

按修饰符点数是 `--error` 81、`--warn` 29、`--ok` 21、裸 `.alert` 10，合计 141 个元素
（另有 `.alert__body` ×2、`.alert__close` ×1、`.alerts` ×1 是它的子部件）。按语义归类：

| 语义 | 约计 | 例子 | 该去哪 |
| --- | --- | --- | --- |
| **P** 页面/段级加载失败 | 59 | `UsersPage.tsx:143` `{error && …}` | 留在页头（页面语法规定） |
| **C** 条件内容 / 表单实时校验 | 53 | `NewInstanceWizard.tsx:1339`「超过本机内存八成」 | 改名 `.note` |
| **J** 队列任务的页内渲染 | 16 | `JavaPage.tsx:668`「正在解压…」 | 留着，C 阶段收组件 |
| **M** 一次性操作结果 | 13 | `ServerConfigPage.tsx:293` `{status && …}` | 进 toast |
| **D** 主机巡检告警 | 1 | `Dashboard.tsx:134`（模板字符串类名） | 不动（`alerts.ts` 自成一套） |

数字是逐条读完 141 处后的归类，落地时仍以每一处的实际写法为准——判据在下面「三个面」一节。

P 和 C 长得一样、用同一个类名，是这份文档存在的全部原因。

### 现状二：`.alert` 是控件体系里唯一的漏网之鱼

仓库对「共用控件必须是组件」是有强制的，`web/scripts/check-ui.mjs` 里有
`ruleBadgesAreComponents`、`ruleEmptyStatesAreComponents`、`ruleSectionsAreComponents`、
`ruleDropdownsAreOurs`——裸写 `<div className="badge">` 构建直接失败。

`.alert` 用了 143 次，**既没有组件，也没有守卫，在 `docs/design-system.md` 的控件清单里没有条目**。
它在全部设计文档里只被提到一次：`.claude/skills/frontend-design/SKILL.md:25` 的页面语法，
写着「错误永远紧贴页头，上面不放别的」——那说的只是 P。

两个可查的后果：

- **tone 用错没人管。** `DatabasePage.tsx:107` 把 `platform.warning` 画成 `alert--error`（红色「出错了」），
  `JavaPage.tsx:219` 同样。拼错类名会被 `ruleNoUndefinedClasses` 抓，用错 tone 不会。
- **错误只能活在渲染它的子树里。** 页面一卸载就没了。

### 现状三：toast 已经是对的形状，但只有一档

`web/src/toast.ts` + `web/src/components/Toast.tsx`：右下角、底边锚定向上堆叠、每条独立计时、
最多 4 条、portal 到 body。这些行为本身不需要改。

缺的是：

- `toast(message: string)` 只有一个签名，视觉硬编码绿色 ✓（`styles.css:14184` `content: '✓'`）
- **没有「不自动消失」**。`toast.ts:38` 的注释明确写了「错误不走这里，因为会消失的消息会被错过」——
  这条决定是对的，但结论下错了：该加的是常驻，不是把错误挡在门外。

### 现状四：错误已经在偷偷走 toast，而且画成绿色 ✓

规则写在注释里没人执行，于是有 8 处失败消息正在以「绿色 ✓ + 成功」的样子弹出：

```
SchematicMarket.tsx:96    下载失败
SchematicMarket.tsx:328   保存失败
SchematicMarket.tsx:345   移除失败
SchematicLibraryPage.tsx:81  上传失败
SchematicPreview.tsx:134  （err.message）
SchematicPreview.tsx:136  入库失败
DatabasePage.tsx:493      复制失败，手动选中复制吧
VelocityConfig.tsx:184    复制失败，手动选中复制吧
```

### 现状五：「常驻 + 知道了」已经被各自发明了三遍

用户要的那个交互，仓库里自己长出来过三次，每次都是页面内的 `.alert` + 一个手写按钮：

| 位置 | 写法 |
| --- | --- |
| `SchematicLibraryPage.tsx:386` | `.alert--ok` + `<button className="link">知道了</button>` |
| `FileManager.tsx:1142` | `.alert--error` + `<button className="link">知道了</button>` |
| `App.tsx:616` | `.alert--error.alert--dismiss` + `.alert__close` |

`.alert--dismiss` 这个修饰符在全仓库只被用了 1 次（`App.tsx:616`），它的注释说的正是本文档的论点：
「nothing about navigating away means the failed start has been read, so it does not go on its own」。

## 设计

### 三个面，各有各的名字

| 面 | 问题 | 载体 | 生命周期 |
| --- | --- | --- | --- |
| **消息** | 刚才发生了什么 | toast（右下角） | 事件驱动：弹出 → 自动淡出，或常驻到被确认 |
| **状态** | 现在是什么情况 | `.alert`（页头错误）/ `.note`（条件内容） | 条件驱动：条件为真时存在 |
| **任务** | 正在做什么 | 下载队列 + 页内进度条 | 任务驱动：跟着 job 状态机 |

判据一句话：**能用 `if (条件)` 渲染的，不是消息；只能在事件处理器里调用的，才是消息。**

### 1 · toast 升级

`web/src/toast.ts`：

```ts
export type ToastTone = 'ok' | 'warn' | 'error'

export interface ToastItem {
  id: number
  message: string
  tone: ToastTone
  /** 不自动消失，必须点「知道了」。error 默认 true，其余默认 false。 */
  sticky: boolean
  /** 同 key 的新消息替换旧的那条（连点保存不该堆六条「已保存」）。 */
  key?: string
}

interface ToastOptions {
  key?: string
  sticky?: boolean
}

export function toast(message: string, opts?: ToastOptions): void       // tone 'ok'
export function toastWarn(message: string, opts?: ToastOptions): void   // tone 'warn'
export function toastError(message: string, opts?: ToastOptions): void  // tone 'error'，sticky 默认 true
```

**为什么是三个具名函数而不是 `toast(msg, {tone})`**：调用点散在四个文件的 promise 链里，
`toastError(err.message)` 比带 tone 的短；而且 `grep toastError` 能一眼数清全站有多少个失败出口，
带 options 的写法数不出来。现有 55 处 `toast(...)` 调用一个字都不用改。

**为什么需要 `key`**：现在 6 处 `{status && <div className="alert alert--ok">{status}</div>}` 是
「一个槽位，后写的覆盖先写的」。搬到 toast 后连点三次保存会堆三条一样的「已保存」。
`key: 'save'` 让它退回覆盖语义。这是 `toast.ts:8` 那段注释说的「一个槽位」失败模式的**反面**——
那里说的是「不同的消息不该互相覆盖」，这里说的是「同一条消息不该重复」，两者不冲突。

停留时间：

| tone | 默认 |
| --- | --- |
| `ok` | 6s（`LINGER` 不变） |
| `warn` | 10s |
| `error` | 常驻 |

**淘汰规则**（`MAX_STACKED` 改动）：现在是超过 4 条挤掉最老的。改成：

- 总上限 6
- 淘汰时**优先挑最老的非常驻**
- 全是常驻时才挤最老的常驻

挤掉第 5 条常驻是可接受的损失，因为常驻 toast 不是唯一记录：下载失败在下载页的历史里，
页面级失败在页头的 `.alert` 里。toast 是提醒，不是档案。这一条要写进代码注释。

### 2 · `Toast.tsx` 改动

- `data-tone={item.tone}` 作为样式钩子
- `role`：`error` 用 `role="alert"`（立刻播报），其余保持 `role="status"`
- 常驻的那条不起 `setTimeout`
- 关闭控件按语义分两种：
  - 非常驻：右上角 `×`（现状），读作「关掉这个打扰」
  - 常驻：文末一个「知道了」文字按钮，读作「我已确认」——沿用
    `SchematicLibraryPage.tsx:386` / `FileManager.tsx:1142` 已有的 `<button className="link">` 写法
- `useDismiss` / 独立计时 / `reducedMotion` 全部原样

不加 Esc 关闭：会和对话框、抽屉的 Esc 抢。

### 3 · 样式（`styles.css` 的 toast 区块，约 14115 行起）

```css
.toast[data-tone='warn']  { border-left-color: var(--caution); }
.toast[data-tone='warn'] .toast__mark { background: var(--caution-soft); color: var(--caution-ink); }
.toast[data-tone='warn'] .toast__mark::before { content: '!'; }

.toast[data-tone='error'] { border-left-color: var(--danger); }
.toast[data-tone='error'] .toast__mark { background: var(--danger-soft); color: var(--danger-ink); }
.toast[data-tone='error'] .toast__mark::before { content: '×'; }
```

`--caution-*` 和 `--danger-*` 在 light / dark 两个令牌块里都已存在（`styles.css:153-160`、`369-373`），
**不需要新增令牌**。属性选择器不受 `ruleNoUndefinedClasses` 管辖（它只查 className 字面量）。

常驻 toast 的「知道了」按钮复用 `.link`，不新增类。

### 4 · 消息迁移（M 类，约 14 处）

**4.1 修掉现存的 8 处「失败画成绿色 ✓」**

上文「现状四」那 8 处改为 `toastError`；其中两处剪贴板失败（`DatabasePage.tsx:493`、
`VelocityConfig.tsx:184`）用 `toastWarn` 且不常驻——不是系统坏了，是浏览器不给剪贴板权限，
用户下一步就是手动选中复制，不需要确认。

**4.2 六处「保存成功」槽位搬进 toast**

| 位置 | 现状 |
| --- | --- |
| `InstancePlugins.tsx:259` | `{status && <div className="alert alert--ok">{status}</div>}` |
| `LaunchSettings.tsx:874` | 同上 |
| `InstanceCorePicker.tsx:178` | 同上 |
| `VelocityConfig.tsx:541` | 同上 |
| `PropertiesEditor.tsx:266` | 同上 |
| `ServerConfigPage.tsx:293` | 同上 |

改成 `toast(msg, { key: '<页面>.save' })`，并删掉各自的 `status` state。

**4.3 三处已有的「常驻 + 知道了」原型收编**

| 位置 | 改成 |
| --- | --- |
| `SchematicLibraryPage.tsx:384` 「已入库 N 个建筑」+ 知道了 | `toast(msg, { sticky: true })` |
| `FileManager.tsx:1140` error + 知道了 | `toastError(msg)` |
| `App.tsx:615` powerError + `.alert--dismiss` | `toastError(msg)` |

`App.tsx:615` 是最该搬的一处：开关机失败现在挂在实例页顶部，人一切走就没了，而开关机
恰恰是最可能切走去看别处的操作。

搬完之后 `.alert--dismiss` 和 `.alert__close` 两条规则没有使用者，一并删除。

**4.4 不搬的**

`NetworkPage.tsx:167`「改了这些：」带一个 `<ul>` 列表，`PluginDrawer.tsx:227`
「装好了」带一个跳转按钮。toast 是一行字加一个动作，装不下列表。

这两处归到 C 类改名 `.note--ok` 而不是硬塞进 toast：它们都是 `{notes.length > 0 && …}` /
`{done && …}` 这样从**留存的 state 条件渲染**出来的，符合「能用 `if (条件)` 渲染的不是消息」
这条判据，改名是诚实的而不是权宜。

### 5 · 条件内容改名（C 类，约 45 处）

`.alert--warn`（29 处，减去 `PluginLibraryDrawer.tsx:208` 那条真·失败消息）、全部裸 `.alert`（10 处）、
以及 `ScriptDraft.tsx:95`、`PathPicker.tsx:104`、`ImportInstanceDialog.tsx:307/315`、
`CoreLibraryPage.tsx:118`、`NewInstanceWizard.tsx:879/1781` 这些条件性的 error/ok，
全部改成新的 `.note`：

```
.note          中性（现在的裸 .alert）
.note--warn    需要读一下再动手
.note--ok      良性的条件说明（「这个目录还不存在，选它会一并建好」）
.note--error   条件性的坏消息（「这个目录已被别的实例占用」）
```

视觉与现在的 `.alert` 完全一致——这是纯改名，**不改任何像素**，只有两处例外：

「现状二」举证的两处 tone 用错在改名时一并修正——`DatabasePage.tsx:107` 和 `JavaPage.tsx:219`
把 `platform.warning`（平台不支持的说明）画成了红色 `alert--error`，改为 `.note--warn`。
这两处会有可见的颜色变化，是有意的。

改名的收益是让守卫能区分「消息」和「条件内容」，这正是上面那两处用错能混过构建的原因。

配套一个组件 `web/src/components/Note.tsx`：

```tsx
export function Note({ tone = 'neutral', children }: {
  tone?: 'neutral' | 'ok' | 'warn' | 'error'
  children: ReactNode
}) 
```

### 6 · 收尾后 `.alert` 只剩一个含义

M 搬走、C 改名之后，`.alert--ok` 和 `.alert--warn` 归零，`.alert--dismiss` 删除。
`.alert` 只剩 P 类的页面级错误——**正好是 `frontend-design/SKILL.md:25` 页面语法里写的那一行**。

于是 `.alert--error` 这个修饰符也没必要了，合并回 `.alert`：一个类，一个含义，
「页面级错误，紧贴页头」。

### 7 · 守卫

`web/scripts/check-ui.mjs` 加一条 `ruleMessageSurfaces`，两条硬规则：

1. **`.alert` 不得带任何修饰符。** `alert--ok` / `alert--warn` / `alert--error` / `alert--dismiss`
   出现即失败。改完之后 `.alert` 只有一个含义，带修饰符就说明有人在拿它当别的用。
2. **`.note*` 必须通过 `<Note>` 组件。** 裸 `<div className="note">` 失败，
   与 `ruleBadgesAreComponents` / `ruleSectionsAreComponents` 同一形态。

「`.alert` 只能紧贴页头」这条**不进守卫，只进文档**。它要判断的是 JSX 里的位置关系，
静态判定要么漏要么误伤（`{cond ? <div className="alert"/> : <Skeleton/>}` 这种三元分支在
`ConfigHistory.tsx:326`、`NetworkPage.tsx:142`、`VelocityConfig.tsx:197` 都有），
一条会误报的守卫比没有守卫更糟——它会教人写 `// eslint-disable` 式的绕过。

### 8 · 文档

- `docs/design-system.md` 第 3 节控件清单补 `Note` 和 `toast` 两个条目，写清三个面的分工和判据
- `.claude/skills/frontend-design/SKILL.md:25` 的页面语法那行加一句：`.alert` 仅限页面级错误
- `CHANGELOG.md`「未发布」：失败提示不再显示为绿色成功；失败提示不再自动消失；开关机失败
  切换页面后仍然可见

## 不做的

- **J 类（16 处任务进度渲染）不动。** 它们已经在读队列了（`job.state === 'extracting'`），
  问题是 6 份几乎一样的实现（`CoreLibraryPage.tsx:235`、`DatabasePage.tsx:1009`、
  `NewInstanceWizard.tsx:934` 和 `:1176`、`JavaPage.tsx:626`，加上 `DownloadsPage` 和
  `DownloadTray` 两个正主）。收成一个组件是独立的一次改动（C 阶段）。
- **D 类（下载队列泛化成任务队列）不做。** `internal/download` 已经约 80% 通用（Kind、
  六态状态机、Cancel、去重、历史、并发槽、按能力过滤），绑死下载的只有 `Request` 的
  `Attempts` / `Install` / `Digest` / `TempDir` 四个字段。但今天没有第二个客户：非下载的长任务
  只有 `confighist` 的 Prune 和 `plugin/picks`（都是后台清理，没人要看进度），以及
  `selfupdate`（自己有一套 `Progress int`，但它跑完会重启面板，放进任务列表语义很怪）。
  仓库里没有备份、没有异步导入导出。泛化应该跟着第一个真需求走。
- **P 类（35 处页面级错误）不动。** 页面语法规定它们紧贴页头。
- `Dashboard.tsx` 的巡检告警不动，`alerts.ts` 自成一套。

## 验证

前端没有单测，`tsc -b` 是唯一的自动检查。

```bash
npm --prefix web run build   # tsc -b + vite build + check:ui
make lint && make test       # 本文档不动后端，但改名可能波及 embed 产物
```

人工确认清单：

- 明暗两种模式 × 三种 tone 各看一遍
- 常驻 toast 确实不自动走；点「知道了」才消失
- ok 和常驻混合堆叠时，ok 过期后常驻那条不跳动（列是底边锚定的，从顶部缩短）
- 连点保存三次只出现一条「已保存」（`key` 生效）
- 1440 / 1200 / 1024 / 768 / 390 五个宽度无横向溢出；390px 下 toast 宽度是
  `min(420px, calc(100vw - 36px))`，不顶边
- 改名后逐页扫一遍：条件说明的视觉与改名前**逐像素一致**
- 折叠侧栏、打开抽屉、开着控制台的实例页三处未被波及

## 落地顺序

1. toast 升级（`toast.ts`、`Toast.tsx`、`styles.css`）——可独立验证，先做
2. 8 处「失败画成绿色」修正——最小、收益最直接
3. M 类迁移（4.2、4.3）
4. `Note` 组件 + C 类改名——面最广，但纯机械
5. `.alert--error` 合并回 `.alert`、删死规则
6. 守卫 + 文档 + CHANGELOG
