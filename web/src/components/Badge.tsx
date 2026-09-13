import type { ReactNode } from 'react'

/** Exported because two callers keep a map from their own state to a tone —
 *  see SecurityPage's KIND_BADGE and Sidebar's ALERT_BADGE. */
export type BadgeTone =
  | 'neutral'
  | 'ok'
  | 'warn'
  | 'danger'
  | 'alert'
  | 'live'
  | 'muted'
  | 'update'
  | 'changed'

interface Props {
  /** Colour is a claim that something is wrong. Leave it neutral otherwise. */
  tone?: BadgeTone
  className?: string
  title?: string
  children: ReactNode
}

const TONE: Record<BadgeTone, string> = {
  neutral: '',
  ok: 'badge--ok',
  warn: 'badge--warn',
  danger: 'badge--danger',
  alert: 'badge--alert',
  live: 'badge--live',
  muted: 'badge--muted',
  update: 'badge--update',
  changed: 'badge--changed',
}

/**
 * The static state label.
 *
 * Not to be confused with Chip, which is a filter the user clicks — it has a
 * pointer cursor, an active state, and it shrinks when pressed. The two look
 * adjacent enough that merging them was tempting, and would have turned a
 * label into a control.
 *
 * The default tone is deliberately colourless. A list where every row carries
 * a coloured badge is a list where colour has stopped meaning anything, which
 * is the whole reason the panel keeps its palette this quiet.
 */
export function Badge({ tone = 'neutral', className, title, children }: Props) {
  const classes = ['badge', TONE[tone]]
  if (className) classes.push(className)
  return (
    <span className={classes.filter(Boolean).join(' ')} title={title}>
      {children}
    </span>
  )
}
