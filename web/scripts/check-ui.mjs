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

const RULES = [ruleNoUndefinedClasses, ruleIconButtonsAreLabelled, ruleNoSilentOverrides]

for (const rule of RULES) rule()

if (problems.length > 0) {
  console.error(`check-ui: ${problems.length} 处问题\n`)
  for (const p of problems) console.error('  ' + p)
  process.exit(1)
}
console.log('check-ui: 通过')
