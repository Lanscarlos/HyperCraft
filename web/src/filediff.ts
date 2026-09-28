/**
 * Line-level diff for the file editor.
 *
 * Not a general diff library: the only two questions the editor asks are
 * "which lines on screen differ from the version on disk" and "what did this
 * one say before", and both are about the *current* text's line numbers.
 *
 * The shape is prefix/suffix trim first, then a bounded LCS over what is left.
 * Trimming is what makes this cheap on the real input — editing one value in a
 * 2000-line paper-global.yml leaves a band of one line — and the bound is what
 * keeps a pathological case (a whole file re-indented) from turning a keystroke
 * into an O(n²) walk over twenty thousand lines. Past the bound the whole band
 * is reported as changed without per-line provenance, which is the honest
 * answer: the gutter still marks it, and 还原此行 is simply not offered.
 */

/** What happened to one line of the current text, relative to the disk copy. */
export type LineChange = { kind: 'mod'; was: string } | { kind: 'add' }

/** Past this many lines on either side of the band, the LCS is skipped. 400²
 *  is 160k cells, a fraction of a frame; 20 000² is a hang. */
const LCS_LIMIT = 400

/** Changed lines of `current`, keyed by 1-based line number.
 *
 *  Deletions are not in the result and cannot be: a line that is only on disk
 *  has no row on screen to mark. */
export function diffLines(original: string, current: string): Map<number, LineChange> {
  const out = new Map<number, LineChange>()
  if (original === current) return out

  const a = original.split('\n')
  const b = current.split('\n')

  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++

  let tail = 0
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  ) {
    tail++
  }

  const left = a.slice(head, a.length - tail)
  const right = b.slice(head, b.length - tail)
  if (right.length === 0) return out

  if (left.length > LCS_LIMIT || right.length > LCS_LIMIT) {
    // Too wide to align. Every line of the band is marked, and none of them
    // carries what it used to say — which is what stops 还原此行 from being
    // offered on a line whose counterpart nobody worked out.
    for (let i = 0; i < right.length; i++) out.set(head + i + 1, { kind: 'add' })
    return out
  }

  for (const op of align(left, right)) {
    if (op.right === null) continue
    if (op.left === null) {
      out.set(head + op.right + 1, { kind: 'add' })
    } else if (left[op.left] !== right[op.right]) {
      out.set(head + op.right + 1, { kind: 'mod', was: left[op.left] })
    }
  }
  settle(out, b)
  return out
}

/** How far one added block is walked up looking for a better seat. Only a
 *  file of near-identical lines gets anywhere near it. */
const SLIDE_LIMIT = 400

/**
 * Moves each run of added lines to where a person would say it went.
 *
 * An inserted block that repeats its neighbours can be drawn at several
 * places, all equally true. The head trim above always picks the lowest one,
 * and on the input this editor sees that is usually wrong: copy one entry of a
 * JSON list and paste it under itself, and the trim eats the new entry's
 * `{`, `"type"`, `"price"` as "unchanged", so the marks start at the fourth
 * line of the paste and end four lines into the next entry (issue #6).
 *
 * Every seat marks the same number of lines, so this only chooses where. The
 * choice is the seat whose first and last lines are least indented — the
 * block's edges then sit on the structure's own boundaries (`{` … `},`, a
 * top-level key) rather than halfway into one. Blocks are only walked up,
 * and a tie keeps the lower seat: that is the one the trim picked, so a
 * pasted copy still reads as the one below the original.
 *
 * Moving a block up one line is sound whenever the line above it is unchanged
 * and equals the block's last line: that last line takes over the partner the
 * line above had, and nothing else changes pairs.
 */
function settle(out: Map<number, LineChange>, b: string[]): void {
  // `b` is 0-based, the map is 1-based; `line(n)` reads the map's numbering.
  const line = (n: number) => b[n - 1]
  const runs: [number, number][] = []
  let n = 1
  while (n <= b.length) {
    if (out.get(n)?.kind !== 'add') {
      n++
      continue
    }
    const start = n
    while (out.get(n)?.kind === 'add') n++
    runs.push([start, n - 1])
  }

  for (const [start, end] of runs) {
    let best = 0
    let bestScore = indent(line(start)) + indent(line(end))
    for (let up = 1; up <= SLIDE_LIMIT; up++) {
      const above = start - up
      if (above < 1 || out.has(above) || line(above) !== line(end - up + 1)) break
      const score = indent(line(above)) + indent(line(end - up))
      if (score < bestScore) {
        best = up
        bestScore = score
      }
    }
    for (let k = 0; k < best; k++) {
      out.delete(end - k)
      out.set(start - 1 - k, { kind: 'add' })
    }
  }
}

/** Leading whitespace, tabs counted as one. A blank line counts as none: it
 *  is a boundary, not something nested. */
function indent(text: string): number {
  let i = 0
  while (i < text.length && (text[i] === ' ' || text[i] === '\t')) i++
  return i === text.length ? 0 : i
}

/** One row of a side-by-side comparison. `null` on a side means that side has
 *  no line there. */
export interface DiffRow {
  left: string | null
  right: string | null
  same: boolean
}

/** The conflict dialog's two columns: both versions, lined up. */
export function sideBySide(a: string, b: string): DiffRow[] {
  const left = a.split('\n')
  const right = b.split('\n')

  if (left.length > LCS_LIMIT * 4 || right.length > LCS_LIMIT * 4) {
    // Too big to align. Row for row is still readable when the change is
    // local, and it is never a lie about what the two files contain.
    const rows: DiffRow[] = []
    for (let i = 0; i < Math.max(left.length, right.length); i++) {
      rows.push({
        left: left[i] ?? null,
        right: right[i] ?? null,
        same: left[i] === right[i],
      })
    }
    return rows
  }

  return align(left, right).map((op) => ({
    left: op.left === null ? null : left[op.left],
    right: op.right === null ? null : right[op.right],
    same: op.left !== null && op.right !== null && left[op.left] === right[op.right],
  }))
}

interface Pairing {
  left: number | null
  right: number | null
}

/**
 * Pairs of indices into the two bands: a matched pair, an insert (left null)
 * or a delete (right null).
 *
 * A plain LCS table rather than Myers. The band is bounded by every caller, so
 * the quadratic table is a few hundred kilobytes at worst, and the simple
 * version is one anybody can check by reading it — which matters more here
 * than the constant factor, because what this drives is a mark that tells
 * somebody a line is unsaved.
 */
function align(a: string[], b: string[]): Pairing[] {
  const cols = b.length + 1
  const table = new Uint32Array((a.length + 1) * cols)
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      table[i * cols + j] =
        a[i] === b[j]
          ? table[(i + 1) * cols + j + 1] + 1
          : Math.max(table[(i + 1) * cols + j], table[i * cols + j + 1])
    }
  }

  const walked: Pairing[] = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      walked.push({ left: i, right: j })
      i++
      j++
    } else if (table[(i + 1) * cols + j] >= table[i * cols + j + 1]) {
      walked.push({ left: i, right: null })
      i++
    } else {
      walked.push({ left: null, right: j })
      j++
    }
  }
  while (i < a.length) walked.push({ left: i++, right: null })
  while (j < b.length) walked.push({ left: null, right: j++ })

  // A delete immediately followed by an insert is one line being rewritten,
  // and the editor has a better answer for that than "this line is new": it
  // can offer the old text back. Pairing them here is what turns most real
  // edits into `mod` rather than `add`.
  const merged: Pairing[] = []
  for (let k = 0; k < walked.length; k++) {
    const here = walked[k]
    const next = walked[k + 1]
    if (here.right === null && next !== undefined && next.left === null) {
      merged.push({ left: here.left, right: next.right })
      k++
      continue
    }
    merged.push(here)
  }
  return merged
}
