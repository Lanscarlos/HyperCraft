# 消息、状态与任务进度三个面 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `.alert` 这一个兼职五种语义的类拆成三个各有名字和守卫的面——消息走 toast、条件内容走 `.note`、页面级错误留给 `.alert`——并把 toast 升级成支持三级 tone 与「常驻到被确认」。

**Architecture:** toast 内核加 `tone` / `sticky` / `key` 三个字段和一条「优先淘汰非常驻」的挤出规则；新增 `<Note>` 组件承接条件内容；`.alert` 收缩到只剩页面级错误、修饰符全部删除；`check-ui.mjs` 加一条守卫锁住这个分工。

**Tech Stack:** React 18 + TypeScript + Vite；样式全部在 `web/src/styles.css` 单文件；守卫是 `web/scripts/check-ui.mjs`（纯 node，无依赖）。

**Spec:** `docs/superpowers/specs/2026-09-13-message-surfaces-design.md`

## Global Constraints

- **样式只写在 `web/src/styles.css`**，不新增样式文件、不引框架、不用 CSS-in-JS。
- **只用令牌**，不写裸 hex。本计划**不新增任何令牌**——`--caution-*`（`styles.css:109-111` / `330-332`）和 `--danger-*`（`153-160` / `369-373`）light/dark 两块都已存在。
- **代码注释用英文，文档用中文**（`CLAUDE.md`）。注释解释**为什么**，不解释代码在做什么。
- **前端没有单元测试**，`npm --prefix web run build`（= `check:ui` → `tsc -b` → `vite build`）是唯一的自动检查。每个任务的验收都以它为准，外加任务内列出的人工确认项。
- **`check:ui` 跑在 `tsc` 之前**（`web/package.json:8`），所以类名问题会先于类型问题失败。
- **改名任务不改任何像素**，两处 tone 修正（Task 9）除外，那两处是有意的颜色变化。
- 分支：`claude/upbeat-einstein-udr0w8`。每个任务一个提交。

## 对 Spec 的一处修正

Spec 的「不做的」一节写着 Dashboard 的巡检告警不动。落地核对时发现这行不通：它用的是
`className={`alert alert--${alert.level}`}`（`Dashboard.tsx:134`、`HostPage.tsx:473`），
Task 12 的守卫要么放它过去（等于给自己开后门），要么误报。

而按 spec 自己的判据——「能用 `if (条件)` 渲染的不是消息」——磁盘只剩 12% 是持续为真的条件，
标准的 C 类。所以**巡检告警一并改名**，见 Task 10。改完 `.alert` 零例外。

---

### Task 1: toast 内核加 tone / sticky / key

**Files:**
- Modify: `web/src/toast.ts`（整文件重写，54 行 → 约 95 行）

**Interfaces:**
- Consumes: 无
- Produces: `ToastTone`、`ToastItem`（新增 `tone: ToastTone`、`sticky: boolean`、`key?: string`）、`ToastOptions`、`toast(message, opts?)`、`toastWarn(message, opts?)`、`toastError(message, opts?)`、`dismissToast(id)`、`useToasts()`

- [ ] **Step 1: 重写 `web/src/toast.ts`**

保留原有的模块级 store 与 `useSyncExternalStore` 写法，保留原注释里关于「为什么是列表不是槽位」和「为什么是模块状态不是 context」的两段——那两段说的约束仍然成立。

```ts
import { useSyncExternalStore } from 'react'

/**
 * The queue behind the corner.
 *
 * A toast used to be a piece of page state — one string, one slot — and that
 * shape has a failure mode the page never showed anyone: the second outcome
 * overwrites the first. 对账 walks the fleet while a download is still landing,
 * and whichever finished last was the only one anybody read. Worse, the slot
 * held its own timer, so a message that arrived two seconds into the previous
 * one's four and a half got whatever was left of them.
 *
 * So outcomes go into a list instead, oldest first, and the stack renders them
 * bottom-anchored: a new one appears in the corner and pushes the older ones
 * up, each keeping its own clock. Nothing is replaced, and nothing is skipped.
 *
 * Module state rather than a context, because the callers are event handlers
 * halfway down a promise chain in four different files, and threading a
 * provider to each of them buys nothing — there is one corner of one screen,
 * and it is the same corner from everywhere.
 */
export type ToastTone = 'ok' | 'warn' | 'error'

export interface ToastItem {
  id: number
  message: string
  tone: ToastTone
  /** Does not leave on its own; the reader has to acknowledge it. Errors
   *  default to this. The rule it replaces — "errors never come through here,
   *  because a message that removes itself can be missed" — was right about
   *  the danger and wrong about the remedy: what an error needs is to stay,
   *  not to be kept out of the one place people look. */
  sticky: boolean
  /** Collapses repeats onto one row. The six 保存成功 slots this replaces were
   *  each "one slot, last write wins", and without a key a triple-click on
   *  保存 would stack three identical 已保存. Distinct from the list-vs-slot
   *  point above: that one is about *different* messages not overwriting each
   *  other, this one is about the *same* message not repeating. */
  key?: string
}

export interface ToastOptions {
  key?: string
  sticky?: boolean
}

/** Past this the corner is a log rather than a report. Raised from four to six
 *  because sticky ones no longer expire on their own and would otherwise crowd
 *  out everything that does. */
const MAX_STACKED = 6

let items: ToastItem[] = []
let seq = 0
const listeners = new Set<() => void>()

function publish(next: ToastItem[]): void {
  items = next
  for (const listener of listeners) listener()
}

/**
 * Drops the oldest thing that was going to leave anyway.
 *
 * A sticky toast is only evicted when every slot holds one, and even then the
 * loss is acceptable: sticky toasts are a reminder, not the record. A failed
 * download is still in 下载 history, a failed page load is still in the page's
 * own 错误 slot. Evicting the oldest of six unread errors costs less than a
 * column that grows until it covers the console.
 */
function evict(next: ToastItem[]): ToastItem[] {
  while (next.length > MAX_STACKED) {
    const oldestExpiring = next.findIndex((item) => !item.sticky)
    next.splice(oldestExpiring === -1 ? 0 : oldestExpiring, 1)
  }
  return next
}

function push(tone: ToastTone, message: string, opts: ToastOptions, stickyByDefault: boolean): void {
  seq += 1
  const item: ToastItem = {
    id: seq,
    message,
    tone,
    sticky: opts.sticky ?? stickyByDefault,
    key: opts.key,
  }
  const kept = item.key ? items.filter((existing) => existing.key !== item.key) : items
  publish(evict([...kept, item]))
}

/** Says that something finished. */
export function toast(message: string, opts: ToastOptions = {}): void {
  push('ok', message, opts, false)
}

/** Says that something finished, but not the way it was meant to. Does not
 *  stay by default: a warning the reader can act on immediately — the
 *  clipboard refused, select it by hand — does not need acknowledging. */
export function toastWarn(message: string, opts: ToastOptions = {}): void {
  push('warn', message, opts, false)
}

/** Says that something failed. Stays until acknowledged. */
export function toastError(message: string, opts: ToastOptions = {}): void {
  push('error', message, opts, true)
}

export function dismissToast(id: number): void {
  publish(items.filter((item) => item.id !== id))
}

export function useToasts(): ToastItem[] {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => items,
  )
}
```

- [ ] **Step 2: 确认类型错误落在预期的地方**

Run: `npm --prefix web run build`
Expected: FAIL。`Toast.tsx` 会因为 `ToastItem` 多了必填字段而报错——这正是 Task 2 要接的地方。
其余 55 处 `toast(...)` 调用**不应该**报错（第二参数可选）。若有别处报错，说明签名设计有误，停下来。

- [ ] **Step 3: 提交**

```bash
git add web/src/toast.ts
git commit -m "toast 内核：三级 tone、常驻、按 key 折叠重复

错误此前被刻意挡在 toast 之外，理由写在注释里：会自己消失的消息会被
错过。理由成立，结论下反了——该加的是「不会自己消失」，不是把最该被
看到的那类消息挡在人唯一会看的角落之外。

淘汰规则跟着改：挤出时优先挑最老的非常驻，全是常驻才挤最老的常驻。
上限从 4 提到 6，因为常驻不再自己让位。"
```

---

### Task 2: Toast 组件与 tone 样式

**Files:**
- Modify: `web/src/components/Toast.tsx:37-84`
- Modify: `web/src/styles.css`（toast 区块，`14195` 起；在 `.toast__mark::before` 之后插入）

**Interfaces:**
- Consumes: Task 1 的 `ToastItem`、`ToastTone`、`dismissToast`、`useToasts`
- Produces: `ToastStack`（签名不变，`App.tsx:863` 无需改动）

- [ ] **Step 1: 改 `Toast.tsx` 的 `LINGER` 与 `Toast` 函数**

把文件顶部的 `const LINGER = 6000` 换成按 tone 分档，并改 import：

```tsx
import { DUR, reducedMotion } from '../motion'
import type { ToastItem, ToastTone } from '../toast'
import { dismissToast, useToasts } from '../toast'
import { useDismiss } from '../useDismiss'

/** Long enough to look up from what you were doing, find the corner and read a
 *  sentence — which is a second or two more than it takes to read one.
 *
 *  Only consulted for a toast that leaves on its own; a sticky one never starts
 *  a clock. An error that is explicitly not sticky gets the warn duration,
 *  because the thing that makes an error worth longer is being unread, and this
 *  one has been declared readable at a glance. */
const LINGER: Record<ToastTone, number> = {
  ok: 6000,
  warn: 10000,
  error: 10000,
}
```

`ToastStack` 不动。`Toast` 函数替换为：

```tsx
function Toast({ item }: { item: ToastItem }) {
  // Stable for the life of this toast, and it has to be: the effect below
  // keys its clock off `close`, so an onDone rebuilt on every render of the
  // stack would restart the countdown of everything already on screen each
  // time something new arrived — a busy minute would leave four toasts that
  // never expire.
  const done = useCallback(() => dismissToast(item.id), [item.id])
  const { leaving, close } = useDismiss(done, DUR.mid)

  useEffect(() => {
    // Reduced motion shortens the exit to nothing, not the reading time — the
    // preference is about movement, not about how fast someone reads.
    if (item.sticky) return
    const timer = window.setTimeout(close, LINGER[item.tone])
    return () => window.clearTimeout(timer)
  }, [close, item.sticky, item.tone])

  return (
    <div
      className="toast"
      data-tone={item.tone}
      data-state={leaving && !reducedMotion() ? 'out' : 'in'}
      // A failure has to reach a screen reader as it lands rather than waiting
      // for a pause in whatever is being read.
      role={item.tone === 'error' ? 'alert' : 'status'}
    >
      <span className="toast__mark" aria-hidden="true" />
      <span className="toast__body">
        {item.message}
        {/* A sticky toast closes by being acknowledged, not by being swatted:
            × reads as "stop bothering me" and 知道了 reads as "I have read
            it", and for the one kind of message that is not allowed to go
            unread the difference is the whole point. */}
        {item.sticky && (
          <button className="link toast__ack" onClick={close}>
            知道了
          </button>
        )}
      </span>
      {!item.sticky && (
        <button className="toast__close" onClick={close} aria-label="关闭">
          ×
        </button>
      )}
    </div>
  )
}
```

同时更新 `Toast` 函数上方的那段文档注释：删掉最后一段「Errors are deliberately not routed here…」，换成：

```
 * Errors come through here now and do not leave on their own. The rule this
 * replaces — errors stay in the page because a message that removes itself can
 * be missed — was right that a failure has to stay, and wrong that the corner
 * cannot hold one: 开关机失败 sat at the top of the instance page, which is
 * the page you leave to go and look at why.
```

- [ ] **Step 2: 在 `styles.css` 的 `.toast__mark::before` 规则之后插入 tone 与 ack 样式**

```css
/* Which of the three kinds of news this is, before the sentence is read.
   Both hues already exist in light and dark; nothing new is minted here. */
.toast[data-tone='warn'] {
  border-left-color: var(--caution);
}

.toast[data-tone='warn'] .toast__mark {
  background: var(--caution-soft);
  color: var(--caution-ink);
}

.toast[data-tone='warn'] .toast__mark::before {
  content: '!';
}

.toast[data-tone='error'] {
  border-left-color: var(--danger);
}

.toast[data-tone='error'] .toast__mark {
  background: var(--danger-soft);
  color: var(--danger-ink);
}

.toast[data-tone='error'] .toast__mark::before {
  content: '×';
}

/* Sits in the sentence rather than beside it: the acknowledgement is the last
   word of the message, not a control bolted to the card. */
.toast__ack {
  margin-left: 8px;
}
```

- [ ] **Step 3: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS。`.toast__ack` 是 BEM 形状，上一步已在 CSS 里定义，`ruleNoUndefinedClasses` 应当放行。

- [ ] **Step 4: 人工确认**

启 `npm --prefix web dev`，在浏览器 console 里逐条触发：

```js
// 三种 tone 各一条，外加一条常驻的 ok
```

确认：明暗两种模式下三种颜色都可辨；常驻那条没有 `×` 只有「知道了」；非常驻那条只有 `×`；
常驻的不会自己走；390px 宽度下 toast 不顶边。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/Toast.tsx web/src/styles.css
git commit -m "toast 三种形态：绿勾、黄叹号、红叉，常驻的用「知道了」收尾

× 读作「别烦我」，「知道了」读作「我已确认」。对唯一一类不允许被
漏读的消息来说，这个区别就是全部意义。"
```

---

### Task 3: 修掉 8 处「失败画成绿色 ✓」

**Files:**
- Modify: `web/src/components/SchematicMarket.tsx:96,328,345`
- Modify: `web/src/components/SchematicLibraryPage.tsx:81`
- Modify: `web/src/components/SchematicPreview.tsx:134,136`
- Modify: `web/src/components/DatabasePage.tsx:493`
- Modify: `web/src/components/VelocityConfig.tsx:184`

**Interfaces:**
- Consumes: Task 1 的 `toastError`、`toastWarn`
- Produces: 无

- [ ] **Step 1: 逐处替换**

这 8 处目前都在调 `toast(...)`，于是失败消息以绿边 + ✓ 弹出。改成：

| 文件:行 | 现在 | 改成 |
| --- | --- | --- |
| `SchematicMarket.tsx:96` | `toast(err instanceof Error ? err.message : '下载失败')` | `toastError(...)` 同表达式 |
| `SchematicMarket.tsx:328` | `toast(err instanceof Error ? err.message : '保存失败')` | `toastError(...)` |
| `SchematicMarket.tsx:345` | `toast(err instanceof Error ? err.message : '移除失败')` | `toastError(...)` |
| `SchematicLibraryPage.tsx:81` | `toast(err instanceof Error ? err.message : '上传失败')` | `toastError(...)` |
| `SchematicPreview.tsx:134` | `toast(err.message)` | `toastError(err.message)` |
| `SchematicPreview.tsx:136` | `toast(err instanceof Error ? err.message : '入库失败')` | `toastError(...)` |
| `DatabasePage.tsx:493` | `toast('复制失败，手动选中复制吧')` | `toastWarn('复制失败，手动选中复制吧')` |
| `VelocityConfig.tsx:184` | `toast('复制失败，手动选中复制吧')` | `toastWarn('复制失败，手动选中复制吧')` |

两处剪贴板用 `toastWarn` 而不是 `toastError`：浏览器不给剪贴板权限不是系统坏了，
而且下一步动作（手动选中复制）就在眼前，不需要人点「知道了」。

每个文件的 import 相应改成 `import { toast, toastError } from '../toast'`（按该文件实际还用不用 `toast` 决定）。

- [ ] **Step 2: 确认没有漏网**

Run: `grep -rn "toast(" web/src --include=*.tsx | grep -i "失败\|错误\|err\."`
Expected: 无输出。有输出说明还有失败消息在走 `toast()`。

- [ ] **Step 3: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add web/src/components/SchematicMarket.tsx web/src/components/SchematicLibraryPage.tsx web/src/components/SchematicPreview.tsx web/src/components/DatabasePage.tsx web/src/components/VelocityConfig.tsx
git commit -m "八处失败消息不再顶着绿色对勾弹出

「错误不走 toast」这条规则只写在 toast.ts 的注释里，没有任何东西执行
它，于是它被破坏了八次：下载失败、上传失败、入库失败，全都带着 ✓ 和
绿色左边框。Task 12 的守卫之后会让这类回潮失败在构建上。"
```

---

### Task 4: 六处「保存成功」槽位搬进 toast

**Files:**
- Modify: `web/src/components/InstancePlugins.tsx:259`
- Modify: `web/src/components/LaunchSettings.tsx:874`
- Modify: `web/src/components/InstanceCorePicker.tsx:178`
- Modify: `web/src/components/VelocityConfig.tsx:541`
- Modify: `web/src/components/PropertiesEditor.tsx:266`
- Modify: `web/src/components/ServerConfigPage.tsx:293`

**Interfaces:**
- Consumes: Task 1 的 `toast(message, { key })`
- Produces: 无

- [ ] **Step 1: 逐文件替换**

六处的形状完全一样：一个 `status` state，加一行 `{status && <div className="alert alert--ok">{status}</div>}`。

对每个文件：

1. 删掉 `const [status, setStatus] = useState<string | null>(null)` 这一行
2. 把所有 `setStatus('…')` 改成 `toast('…', { key: '<组件名小写>.save' })`
3. 把所有 `setStatus(null)` 删掉（toast 自己会走）
4. 删掉那行 `{status && <div className="alert alert--ok">{status}</div>}`
5. 补 `import { toast } from '../toast'`（若该文件尚未引入）

`key` 取值逐文件固定，不要复用同一个字符串——两个页面的保存互相覆盖是 bug：

| 文件 | key |
| --- | --- |
| `InstancePlugins.tsx` | `'instance-plugins.save'` |
| `LaunchSettings.tsx` | `'launch-settings.save'` |
| `InstanceCorePicker.tsx` | `'core-picker.save'` |
| `VelocityConfig.tsx` | `'velocity-config.save'` |
| `PropertiesEditor.tsx` | `'properties-editor.save'` |
| `ServerConfigPage.tsx` | `'server-config.save'` |

- [ ] **Step 2: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS。若报「`status` 已声明但未使用」，说明第 1 步漏删了某个 setter 调用。

- [ ] **Step 3: 人工确认**

打开「服务器配置」页，改一个值，连点三次保存。
Expected: 右下角只有**一条**「已保存」，不是三条。

- [ ] **Step 4: 提交**

```bash
git add web/src/components/InstancePlugins.tsx web/src/components/LaunchSettings.tsx web/src/components/InstanceCorePicker.tsx web/src/components/VelocityConfig.tsx web/src/components/PropertiesEditor.tsx web/src/components/ServerConfigPage.tsx
git commit -m "六处「已保存」从页面里的绿条改成右下角的一条 toast

它们本来就是一次性的操作结果：保存成功这件事在你读完它的那一刻就不再
成立，却占着表单顶上一行直到下次导航。key 让连点保存只留一条——这六处
原本是「一个槽位、后写覆盖先写」，直接搬过去会堆三条一样的。"
```

---

### Task 5: 三处「常驻 + 知道了」原型收编

**Files:**
- Modify: `web/src/components/SchematicLibraryPage.tsx:382-390`
- Modify: `web/src/components/FileManager.tsx:1138-1146`
- Modify: `web/src/App.tsx:615-625`
- Modify: `web/src/styles.css`（删 `.alert--dismiss`、`.alert__close` 两条规则）

**Interfaces:**
- Consumes: Task 1 的 `toast(message, { sticky })`、`toastError`
- Produces: 无

- [ ] **Step 1: `SchematicLibraryPage.tsx`**

`:384` 的「已入库 N 个建筑」+ 手写「知道了」按钮，整块删掉，改由入库成功的那条路径调用：

```tsx
toast(`已入库 ${results.length} 个建筑。`, { sticky: true })
```

`sticky` 是因为这条消息带着一个结论（入了几个），人可能正在别处操作，不该 6 秒后消失。
同时删掉驱动它的 `results` / `onDismiss` state（若删后 `results` 还有别的用途则保留）。

- [ ] **Step 2: `FileManager.tsx`**

`:1140` 的 `.alert--error` + 「知道了」整块删掉，产生这个 error 的路径改调 `toastError(msg)`。
`toastError` 默认 `sticky: true`，「知道了」由组件自带，不需要手写。

- [ ] **Step 3: `App.tsx`**

`:615-625` 的 `powerError` 块整块删掉，`setPowerError(msg)` 改成 `toastError(msg)`，
删掉 `const [powerError, setPowerError] = useState<string | null>(null)`。

这一处是三处里最该搬的：开关机失败现在挂在实例页顶部，而开关机恰恰是最可能立刻切走
去看别处的操作，一切走这条失败就没了。

- [ ] **Step 4: 删掉失去使用者的两条 CSS**

`styles.css` 里删除 `.alert--dismiss`（约 2937 行）和 `.alert__close`、`.alert__close:hover`
（约 2944-2961 行）三条规则及其注释——`.alert--dismiss` 全仓库只有 `App.tsx:616` 一个使用者。

- [ ] **Step 5: 确认没有残留引用**

Run: `grep -rn "alert--dismiss\|alert__close" web/src`
Expected: 无输出

- [ ] **Step 6: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 7: 人工确认**

开一台实例，制造一次启动失败（例如把核心 jar 改名），点开机，然后**立刻切到别的页面**。
Expected: 失败消息在右下角，切页后仍在，点「知道了」才消失。

- [ ] **Step 8: 提交**

```bash
git add web/src/components/SchematicLibraryPage.tsx web/src/components/FileManager.tsx web/src/App.tsx web/src/styles.css
git commit -m "「常驻 + 知道了」这件事不再各写各的

仓库里它被独立发明过三次：SchematicLibraryPage 和 FileManager 各手写了
一个「知道了」按钮，App.tsx 用了全仓库唯一一次 .alert--dismiss。三处都
收进 toast 的 sticky。

开关机失败是其中最该搬的：它此前挂在实例页顶部，而开关机正是最可能
立刻切走去看别处的操作，一切走这条失败就没了。"
```

---

### Task 6: `Note` 组件与 `.note` 样式

**Files:**
- Create: `web/src/components/Note.tsx`
- Modify: `web/src/styles.css`（在 `.alert` 区块之后新增 `.note` 区块）

**Interfaces:**
- Consumes: 无
- Produces: `NoteTone = 'neutral' | 'ok' | 'warn' | 'error'`、`Note({ tone?, className?, children })`

- [ ] **Step 1: 新建 `web/src/components/Note.tsx`**

沿用 `Badge.tsx` 的写法（导出 tone 类型 + `TONE` 映射表 + 默认中性）：

```tsx
import type { ReactNode } from 'react'

/** Exported because Dashboard and HostPage map an AlertLevel onto it. */
export type NoteTone = 'neutral' | 'ok' | 'warn' | 'error'

interface Props {
  /** Colour is a claim that something needs attention. Leave it neutral
   *  otherwise — see 「只有异常才上色」 in docs/design-system.md. */
  tone?: NoteTone
  className?: string
  children: ReactNode
}

const TONE: Record<NoteTone, string> = {
  neutral: '',
  ok: 'note--ok',
  warn: 'note--warn',
  error: 'note--error',
}

/**
 * Something that is true right now.
 *
 * Not to be confused with a toast, which is something that happened, or with
 * `.alert`, which is the one slot under a page head where that page's own
 * load failure goes. The three used to share one class name and that is the
 * whole reason this file exists: 「超过本机内存八成」 and 「已保存」 were both
 * `.alert--ok`-shaped divs, so nothing could tell that one of them belongs to
 * a memory slider and the other to a moment.
 *
 * The judgement, when it is not obvious: anything that can be rendered from
 * `if (condition)` is a note. Anything that can only be raised from an event
 * handler is a toast.
 */
export function Note({ tone = 'neutral', className, children }: Props) {
  const classes = ['note', TONE[tone], className].filter(Boolean).join(' ')
  return <div className={classes}>{children}</div>
}
```

- [ ] **Step 2: 在 `styles.css` 的 `.alert .link` 规则之后新增 `.note` 区块**

与 `.alert` 逐条一致——这是纯改名，视觉不变：

```css
/* ================================================================== note

   A condition, as opposed to an outcome.

   It is the old .alert minus the one job that class keeps: 「这超过了本机内存
   的八成」 moves with the memory slider, 「EULA 还没同意」 is true until
   somebody ticks it, 「终端已开启」 is true for as long as it is. None of them
   is news, and none of them can be said in the corner and taken away — a toast
   that reappeared every time a slider moved would be unusable.

   Identical to .alert on purpose: the split is about what a name lets the
   guard tell apart, not about how these look. */
.note {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 6px;
  padding: 10px 13px;
  background: var(--surface-2);
  border: 1px solid var(--border-strong);
  border-left: 3px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font-size: 13px;
  animation: rise 200ms var(--ease) both;
}

.note--error {
  background: var(--danger-soft);
  border-color: color-mix(in srgb, var(--danger) 34%, transparent);
  border-left-color: var(--danger);
  color: var(--danger-ink);
}

.note--ok {
  background: var(--ok-soft);
  border-color: color-mix(in srgb, var(--ok) 34%, transparent);
  border-left-color: var(--ok);
  color: var(--ok-ink);
}

.note--warn {
  background: var(--caution-soft);
  border-color: color-mix(in srgb, var(--caution) 36%, transparent);
  border-left-color: var(--caution);
  color: var(--caution-ink);
}

.note .link {
  color: inherit;
  font-size: 13px;
}

/* The stacked body a note grows when it carries a heading and its explanation:
   .note is a wrapping flex row, so without this the two sit side by side. */
.note__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.note__body span {
  font-size: 12px;
  opacity: 0.9;
}
```

- [ ] **Step 3: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS。此时 `Note` 尚无使用者，`.note--*` 已定义但未被用——
`ruleNoUndefinedClasses` 只检查「用了但没定义」的方向，不检查反向，所以不会失败。

- [ ] **Step 4: 提交**

```bash
git add web/src/components/Note.tsx web/src/styles.css
git commit -m "新增 Note：现在是什么情况，区别于刚才发生了什么

.alert 此前同时装着「页面加载失败」「表单实时校验」「保存成功」三种
东西，共用一个类名，于是没有任何东西能看出「超过本机内存八成」跟着
滑块走、而「已保存」只是一个瞬间。

样式与 .alert 逐条一致：这次拆分要的是一个守卫能分辨的名字，不是新的
长相。"
```

---

### Task 7: C 类改名 ①——裸 `.alert` → `<Note>`

**Files:**
- Modify: `web/src/components/TerminalSettings.tsx:62`
- Modify: `web/src/components/InstancePlugins.tsx:327`
- Modify: `web/src/components/VelocityConfig.tsx:225`
- Modify: `web/src/components/PropertiesEditor.tsx:196`
- Modify: `web/src/components/ServerConfigPage.tsx:254`
- Modify: `web/src/components/SchematicLibraryPage.tsx:565`
- Modify: `web/src/App.tsx:804`
- Modify: `web/src/components/NewInstanceWizard.tsx:1788`

**Interfaces:**
- Consumes: Task 6 的 `<Note>`
- Produces: 无

- [ ] **Step 1: 机械替换**

每处把 `<div className="alert">…</div>` 换成 `<Note>…</Note>`，并在文件顶部加
`import { Note } from './Note'`（`App.tsx` 用 `'./components/Note'`）。

内容一字不改。这 8 处都是条件说明：

| 位置 | 说的是 |
| --- | --- |
| `TerminalSettings.tsx:62` | `{status.reason}`——这台机器为什么不支持终端 |
| `InstancePlugins.tsx:327` | 这里下载的插件进的是面板的插件库，不是这台服务器 |
| `VelocityConfig.tsx:225` | `velocity.toml` 还不存在 |
| `PropertiesEditor.tsx:196` | `server.properties` 还不存在 |
| `ServerConfigPage.tsx:254` | `{data.path}` 还不存在 |
| `SchematicLibraryPage.tsx:565` | 还没有实例可以装 |
| `App.tsx:804` | 找不到这个实例，它可能已经被删除了 |
| `NewInstanceWizard.tsx:1788` | 目录里还没有服务端 jar |

**注意**：`NewInstanceWizard.tsx:958` 和 `:1203` 也是裸 `.alert`，但它们是 J 类
（`job.state === 'cancelled'` 的分支），本计划不动，留给后续的进度组件收编。

- [ ] **Step 2: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 3: 人工确认**

逐个打开这 8 处所在的界面，确认视觉与改名前**一致**（同样的灰底、同样的左边框、同样的字号）。

- [ ] **Step 4: 提交**

```bash
git add -u && git commit -m "八处中性条件说明改用 Note

都是「现在是什么情况」：配置文件还不存在、这台机器不支持终端、还没有
实例可以装。视觉不变，换的是名字。"
```

---

### Task 8: C 类改名 ②——`.alert--warn` → `<Note tone="warn">`

**Files:** 28 处，分布在
`TerminalSettings.tsx:89,112`、`ConfigHistory.tsx:453,921,976,979`、
`InstancePluginDrawer.tsx:122`、`CoreCatalogue.tsx:327`、`InstancePlugins.tsx:276,680`、
`LaunchSettings.tsx:706`、`RoleDialog.tsx:184`、`HostPage.tsx:157`、
`PluginLibraryPage.tsx:1402,1477`、`SchematicMarket.tsx:173`、`PluginBrowse.tsx:326`、
`NewInstanceWizard.tsx:1036,1097,1104,1339,1711,1795`、`VelocityConfig.tsx:509`、
`SchematicLibraryPage.tsx:393`、`PluginInstallDialog.tsx:180,255,274`

**Interfaces:**
- Consumes: Task 6 的 `<Note>`
- Produces: 无

- [ ] **Step 1: 先处理唯一的例外**

`PluginLibraryDrawer.tsx:208` 是 `<div className="alert alert--warn">检查更新失败：{item.checkError}</div>`——
这是一条**失败消息**，不是条件。它归 M 类，改成在触发检查更新的那个 handler 里调
`toastWarn(\`检查更新失败：${err}\`)`，并删掉这一行和 `item.checkError` 的渲染路径。

不用 `toastError`：这是单个插件的检查失败，插件库本身照常可用，不需要人点「知道了」。

- [ ] **Step 2: 其余 28 处机械替换**

`<div className="alert alert--warn">…</div>` → `<Note tone="warn">…</Note>`，内容一字不改。

两处带额外类名的特殊写法：

- `InstancePlugins.tsx:680` 是 `<div className="alert alert--warn restart-banner">`，
  改成 `<Note tone="warn" className="restart-banner">`
- `SchematicMarket.tsx:173` 和 `PluginBrowse.tsx:326` 带 `key={...}`，
  改成 `<Note tone="warn" key={id}>`——`key` 是 React 内建属性，不需要加进 `Props`

`InstancePlugins.tsx:277` 那条注释提到「`.alert` is a wrapping flex row」，
把注释里的 `.alert` 改成 `.note`，其余不动。

- [ ] **Step 3: 确认无残留**

Run: `grep -rn "alert--warn" web/src`
Expected: 无输出

- [ ] **Step 4: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 5: 人工确认**

重点看这三处布局最复杂的：`InstancePlugins.tsx:680` 的重启横幅（带 `restart-banner`
额外类）、`ConfigHistory.tsx:979` 的版本不一致块（内含嵌套 `<div>`）、
`NewInstanceWizard.tsx:1339` 的内存警告（跟着滑块实时变）。三处都应与改名前逐像素一致。

- [ ] **Step 6: 提交**

```bash
git add -u && git commit -m "28 处黄色警示改用 Note，一处失败消息改走 toast

这批是 .alert--warn 的全部：跟着内存滑块走的、跟着权限勾选走的、跟着
选中版本走的——都是表单的实时校验和影响预览，随输入变化，做成会弹出的
消息等于拖一次滑块弹一条。

例外是 PluginLibraryDrawer 那条「检查更新失败」，它混在警示里但其实是
一次失败，改走 toastWarn。"
```

---

### Task 9: C 类改名 ③——条件性 error / ok，含两处 tone 修正

**Files:**
- Modify: `web/src/components/ScriptDraft.tsx:32,95`
- Modify: `web/src/components/PathPicker.tsx:104,109`
- Modify: `web/src/components/ImportInstanceDialog.tsx:307,315,321`
- Modify: `web/src/components/CoreLibraryPage.tsx:118`
- Modify: `web/src/components/NewInstanceWizard.tsx:879,1781`
- Modify: `web/src/components/PluginSourceDialog.tsx:243`
- Modify: `web/src/components/DatabasePage.tsx:107,759`
- Modify: `web/src/components/JavaPage.tsx:219`
- Modify: `web/src/components/InstancePluginDrawer.tsx:107`
- Modify: `web/src/components/InstancePlugins.tsx:293`
- Modify: `web/src/components/ConfigHistory.tsx:975`
- Modify: `web/src/components/NetworkPage.tsx:167`
- Modify: `web/src/components/PluginDrawer.tsx:227`
- Modify: `web/src/components/SchematicLibraryPage.tsx:621`

**Interfaces:**
- Consumes: Task 6 的 `<Note>`
- Produces: 无

- [ ] **Step 1: 条件性 error → `<Note tone="error">`**

这些是「条件为真时一直成立的坏消息」，不是刚发生的失败：

| 位置 | 说的是 |
| --- | --- |
| `ScriptDraft.tsx:32` | 这个脚本拆不出启动参数 |
| `PathPicker.tsx:109` | 读不了这个目录 |
| `ImportInstanceDialog.tsx:307` | 这个目录已经被别的实例占用 |
| `ImportInstanceDialog.tsx:315` | 这个目录不存在 |
| `ImportInstanceDialog.tsx:321` | 读不了这个目录 |
| `CoreLibraryPage.tsx:118` | 没能取到可下载的核心列表 |
| `NewInstanceWizard.tsx:879` | 同上（向导里的那份） |
| `NewInstanceWizard.tsx:1781` | 实例建好了但有 N 步没做成 |
| `PluginSourceDialog.tsx:243` | 读不到这个仓库 |
| `DatabasePage.tsx:759` | `{install.problem}` |
| `InstancePluginDrawer.tsx:107` | 这个 jar 加载失败 |
| `InstancePlugins.tsx:293` | 有 N 个插件没能加载 |
| `ConfigHistory.tsx:975` | `{plan.blockedBy}` |

- [ ] **Step 2: 条件性 ok → `<Note tone="ok">`**

| 位置 | 说的是 |
| --- | --- |
| `ScriptDraft.tsx:95` | 从脚本里读到了这些 |
| `PathPicker.tsx:104` | 这个目录还不存在，选它会一并建好 |
| `NetworkPage.tsx:167` | 改了这些：（带 `<ul>` 列表，toast 装不下） |
| `PluginDrawer.tsx:227` | 装好了（带跳转按钮） |
| `SchematicLibraryPage.tsx:621` | 装好了。进服打 `{done}` 就能贴出来 |

后三处是 spec 4.4 明确留在页面的：它们都从留存的 state 条件渲染，而且带着 toast
装不下的列表或按钮。

- [ ] **Step 3: 两处 tone 修正——这两处会有可见的颜色变化**

```
DatabasePage.tsx:107  {platform.warning && <div className="alert alert--error">{platform.warning}</div>}
JavaPage.tsx:219      <div className="alert alert--error">{overview.platform.warning}</div>
```

两处都把一个名为 `warning` 的字段（平台不支持的说明）画成了红色「出错了」。
改成 `<Note tone="warn">`。

这是 spec「现状二」举证的那两处——拼错类名会被 `ruleNoUndefinedClasses` 抓，
用错 tone 不会，而这正是 Task 12 的守卫要补的洞。

- [ ] **Step 4: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 5: 人工确认**

- 打开 Java 页和数据库页，确认平台说明现在是**黄色**而不是红色
- 其余各处逐像素与改名前一致

- [ ] **Step 6: 提交**

```bash
git add -u && git commit -m "条件性的好消息坏消息改用 Note，顺手修两处上错的色

这批 error 说的不是「刚才失败了」而是「这个条件下一直成立」：这个目录
被别的实例占着、这个脚本拆不出参数、有 N 个插件加载不了。

DatabasePage 和 JavaPage 把两个叫 warning 的字段画成了红色出错——
拼错类名构建会失败，用错 tone 不会，这正是下一步守卫要补的洞。"
```

---

### Task 10: 巡检告警改用 `Note`

**Files:**
- Modify: `web/src/components/Dashboard.tsx:132-145`
- Modify: `web/src/components/HostPage.tsx:473-484`
- Modify: `web/src/styles.css:10138-10156`（`.alerts .alert` → `.alerts .note`；删 `.alert__body`）

**Interfaces:**
- Consumes: Task 6 的 `<Note>`、`NoteTone`
- Produces: 无（两个文件各自把自己的 level 映射成 tone，不跨文件共享——见 Step 3 的理由）

- [ ] **Step 1: 在 `Dashboard.tsx` 加 level → tone 的映射**

`alerts.ts:15` 的 `AlertLevel` 是 `'error' | 'warn' | 'info'`。`info` 映射到 `neutral`——
这和现状一致：`.alert--info` 从来没被定义过，info 一直渲染成不带修饰符的基类，
而 `styles.css:10158` 那段注释说明了那是有意的（「Info gets no modifier at all」）。

`Dashboard.tsx:3` 现在只 import 了 `PanelAlert`，要把 `AlertLevel` 一起带上：

```tsx
import type { AlertLevel, PanelAlert } from '../alerts'
```

再加上组件 import 与映射函数（不导出，见 Step 3）：

```tsx
import type { NoteTone } from './Note'
import { Note } from './Note'

/** Info gets no colour: it is a thing worth knowing, not a thing that is
 *  wrong. Same three answers the 概览 badge already gives this list. */
function noteTone(level: AlertLevel): NoteTone {
  return level === 'info' ? 'neutral' : level
}
```

- [ ] **Step 2: 替换 `Dashboard.tsx:134-141`**

```tsx
<Note key={alert.id} tone={noteTone(alert.level)}>
  <div className="note__body">
    <strong>{alert.title}</strong>
    {alert.detail && <span>{alert.detail}</span>}
  </div>
  <button className="link" onClick={() => onNavigate(alert.action.route)}>
    {alert.action.label}
  </button>
</Note>
```

- [ ] **Step 3: 替换 `HostPage.tsx:473`**

`HostPage.tsx:464` 的 `level` 不是 `AlertLevel`——它是本地算出来的
`'error' | 'warn' | 'ok'`，而且外面裹着 `{level !== 'ok' && (…)}`，
所以在这个位置 TypeScript 已经把它收窄成 `'error' | 'warn'`，两个值都是合法的
`NoteTone`。**直接传，不要用 Dashboard 的 `noteTone`**——那个函数的入参是
`AlertLevel`（含 `info`、不含 `ok`），是另一个类型，为了共用而跨文件 import
只会把两个本来无关的枚举绑在一起。

```tsx
<Note tone={level}>
  <div className="note__body">
```

（内含的 `<strong>` / `<span>` 不动，收尾那个 `</div>` 改成 `</Note>`。）
`HostPage.tsx` 顶部加 `import { Note } from './Note'`。

- [ ] **Step 4: 改 `styles.css`**

- `.alerts .alert` 选择器改成 `.alerts .note`（约 10138 行）
- 删掉 `.alert__body` 和 `.alert__body span` 两条规则（约 10146-10156 行）——
  它们已在 Task 6 里以 `.note__body` 重新定义

`.alerts` 这个容器类名保留：它是按数据来源 `alerts.ts` 命名的列表容器，不是消息类。

- [ ] **Step 5: 确认无残留**

Run: `grep -rn "alert__body\|alert--info" web/src`
Expected: 无输出

- [ ] **Step 6: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 7: 人工确认**

把某台机器的磁盘占到 80% 以上（或临时改 `alerts.ts:11` 的 `DISK_CRITICAL_FREE` 触发），
确认概览页的告警列表和主机页的磁盘告警：红 / 黄 / 中性三种都与改名前一致。

- [ ] **Step 8: 提交**

```bash
git add -u && git commit -m "巡检告警也改用 Note，.alert 至此零例外

磁盘只剩 12% 是一个持续为真的条件，不是一条刚发生的消息——按拆分的
判据它本来就属于 Note。

Spec 原本把这两处列在「不动」里，落地时发现不行：它们用的是
alert--${level} 这种插值类名，下一步的守卫要么放它们过去（等于给自己
开后门），要么误报。"
```

---

### Task 11: `.alert--error` 合并回 `.alert`

**Files:**
- Modify: `web/src/styles.css`（合并 `.alert` 与 `.alert--error`）
- Modify: 约 59 个仍在用 `alert alert--error` 的位置（全部是 P 类页面/段级错误）

**Interfaces:**
- Consumes: 无
- Produces: 无

- [ ] **Step 1: 确认此刻只剩 P 类**

Run: `grep -rno "alert--[a-z]*" web/src --include=*.tsx | sed 's/.*://' | sort | uniq -c`
Expected: 只有 `alert--error` 一种。若还有别的，说明 Task 7-10 有遗漏，回去补。

- [ ] **Step 2: 把 `.alert` 与 `.alert--error` 合并成一条规则**

`styles.css` 的 `.alert` 规则改成带上原 `.alert--error` 的三行，并删除 `.alert--error`、
`.alert--ok`、`.alert--warn` 三条规则。合并后：

```css
/* The one slot under a page head where that page's own failure goes.
   Everything else that used to share this class is a Note now (a condition) or
   a toast (an outcome). It has exactly one meaning and therefore no modifiers:
   red, because the only thing it is ever allowed to say is that this page
   could not load what it came for. */
.alert {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 6px;
  padding: 10px 13px;
  background: var(--danger-soft);
  border: 1px solid color-mix(in srgb, var(--danger) 34%, transparent);
  border-left: 3px solid var(--danger);
  border-radius: var(--radius-sm);
  color: var(--danger-ink);
  font-size: 13px;
  animation: rise 200ms var(--ease) both;
}

.alert .link {
  color: inherit;
  font-size: 13px;
}
```

- [ ] **Step 3: 全量替换 tsx 里的类名**

```bash
grep -rl 'className="alert alert--error"' web/src --include=*.tsx \
  | xargs sed -i 's/className="alert alert--error"/className="alert"/g'
```

- [ ] **Step 4: 确认无残留**

Run: `grep -rn "alert--" web/src`
Expected: 无输出

- [ ] **Step 5: 验证构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 6: 人工确认**

随便断开后端（`Ctrl-C` 掉 `go run`），刷新几个页面，确认页头下那条红色错误条
与改名前一致。

- [ ] **Step 7: 提交**

```bash
git add -u && git commit -m ".alert 收缩成一个类一个含义：页面级错误

--ok 和 --warn 在前几步里归零，--dismiss 已删，剩下的 --error 也就没有
存在的必要了——一个只有一种形态的类不需要修饰符。

这正好等于 frontend-design skill 页面语法里写的那一行：错误永远紧贴
页头，上面不放别的。"
```

---

### Task 12: 守卫 `ruleMessageSurfaces`

**Files:**
- Modify: `web/scripts/check-ui.mjs`（新增规则函数 + 注册进 `RULES`）

**Interfaces:**
- Consumes: 该文件已有的 `tsxFiles`、`withoutComments`、`problems`、`SRC`、`path`、`fs`
- Produces: `ruleMessageSurfaces()`

- [ ] **Step 1: 在 `rulePrimaryButtons` 之前插入规则**

```js
/** Rule: the three message surfaces keep their names apart.
 *
 *  `.alert` is one thing — the slot under a page head where that page's own
 *  load failure goes — and a modifier on it means somebody is using it for
 *  something else. That is not hypothetical: 「超过本机内存八成」 (a condition
 *  that moves with a slider) and 「已保存」 (a moment) were both `.alert--*`
 *  divs, and two `warning` fields were painted `alert--error` for months
 *  because a wrong tone, unlike a wrong class name, had nothing to fail on.
 *
 *  `.note` goes through its component for the same reason badges and sections
 *  do: a tone is a decision, and decisions belong somewhere a reviewer can see
 *  them all at once.
 *
 *  What is deliberately NOT checked here is "an .alert must sit directly under
 *  the page head". That is a claim about position in JSX, and the honest
 *  renderings of it include ternaries (ConfigHistory.tsx, NetworkPage.tsx,
 *  VelocityConfig.tsx all write `cond ? <div className="alert"/> : <Skeleton/>`).
 *  A rule that false-positives teaches people to route around the guard, which
 *  costs more than the rule was worth. It lives in docs/design-system.md. */
function ruleMessageSurfaces() {
  for (const file of tsxFiles(SRC)) {
    const rel = path.relative(SRC, file)
    const src = withoutComments(fs.readFileSync(file, 'utf8'))
    for (const m of src.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\})/g)) {
      // Interpolations out first, so `alert--${level}` still reads as a
      // modifier rather than sailing past as an unparseable token.
      const tokens = (m[1] ?? m[2] ?? '').replace(/\$\{[^}]*\}/g, ' ').split(/\s+/)
      const line = src.slice(0, m.index).split('\n').length
      if (tokens.some((t) => t.startsWith('alert--'))) {
        problems.push(
          `${rel}:${line} .alert 带了修饰符 —— 它只有一个含义（页面级错误）；` +
            `条件说明改用 <Note tone="…">，操作结果改用 toast`,
        )
      }
      if (rel === 'components/Note.tsx') continue
      if (tokens.some((t) => t === 'note' || t.startsWith('note--'))) {
        problems.push(`${rel}:${line} 手写了 .note —— 改用 <Note tone="…">`)
      }
    }
  }
}
```

- [ ] **Step 2: 注册进 `RULES`**

在文件末尾的 `RULES` 数组里，`rulePrimaryButtons` 之前加上 `ruleMessageSurfaces,`。

- [ ] **Step 3: 先确认它在干净的代码上通过**

Run: `npm --prefix web run check:ui`
Expected: `check-ui: 通过`

- [ ] **Step 4: 制造一次违规，确认它能抓到**

临时在 `web/src/components/DevicesPage.tsx` 的 `return (` 之后插入两行：

```tsx
<div className="alert alert--warn">试探守卫</div>
<div className="note note--ok">试探守卫</div>
```

Run: `npm --prefix web run check:ui`
Expected: FAIL，输出两条问题，分别指向这两行，并提到 `<Note tone="…">`。

- [ ] **Step 5: 删掉试探代码，确认恢复通过**

Run: `npm --prefix web run check:ui`
Expected: `check-ui: 通过`

- [ ] **Step 6: 跑完整构建**

Run: `npm --prefix web run build`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add web/scripts/check-ui.mjs
git commit -m "守卫：.alert 不许带修饰符，.note 必须走组件

这次拆分能被推翻的唯一方式，是有人图省事又往 .alert 上挂一个修饰符。
上一次同样的约定只写在 toast.ts 的注释里，被破坏了八次。

「.alert 只能紧贴页头」这条没有进守卫：它要判断 JSX 里的位置关系，而
三元分支的写法在三个文件里都有，一条会误报的守卫会教人绕过它。那条
留在文档里。"
```

---

### Task 13: 文档与 CHANGELOG

**Files:**
- Modify: `docs/design-system.md`（第 3 节控件清单新增 `Note` 与 `toast` 条目；第 9 节补守卫）
- Modify: `.claude/skills/frontend-design/SKILL.md:25`
- Modify: `CHANGELOG.md`（「未发布」小节）

**Interfaces:**
- Consumes: 无
- Produces: 无

- [ ] **Step 1: `docs/design-system.md` 第 3 节控件清单，在 `Card` 之前插入**

```markdown
### `Note` / `.alert` / `toast`——三个面的分工

「给人看一句话」有三个载体，判据是**能用 `if (条件)` 渲染的不是消息，
只能在事件处理器里调用的才是消息**。

| 面 | 问题 | 载体 | 生命周期 |
| --- | --- | --- | --- |
| 消息 | 刚才发生了什么 | `toast()` / `toastWarn()` / `toastError()` | 弹出后自动淡出；error 常驻到点「知道了」 |
| 状态 | 现在是什么情况 | `<Note tone>` | 条件为真时存在 |
| 页面级错误 | 这一页没能加载 | `.alert` | 紧贴页头，上面不放别的 |
| 任务 | 正在做什么 | 下载队列 + 页内进度 | 跟着 job 状态机 |

`.alert` **没有修饰符**，只有一个含义。`.note` 必须通过 `<Note>`。两条都由
`check-ui.mjs` 的 `ruleMessageSurfaces` 执行。

`toast` 的第二参数：`{ key }` 让同 key 的新消息替换旧的（连点保存只留一条），
`{ sticky }` 让它不自动消失。`toastError` 默认 `sticky`。
```

- [ ] **Step 2: `docs/design-system.md` 第 9 节「已经能被 CI 执行的规则」加一行**

```markdown
- `ruleMessageSurfaces`：`.alert` 不得带修饰符；`.note` 必须走 `<Note>`。
```

并从第 9 节末尾的「还没被守卫覆盖的」里加一条：

```markdown
- `.alert` 必须紧贴页头。位置关系的静态判定会误报（三元分支的写法在
  `ConfigHistory.tsx`、`NetworkPage.tsx`、`VelocityConfig.tsx` 都有），只作约定。
```

- [ ] **Step 3: `.claude/skills/frontend-design/SKILL.md:25` 改页面语法那一行**

```
  .alert?                       ← 这一页没能加载；紧贴页头，上面不放别的，且没有修饰符
```

并在「布局硬规则」的页面语法小节末尾补一句：

```markdown
`.alert` 只装页面级加载失败。条件说明（跟着表单实时变的校验、「这个文件还不存在」
这类）用 `<Note tone>`；一次性的操作结果用 `toast()`。判据：能用 `if (条件)` 渲染的
不是消息。
```

- [ ] **Step 4: `CHANGELOG.md` 的「未发布」小节加三条**

```markdown
- 失败提示不再顶着绿色对勾弹出，也不再自动消失——需要点「知道了」确认。
- 开关机失败切换页面后仍然可见，不再随页面卸载丢失。
- Java 页与数据库页的平台说明改回黄色警示，此前被误画成红色出错。
```

- [ ] **Step 5: 验证构建**

Run: `npm --prefix web run build && make lint && make test`
Expected: 全部 PASS。（后端未改，但 `make test` 会覆盖 `internal/webui` 的 embed。）

- [ ] **Step 6: 提交**

```bash
git add docs/design-system.md .claude/skills/frontend-design/SKILL.md CHANGELOG.md
git commit -m "文档：三个面的分工与判据写进设计规范

上一次这条约定只活在 toast.ts 的注释里，于是被破坏了八次。这次它同时
在三个地方：守卫执行能执行的部分，设计规范写判据，frontend-design skill
写页面语法里的位置。"
```

---

## 全部完成后

跑一遍完整自查（`frontend-design` skill 的验收清单）：

```bash
npm --prefix web run build
make lint && make test
```

人工过一遍：

- [ ] 明暗两种模式 × toast 三种 tone
- [ ] 常驻 toast 不自动走，点「知道了」才消失
- [ ] ok 与常驻混合堆叠时，ok 过期后常驻那条不跳动
- [ ] 连点保存三次只出现一条「已保存」
- [ ] 1440 / 1200 / 1024 / 768 / 390 五个宽度无横向溢出
- [ ] 折叠侧栏、打开抽屉、开着控制台的实例页三处未被波及
- [ ] 概览页巡检告警红/黄/中性三种正常
- [ ] Java 页与数据库页的平台说明是黄色

然后按 `CLAUDE.md` 的工作流程合并：推功能分支 → 切 `main` → `git pull origin main` →
合并 → 推 `main` → 切回功能分支。
