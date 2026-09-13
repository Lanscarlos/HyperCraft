import type { HTMLAttributes, ReactNode } from 'react'

type Tone = 'default' | 'warn' | 'danger'
type As = 'section' | 'article' | 'div' | 'aside'

interface Props extends Omit<HTMLAttributes<HTMLElement>, 'title'> {
  title?: ReactNode
  /** A number beside the title — how many things the section holds. */
  count?: ReactNode
  /** One sentence under the title: why this section exists. Sentences go
   *  here, never in `meta`. */
  note?: ReactNode
  /** A short fact at the end of the head — "物理 15.7 GB", "linux/x64".
   *  A few words at most; anything with a verb in it is a `note`. */
  meta?: ReactNode
  /** The section's own controls, at the end of the head. A source picker, a
   *  refresh link, the one button that adds a row. */
  tools?: ReactNode
  tone?: Tone
  /** Fields. The body stops at the reading measure (880px) so a row of
   *  inputs on a wide monitor is still a form and not a search for the next
   *  control; the trailing space is deliberate. */
  form?: boolean
  as?: As
  children?: ReactNode
}

const TONE: Record<Tone, string> = {
  default: '',
  warn: 'panel--warn',
  danger: 'panel--danger',
}

/**
 * A titled block of content on a page, and the only way to write one.
 *
 * There were five ways to open a card — `.chart-head` with a meta line,
 * `.panel__head` with a button, `.update__head`, the form panel's `.panel__aside`,
 * and a bare `<h3>` — and a sixth and seventh on the pages that had grown
 * their own (`.dlqueue__title`, `.foreign__title`). They put the note in three
 * different places, the count in two, and the tools on whichever side the
 * author remembered. Read one page after another and the panel looked
 * assembled by seven people. This is the one shape: title and note on the
 * left, a short fact and the controls on the right, the body under both.
 *
 * `form` is the settings-page variant: the body is capped at the reading
 * measure and stacks its fields at the form rhythm. check-ui.mjs guarantees
 * `.panel--form` is only ever written here, so the cap cannot be forgotten.
 */
export function Section({
  title,
  count,
  note,
  meta,
  tools,
  tone = 'default',
  form,
  as: Tag = 'section',
  className,
  children,
  ...rest
}: Props) {
  const classes = ['panel', TONE[tone]]
  if (form) classes.push('panel--form')
  if (className) classes.push(className)

  const head = title !== undefined || note !== undefined || meta !== undefined || tools !== undefined

  return (
    <Tag className={classes.filter(Boolean).join(' ')} {...rest}>
      {head && (
        <header className="panel__head">
          {(title !== undefined || note !== undefined) && (
            <div className="panel__heading">
              {title !== undefined && (
                <h2 className="panel__title">
                  {title}
                  {count !== undefined && <span className="panel__count">{count}</span>}
                </h2>
              )}
              {note !== undefined && <p className="panel__note">{note}</p>}
            </div>
          )}
          {meta !== undefined && <p className="panel__meta">{meta}</p>}
          {tools !== undefined && <div className="panel__tools">{tools}</div>}
        </header>
      )}
      {form ? <div className="panel__body">{children}</div> : children}
    </Tag>
  )
}
