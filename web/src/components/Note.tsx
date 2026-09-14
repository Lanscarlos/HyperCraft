import type { ReactNode } from 'react'

/** Exported because Dashboard maps an AlertLevel onto it. */
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
 * `.alert`, which is the one slot under a page head where that page's own load
 * failure goes. All three used to share one class name, and that is the whole
 * reason this file exists: 「这超过了本机内存的八成」 and 「已保存」 were both
 * `.alert--*` divs, so nothing could tell that one of them belongs to a memory
 * slider and the other to a moment.
 *
 * The judgement, where it is not obvious: anything that can be rendered from
 * `if (condition)` is a note. Anything that can only be raised from an event
 * handler is a toast.
 */
export function Note({ tone = 'neutral', className, children }: Props) {
  const classes = ['note', TONE[tone], className].filter(Boolean).join(' ')
  return <div className={classes}>{children}</div>
}
