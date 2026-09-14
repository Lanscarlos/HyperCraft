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
  return out
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
