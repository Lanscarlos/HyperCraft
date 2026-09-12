import { forwardRef } from 'react'
import type { ButtonHTMLAttributes } from 'react'

type Variant = 'default' | 'primary' | 'danger'
type Size = 'default' | 'small' | 'row'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** 'primary' is the one filled button a screen is allowed. */
  variant?: Variant
  /** 'row' is the size that lives inside a table row, and it composes with
   *  every variant — a destructive row button is `variant="danger"` at
   *  `size="row"`. Keeping it out of Variant is not a detail: as a variant it
   *  could not coexist with 'danger', and three call sites needed exactly
   *  that. */
  size?: Size
  /** Square, icon-only. Callers must pass aria-label as well. */
  icon?: boolean
}

const VARIANT: Record<Variant, string> = {
  default: '',
  primary: 'btn--primary',
  danger: 'btn--danger',
}

const SIZE: Record<Size, string> = {
  default: '',
  small: 'btn--small',
  row: 'btn--row',
}

/**
 * Every button in the panel, so that a size or a variant is a value rather
 * than a string someone has to spell right.
 *
 * It was a string for a long time, and `btn--small` — a class that never
 * existed in the stylesheet — was spelled correctly fifteen times and styled
 * zero of them. `btn--sm` was a sixteenth spelling of the same intent. Types
 * are the fix; check-ui.mjs is the backstop for the call sites still passing
 * raw classNames.
 *
 * Forwards its ref because the drawers focus their own close button on open.
 */
export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  { variant = 'default', size = 'default', icon, className, type, ...rest },
  ref,
) {
  const classes = ['btn', VARIANT[variant], SIZE[size]]
  if (icon) classes.push('btn--icon')
  if (className) classes.push(className)

  // A button inside a form defaults to submit, which has surprised this
  // codebase before — every caller here means an ordinary button unless it
  // says otherwise.
  return (
    <button ref={ref} type={type ?? 'button'} className={classes.filter(Boolean).join(' ')} {...rest} />
  )
})
