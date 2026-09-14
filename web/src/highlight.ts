import Prism from 'prismjs/components/prism-core'
import 'prismjs/components/prism-markup'
import 'prismjs/components/prism-yaml'
import 'prismjs/components/prism-json'
import 'prismjs/components/prism-toml'
import 'prismjs/components/prism-properties'
import 'prismjs/components/prism-ini'
import 'prismjs/components/prism-bash'
import 'prismjs/components/prism-markdown'

/**
 * Syntax colouring for the file editor.
 *
 * Prism's core only, with the grammars listed one by one rather than pulled in
 * wholesale: all of this ends up embedded in a single Go binary, and a hundred
 * languages nobody opens is a hundred languages every operator downloads.
 * markup is not in the list for its own sake — markdown needs it.
 *
 * What this is not is a language server. A file the panel colours wrong is a
 * cosmetic bug; a file the *server* refuses is reported by the server, which is
 * the only opinion that decides whether Minecraft starts.
 */

/** Extension → the name shown in the status line, and the grammar to colour
 *  with. A null grammar means plain text: shown, not coloured. */
const LANGS: Record<string, { label: string; prism: string | null }> = {
  yml: { label: 'YAML', prism: 'yaml' },
  yaml: { label: 'YAML', prism: 'yaml' },
  json: { label: 'JSON', prism: 'json' },
  toml: { label: 'TOML', prism: 'toml' },
  properties: { label: 'Properties', prism: 'properties' },
  conf: { label: 'Conf', prism: 'ini' },
  cfg: { label: 'Conf', prism: 'ini' },
  ini: { label: 'INI', prism: 'ini' },
  md: { label: 'Markdown', prism: 'markdown' },
  sh: { label: 'Shell', prism: 'bash' },
  txt: { label: '纯文本', prism: null },
  // A server log is the one thing here that is *not* a config, and Prism has no
  // grammar that fits it. What a log is read for is the level — INFO scrolls
  // past, WARN is looked at, ERROR is why the page was opened — so it gets
  // highlightLog below instead of a grammar, and the null stays.
  log: { label: '日志', prism: null },
  kts: { label: 'Kotlin Script', prism: null },
}

export function langOf(path: string): { label: string; prism: string | null } {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  const known = LANGS[ext]
  if (known) return known
  return { label: ext ? ext.toUpperCase() : '纯文本', prism: null }
}

/**
 * Colours `code` as `lang`, as an HTML string.
 *
 * Safe to hand to dangerouslySetInnerHTML: Prism escapes every character of the
 * input it does not itself wrap, so the only tags in the output are the
 * <span class="token …"> it generated. A config file full of <script> comes
 * back as text. Do not "simplify" this by interpolating the raw code.
 */
export function highlight(code: string, lang: string): string {
  const grammar = Prism.languages[lang]
  if (!grammar) return escapeHTML(code)
  // A trailing newline is eaten by <pre>, and an overlay one line shorter than
  // the textarea above it drifts by a line at the bottom of every file that
  // ends the way nearly every file ends.
  return Prism.highlight(code, grammar, lang) + '\n'
}

function escapeHTML(text: string): string {
  return text.replace(/[&<>]/g, (ch) => (ch === '&' ? '&amp;' : ch === '<' ? '&lt;' : '&gt;'))
}

/**
 * Colours a server log by level, and by nothing else.
 *
 * A log is read for one thing: which of these lines is the one that broke the
 * server. Tokenising the rest of the line — timestamps, thread names, the
 * plugin's own prose — would be a second colour scheme competing with the
 * answer, on the one screen in the panel where the answer is already hard to
 * find. So the level word gets a class and everything else stays ink.
 *
 * Safe for dangerouslySetInnerHTML: the input is escaped first, and the only
 * tags added afterwards are the spans below.
 */
export function highlightLog(code: string): string {
  const painted = escapeHTML(code).replace(
    /\b(INFO|WARN|WARNING|ERROR|SEVERE|FATAL|DEBUG|TRACE)\b/g,
    (word) => `<span class="token log-${LOG_TONE[word]}">${word}</span>`,
  )
  // The same trailing newline highlight() adds, for the same reason: <pre>
  // eats it, and an overlay one line shorter than the textarea above it drifts
  // by a line at the bottom of every file that ends the way files end.
  return painted + '\n'
}

/** Three tones, not eight. Anything below a warning is either the normal case
 *  or noise, and both of those are things to skip past. */
const LOG_TONE: Record<string, string> = {
  INFO: 'info',
  DEBUG: 'muted',
  TRACE: 'muted',
  WARN: 'warn',
  WARNING: 'warn',
  ERROR: 'error',
  SEVERE: 'error',
  FATAL: 'error',
}
