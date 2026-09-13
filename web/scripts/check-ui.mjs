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
  const css = fs.readFileSync(CSS, 'utf8')
  const out = new Set()
  for (const m of css.matchAll(/\.(-?[_a-zA-Z][\w-]*)/g)) out.add(m[1])
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

/** Rule: a .panel--form declares both of its columns.
 *
 *  The panel is a two-column flex box — an aside carrying the section's title
 *  and one sentence of why, and a body carrying the fields. A section that
 *  forgets the wrappers does not break: it degrades into one flat column of
 *  full-width controls, which looks close enough to right that it survives
 *  review. That is exactly the failure this layout set out to remove, so it is
 *  checked rather than remembered.
 *
 *  Counting occurrences per file rather than parsing JSX nesting: the files
 *  that use .panel--form write one aside and one body per section, so the
 *  counts match when every section is wrapped and diverge the moment one is
 *  missed. A nesting parser would catch more and cost far more. */
function ruleFormPanelsHaveColumns() {
  for (const file of tsxFiles(SRC)) {
    const src = fs.readFileSync(file, 'utf8')
    const panels = (src.match(/panel--form/g) ?? []).length
    if (panels === 0) continue
    const asides = (src.match(/panel__aside/g) ?? []).length
    const bodies = (src.match(/panel__body/g) ?? []).length
    if (asides === panels && bodies === panels) continue
    problems.push(
      `${path.relative(SRC, file)} 有 ${panels} 个 .panel--form，` +
        `但 ${asides} 个 .panel__aside、${bodies} 个 .panel__body —— 每个都要两栏包裹`,
    )
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

/** Advisory: one filled button per screen.
 *
 *  Not an error yet — a dozen components exceed it, and each needs a
 *  judgement about which of its buttons is the primary one. Printed so the
 *  number goes down over time rather than up. */
function adviseOnePrimaryPerFile() {
  const over = []
  for (const file of tsxFiles(SRC)) {
    const n = (fs.readFileSync(file, 'utf8').match(/variant="primary"/g) ?? []).length
    if (n > 1) over.push(`${path.relative(SRC, file)} (${n})`)
  }
  if (over.length > 0) {
    console.warn(`check-ui 提示: ${over.length} 个组件有多于一个实心按钮 — ${over.join('、')}`)
  }
}

const RULES = [
  ruleNoUndefinedClasses,
  ruleIconButtonsAreLabelled,
  ruleNoSilentOverrides,
  ruleFormPanelsHaveColumns,
  ruleDropdownsAreOurs,
]

for (const rule of RULES) rule()
adviseOnePrimaryPerFile()

if (problems.length > 0) {
  console.error(`check-ui: ${problems.length} 处问题\n`)
  for (const p of problems) console.error('  ' + p)
  process.exit(1)
}
console.log('check-ui: 通过')
