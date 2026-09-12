import type { InstanceState } from '../types'

interface Props {
  state: InstanceState
  className?: string
}

/**
 * The one thing on screen that changes without anyone touching it.
 *
 * The stylesheet gives it a fifth-of-a-second transition on purpose: a dot
 * that snaps from grey to green is a change you only catch if you happened to
 * be looking at it, and the whole job of a status dot on a sidebar you are not
 * currently reading is to be caught in peripheral vision.
 *
 * Decorative: every place this appears, the state is also written next to it
 * in words, so there is nothing here for a screen reader that it will not
 * already have read.
 */
export function StatusDot({ state, className }: Props) {
  const classes = ['status__dot', `status__dot--${state}`]
  if (className) classes.push(className)
  return <span className={classes.join(' ')} aria-hidden="true" />
}
