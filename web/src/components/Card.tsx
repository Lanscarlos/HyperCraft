import type { HTMLAttributes, ReactNode } from 'react'

type Pad = 'default' | 'tight' | 'none'
type Tone = 'raised' | 'sunken'
type As = 'div' | 'article' | 'section'

interface Props extends HTMLAttributes<HTMLElement> {
  pad?: Pad
  /** 'sunken' sits on --surface-2: a card inside a card. */
  tone?: Tone
  /** The whole card is the click target — gets the sheen and the hover lift. */
  interactive?: boolean
  /** The listing cards are <article role="listitem">; a hardcoded div would
   *  take the list semantics away from them. */
  as?: As
  children?: ReactNode
}

const PAD: Record<Pad, string> = { default: '', tight: 'card--tight', none: 'card--flush' }

/**
 * The surface a panel of content sits on.
 *
 * There were five of these — card, browse-card, schemcard, jvmcard, kpi — with
 * five paddings, two radii, two surfaces, and shadow and sheen on an arbitrary
 * two apiece. None of the differences meant anything; they are the order the
 * pages were written in. Two of the five sat next to each other on one screen.
 *
 * Each caller keeps its own block class alongside this one, so the rules for
 * what goes *inside* a card stay where they were written. Only the container
 * is shared.
 */
export function Card({
  pad = 'default',
  tone = 'raised',
  interactive,
  as: Tag = 'div',
  className,
  children,
  ...rest
}: Props) {
  const classes = ['card', PAD[pad]]
  if (tone === 'sunken') classes.push('card--sunken')
  if (interactive) classes.push('card--interactive')
  if (className) classes.push(className)
  return (
    <Tag className={classes.filter(Boolean).join(' ')} {...rest}>
      {children}
    </Tag>
  )
}
