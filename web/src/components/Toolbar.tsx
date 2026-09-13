import { forwardRef } from 'react'
import type { HTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'

/**
 * The one row of controls above a list.
 *
 * Search on the left, the filter chips beside it, a count or a note where the
 * eye rests, and the list's own tools at the far end. Seven pages had grown
 * seven of these (`.filters`, `.chart-filters`, `.chist__filters`,
 * `.schemlib__bar`, `.browse__search`, `.file-toolbar`…), each with its own
 * gap, its own search box and its own idea of which side the buttons go. A
 * list gets at most one of these, directly above it, and it is this one.
 *
 * The slots are class names rather than props, because a toolbar's children
 * are already components (`Button`, `Select`, `.chip`) and a prop per slot
 * would only wrap what the caller writes anyway:
 *
 *   <Toolbar>
 *     <ToolbarSearch … />
 *     <div className="toolbar__chips">…chips…</div>
 *     <span className="toolbar__count">12 / 40</span>
 *     <div className="toolbar__tools">…buttons…</div>
 *   </Toolbar>
 */
export function Toolbar({
  className,
  children,
  ...rest
}: HTMLAttributes<HTMLDivElement> & { children: ReactNode }) {
  return (
    <div className={['toolbar', className].filter(Boolean).join(' ')} {...rest}>
      {children}
    </div>
  )
}

/** The search box that starts a toolbar. `type="search"` for the clear
 *  affordance the platform gives it; the styling is the panel's. */
export const ToolbarSearch = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  function ToolbarSearch({ className, type, ...rest }, ref) {
    return (
      <input
        ref={ref}
        type={type ?? 'search'}
        className={['toolbar__search', className].filter(Boolean).join(' ')}
        {...rest}
      />
    )
  },
)
