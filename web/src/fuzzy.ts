/**
 * Turning the server's match offsets into something React can paint.
 *
 * The matching itself is not here — it is the half that has to be fast over
 * tens of thousands of paths, so it happens in Go and the offsets come back
 * with the hit. See internal/serverfiles/index.go.
 */

/** One run of a string, marked by whether the query matched it. */
export interface Run {
  text: string
  hit: boolean
}

/**
 * Splits a string into alternating matched and unmatched runs.
 *
 * Runs rather than one element per character: a path is sixty characters and a
 * list is fifty rows, and three thousand spans is a list that stutters as you
 * type. Offsets outside the string are dropped rather than throwing — they can
 * only arrive from a `shift` that has trimmed the wrong end, and a highlight
 * that is quietly missing beats a palette that goes blank.
 */
export function segments(text: string, match: number[]): Run[] {
  if (match.length === 0) return [{ text, hit: false }]
  const on = new Set(match)
  const out: Run[] = []
  for (let i = 0; i < text.length; i++) {
    const hit = on.has(i)
    const last = out[out.length - 1]
    if (last !== undefined && last.hit === hit) last.text += text[i]
    else out.push({ text: text[i], hit })
  }
  return out
}

/**
 * Moves match offsets from a whole path onto one slice of it.
 *
 * The server matches — and reports offsets into — the full path, because that
 * is what the query is written against: `vulp con` spans a directory and a
 * filename. A result row shows the basename on one line and the directory in
 * grey on the next, so each half needs the offsets that fall inside it, moved
 * back to its own start. Doing this by re-running a matcher on the basename
 * would answer a different question and highlight different characters.
 */
export function shift(match: number[], offset: number, length: number): number[] {
  const out: number[] = []
  for (const at of match) {
    if (at < offset || at >= offset + length) continue
    out.push(at - offset)
  }
  return out
}
