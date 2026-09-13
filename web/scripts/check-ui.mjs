// The design system, as something CI can fail on.
//
// A className in this codebase is a bare string, so a typo is invisible: the
// element just renders unstyled and nobody notices for months. `btn--small`
// was used fifteen times and defined zero times. Types cannot catch that, and
// there is no CSS build step that would — so this script is the check.
//
// Only BEM-shaped tokens (containing `__` or `--`) are verified. Single-word
// lowercase tokens are skipped: template-literal interpolation makes object
// keys and state names look like classes, and every one of those is a false
// positive.
import fs from 'node:fs'
import path from 'node:path'

const SRC = new URL('../src/', import.meta.url).pathname
const CSS = path.join(SRC, 'styles.css')

/** Every class name that appears anywhere in a selector. */
function definedClasses() {
  // styles.migrate-*.css are the per-group scratch sheets of the layout
  // migration; they are folded into styles.css when it lands. Temporary.
  const sheets = [CSS, ...fs.readdirSync(SRC).filter((f) => /^styles\.migrate-.*\.css$/.test(f)).map((f) => path.join(SRC, f))]
  const out = new Set()
  for (const sheet of sheets) {
    const css = fs.readFileSync(sheet, 'utf8')
    for (const m of css.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) out.add(m[1])
  }
  return out
}

function tsxFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) return tsxFiles(p)
    return p.endsWith('.tsx') ? [p] : []
  })
}

/** class name -> files that use it, for BEM-shaped tokens only. */
function usedClasses() {
  const out = new Map()
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    for (const m of src.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\}|\{"([^"]*)"\})/g)) {
      for (const raw of (m[1] ?? m[2] ?? m[3] ?? '').split(/[\s`]+/)) {
        const token = raw.trim()
        if (!/^[a-z][\w-]*$/.test(token)) continue
        if (!token.includes('__') && !token.includes('--')) continue
        if (!out.has(token)) out.set(token, new Set())
        out.get(token).add(path.relative(SRC, file))
      }
    }
  }
  return out
}

const problems = []

/** Rule: every BEM class used in a component exists in styles.css.
 *
 *  A base whose modifiers carry all the styling counts as defined: the diff
 *  rows are `.chist__line` + `--add`/`--delete`, and an unchanged row having
 *  no background of its own is the correct rendering, not a missing rule.
 *  A base with no modifiers anywhere is a different thing — that is the
 *  `btn--small` case, and it stays an error. */
function ruleNoUndefinedClasses() {
  const defined = definedClasses()
  const basesWithModifiers = new Set()
  for (const cls of defined) {
    const cut = cls.indexOf('--')
    if (cut > 0) basesWithModifiers.add(cls.slice(0, cut))
  }
  for (const [cls, files] of [...usedClasses()].sort()) {
    if (defined.has(cls) || basesWithModifiers.has(cls)) continue
    problems.push(`未定义的类名 .${cls}  ←  ${[...files].join(', ')}`)
  }
}

/** The `>` that closes the JSX tag opening at `start`, or -1.
 *
 *  Scanning for the next `>` does not work: `onClick={() => …}` contains one,
 *  and half the icon buttons in this codebase are written that way. So skip
 *  over strings and brace expressions and only accept a `>` at depth zero.
 *
 *  Comments have to be skipped before quotes, not after. This codebase writes
 *  prose comments between a tag's attributes, and one of them says "snapshot's
 *  copy" — treat that apostrophe as a string opener and the scan swallows the
 *  rest of the file. */
function endOfTag(src, start) {
  let depth = 0
  for (let i = start; i < src.length; i++) {
    const c = src[i]
    if (c === '/' && src[i + 1] === '/') {
      i = src.indexOf('\n', i)
      if (i === -1) return -1
      continue
    }
    if (c === '/' && src[i + 1] === '*') {
      const end = src.indexOf('*/', i + 2)
      if (end === -1) return -1
      i = end + 1
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      const quote = c
      i++
      while (i < src.length && src[i] !== quote) {
        if (src[i] === '\\') i++
        i++
      }
      continue
    }
    if (c === '{') depth++
    else if (c === '}') depth--
    else if (c === '>' && depth === 0) return i
  }
  return -1
}

/** Rule: an icon-only button carries no text, so it needs a label.
 *
 *  Without one a screen reader reads out "button" and nothing else, which is
 *  the same as reading out nothing. */
function ruleIconButtonsAreLabelled() {
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    for (let at = src.indexOf('<Button'); at !== -1; at = src.indexOf('<Button', at + 7)) {
      const end = endOfTag(src, at)
      if (end === -1) break
      const tag = src.slice(at, end + 1)
      if (!/(^|\s)icon(\s|=|\/|>)/.test(tag)) continue
      if (tag.includes('aria-label')) continue
      problems.push(`图标按钮缺 aria-label  ←  ${path.relative(SRC, file)}`)
    }
  }
}

/** Overrides that are written on purpose. Each needs a reason. */
const OVERRIDE_ALLOWED = new Map([
  [
    '.pdot--foreign',
    '有意覆盖：库外来源画成空心环，而不是第七种颜色。见 styles.css 里该规则上方的注释。',
  ],
])

/** Rule: no selector silently overrides its own earlier declaration.
 *
 *  Writing a selector twice is not itself wrong here. This sheet is organised
 *  by narrative, not by selector: `.sidebar__group` is defined where the
 *  sidebar is built and again, additively, in the passage about folding it,
 *  each with the comment that explains that behaviour. Splitting those apart
 *  would move the comments away from what they explain.
 *
 *  What is always a bug is the same *property* declared twice, because then
 *  one of the two blocks is quietly not doing what it says. `.badge--warn`
 *  was written twice; the later block dropped the earlier one's border-color,
 *  so warning badges lost their tinted edge and nobody noticed. `.preview`
 *  was two different components that happened to pick the same name, each
 *  leaking properties into the other. */
function ruleNoSilentOverrides() {
  const lines = fs.readFileSync(CSS, 'utf8').split('\n')
  const blocks = new Map()
  for (let i = 0; i < lines.length; i++) {
    const m = /^(\.[a-zA-Z0-9_-]+(?:__[a-zA-Z0-9_-]+)?(?:--[a-zA-Z0-9_-]+)?) \{$/.exec(lines[i])
    if (!m) continue
    // The last line of a selector group is not a second definition: a shared
    // rule for `.a, .b` followed by a specific rule for `.b` is ordinary CSS,
    // and thirteen of this sheet's twenty-one apparent duplicates were that.
    if ((lines[i - 1] ?? '').trimEnd().endsWith(',')) continue
    const props = new Set()
    for (let j = i + 1; j < lines.length && lines[j] !== '}'; j++) {
      const p = /^\s{2}([a-z-]+):/.exec(lines[j])
      if (p) props.add(p[1])
    }
    if (!blocks.has(m[1])) blocks.set(m[1], [])
    blocks.get(m[1]).push({ line: i + 1, props })
  }
  for (const [selector, list] of blocks) {
    if (list.length < 2 || OVERRIDE_ALLOWED.has(selector)) continue
    const clashes = new Set()
    for (let a = 0; a < list.length; a++) {
      for (let b = a + 1; b < list.length; b++) {
        for (const p of list[b].props) if (list[a].props.has(p)) clashes.add(p)
      }
    }
    if (clashes.size > 0) {
      const where = list.map((x) => x.line).join(' 与 ')
      problems.push(`${selector} 在第 ${where} 行重复声明了 ${[...clashes].join('、')}`)
    }
  }
}

/** Rule: a section's head and body are written by Section.tsx, nowhere else.
 *
 *  `.panel--form` carries the guarantee that a form's fields stop at the
 *  reading measure; `.panel__head` / `.panel__body` are the two wrappers that
 *  guarantee depends on. A section written by hand that forgets one does not
 *  break — the fields lose their measure and run to the card's edge, which
 *  looks close enough to right that it survives review. That is the failure
 *  the shared shape exists to remove, so the classes are checked rather than
 *  remembered: they live in one file, and a page asks for a section by calling
 *  the component. */
const SECTION_ONLY = ['panel--form', 'panel__head', 'panel__heading', 'panel__body', 'panel__tools']

function ruleSectionsAreComponents() {
  for (const file of tsxFiles(SRC)) {
    const rel = path.relative(SRC, file)
    if (rel === 'components/Section.tsx') continue
    const src = withoutComments(fs.readFileSync(file, 'utf8'))
    for (const cls of SECTION_ONLY) {
      if (new RegExp(`[\s"'\`]${cls}[\s"'\`]`).test(src)) {
        problems.push(`${rel} 手写了 .${cls} —— 改用 <Section>`)
      }
    }
  }
}

/** Rule: an empty state is the component, not a paragraph.
 *
 *  Six shapes used to say "nothing here" — a dashed box, a <p> inside a table,
 *  a .muted paragraph, one with a glyph, and two per-page ones — and which you
 *  got depended on the page. EmptyState.tsx is where the two that remain are
 *  written (a block, or a line inside a list). */
const EMPTY_ONLY = ['empty__title', 'empty__note', 'empty__actions', 'empty--inline']

function ruleEmptyStatesAreComponents() {
  for (const file of tsxFiles(SRC)) {
    const rel = path.relative(SRC, file)
    if (rel === 'components/EmptyState.tsx') continue
    const src = withoutComments(fs.readFileSync(file, 'utf8'))
    for (const cls of EMPTY_ONLY) {
      if (new RegExp(`[\s"'\`]${cls}[\s"'\`]`).test(src)) {
        problems.push(`${rel} 手写了 .${cls} —— 改用 <EmptyState>`)
      }
    }
  }
}

/** Rule: dropdowns are the panel's, not the platform's.
 *
 *  A native `<select>` is two controls in one: a box the page draws and a
 *  popup the *platform* draws. `appearance: none` and the rules in styles.css
 *  win the box; nothing wins the popup. So choosing a Java runtime used to end
 *  in a Windows 95 list dropping out of a control styled to the millimetre,
 *  and Select.tsx exists to answer exactly that.
 *
 *  `<datalist>` is the same bug wearing the other hat, and it outlived the fix
 *  by a year in the three 服务端 jar fields — one field below a Select, in the
 *  same form, in the same screenshot. Neither can read a token, neither fades
 *  the way every other surface in the panel fades, and neither has anywhere to
 *  put the second line that is most of why a list is worth offering.
 *
 *  Select.tsx is exempt: it owns both branches, including the real `<select>`
 *  it falls back to on a touch screen, where the platform's picker is the
 *  better one. */
const DROPDOWN_EXEMPT = new Set(['components/Select.tsx'])

/** Source with its comments blanked out.
 *
 *  Needed because this codebase's comments are prose about the code, so they
 *  talk about `<select>` and `<datalist>` constantly — InstancePlugins and
 *  JVMArgsEditor each explain at length why they do *not* use one, and both
 *  read as violations until the comments are gone. Line comments are only
 *  taken when the `//` does not follow a `:`, which is what a URL looks like.
 */
function withoutComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/.*$/gm, '$1')
}

function ruleDropdownsAreOurs() {
  for (const file of tsxFiles(SRC)) {
    const rel = path.relative(SRC, file)
    if (DROPDOWN_EXEMPT.has(rel)) continue
    const src = withoutComments(fs.readFileSync(file, 'utf8'))
    for (const [tag, hint] of [
      ['<select', '改用 <Select>'],
      ['<datalist', '改用 <Select allowCustom>'],
    ]) {
      const n = (src.match(new RegExp(`${tag}[\\s>]`, 'g')) ?? []).length
      if (n > 0) problems.push(`${rel} 用了 ${n} 处原生 ${tag}> —— ${hint}`)
    }
  }
}

/** Rule: a badge is the component, not a hand-written class.
 *
 *  `.badge` and its nine `--tone` modifiers were spread across 23 files as bare
 *  strings. That is how `.badge--warn` came to be declared twice with the second
 *  block dropping its border-color — the warning badge lost its tinted edge and
 *  nobody noticed, because there was no one place the tone vocabulary lived.
 *  Now there is, and the point of Badge is that the vocabulary is a union type
 *  the compiler checks rather than a string anyone can misspell.
 *
 *  A <span> is what Badge renders, so a <span> wearing these classes is always
 *  a Badge that has not been written as one. Any other element is not: 插件列表
 *  的升级键 is a <button> painted as a badge, and Badge cannot be a button —
 *  the same exception `<a className="btn">` has from Button, for the same
 *  reason. Badge.tsx itself is where the strings are supposed to be. */
function ruleBadgesAreComponents() {
  for (const file of tsxFiles(SRC)) {
    const rel = path.relative(SRC, file)
    if (rel === 'components/Badge.tsx') continue
    const src = withoutComments(fs.readFileSync(file, 'utf8'))
    for (const m of src.matchAll(/className=(?:"([^"]*)"|\{`([^`]*)`\})/g)) {
      // Interpolations out first, so `badge${TONE[x]}` still reads as the token
      // `badge`. Without this the one in Sidebar walked straight past.
      const tokens = (m[1] ?? m[2] ?? '').replace(/\$\{[^}]*\}/g, ' ').split(/\s+/)
      if (!tokens.some((t) => t === 'badge' || t.startsWith('badge--'))) continue
      const open = src.lastIndexOf('<', m.index)
      if (!/^<span[\s>]/.test(src.slice(open, open + 6))) continue
      const line = src.slice(0, open).split('\n').length
      problems.push(`${rel}:${line} 手写了 .badge —— 改用 <Badge tone="…">`)
    }
  }
}

/** How many filled buttons a file may declare, and why more than one of them
 *  is still not more than one *screen*.
 *
 *  The count is per file because a script cannot see a screen. Most of the time
 *  that is the same thing; these are the places where it is not — a dialog is
 *  its own screen, and two branches of a ternary are never both on one. Every
 *  entry is a promise that someone looked.
 *
 *  A file not listed here gets one. Going over fails; coming under only prints,
 *  because a build that breaks when you remove a filled button is a build that
 *  argues for keeping it. */
const PRIMARY_ALLOWED = new Map([
  ['components/ConfigHistory.tsx', [2, '页面上的「打快照」，和二次确认对话框里的那一下']],
  ['components/FileManager.tsx', [3, '编辑器的保存，加上重命名与图片预览两个对话框']],
  ['components/NewInstanceWizard.tsx', [3, '页脚的「下一步」与「创建实例」互斥，加上完成页的「进入控制台」']],
  ['components/PluginImportDialog.tsx', [2, '同一个对话框的两个状态：导入前与导入后']],
  ['components/PluginLibraryPage.tsx', [3, '批量条，加上批量安装与批量升级两个确认对话框']],
  ['components/SchematicLibraryPage.tsx', [3, '页面的「上传建筑」，加上编辑与安装两个对话框']],
  ['components/ScriptImportDialog.tsx', [2, '同一个对话框的两个状态：读脚本前与读出来之后']],
  ['components/TerminalSettings.tsx', [3, '终端已开、确认中、未开三种互斥状态各一个']],
  ['components/UpdatePanel.tsx', [2, '面板上的「立即更新」，和二次确认对话框里的那一下']],
])

/** Rule: one filled button per screen.
 *
 *  A filled button is a claim that this is the thing to do here. Two of them on
 *  one screen is two claims, and the reader checks both — which is the cost the
 *  quiet palette was bought to avoid. The rule bites hardest on lists: a row
 *  action that is filled is filled once per row, and a page where every row is
 *  filled has no filled button at all.
 *
 *  Where a screen has two candidates, the one that stays is what the screen is
 *  asking for right now — a form's submit, a wizard footer's next, an empty
 *  state's call to action, the pending item in a banner. The standing entrance
 *  in a page or card head is not it, and neither is an escape hatch beside the
 *  main path. */
function rulePrimaryButtons() {
  for (const file of tsxFiles(SRC)) {
    const rel = path.relative(SRC, file)
    const n = (withoutComments(fs.readFileSync(file, 'utf8')).match(/variant="primary"/g) ?? [])
      .length
    const [allowed, why] = PRIMARY_ALLOWED.get(rel) ?? [1, '']
    if (n > allowed) {
      problems.push(
        `${rel} 有 ${n} 个实心按钮，最多 ${allowed} 个` +
          (why ? `（${why}）` : '') +
          ' —— 一屏只留那个「此刻要你做的事」，其余降成描边',
      )
    } else if (allowed > 1 && n < allowed) {
      console.warn(
        `check-ui 提示: ${rel} 只剩 ${n} 个实心按钮了，` +
          `PRIMARY_ALLOWED 里那条可以改成 ${n} 或删掉`,
      )
    }
  }
}

const RULES = [
  ruleNoUndefinedClasses,
  ruleIconButtonsAreLabelled,
  ruleNoSilentOverrides,
  ruleSectionsAreComponents,
  ruleEmptyStatesAreComponents,
  ruleDropdownsAreOurs,
  ruleBadgesAreComponents,
  rulePrimaryButtons,
]

for (const rule of RULES) rule()

if (problems.length > 0) {
  console.error(`check-ui: ${problems.length} 处问题\n`)
  for (const p of problems) console.error('  ' + p)
  process.exit(1)
}
console.log('check-ui: 通过')
