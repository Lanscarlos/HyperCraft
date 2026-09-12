import type { ReactNode } from 'react'

interface Props {
  /** The one line that stays visible. Defaults to the question the body answers. */
  summary?: string
  children: ReactNode
}

/**
 * The paragraph behind a one-line hint.
 *
 * Several settings on this page need a paragraph to be settable safely — why
 * terminal mode changes what Tab completion answers, why the two colour flags
 * are wrong under it, what Forge ships instead of a runnable jar. Left inline
 * they were six-line blocks sitting beside the control they described, at the
 * same weight as it, and the eye could not tell a field from a footnote.
 *
 * A <details> rather than a popover: no JS, no focus management, reachable by
 * keyboard, and nothing animating for prefers-reduced-motion to have to switch
 * off. The text is not hidden — it is one keypress away, next to what it is
 * about.
 */
export function FieldHelp({ summary = '为什么？', children }: Props) {
  return (
    <details className="field__help">
      <summary>{summary}</summary>
      <div className="field__help-body">{children}</div>
    </details>
  )
}
