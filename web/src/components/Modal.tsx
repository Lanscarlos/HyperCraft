import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import type { MouseEvent, ReactNode } from 'react'

import { useDismiss } from '../useDismiss'

/**
 * The frame every dialog in the panel sits in.
 *
 * There were five copies of `<div className="modal">` and they agreed on the
 * markup and on nothing else: none of them closed on Escape, none of them
 * closed on a click outside, and all five vanished on 取消 without the card
 * ever animating away. Those are not five separate omissions — they are one
 * missing component, which is now this.
 *
 * What it owns is dismissal, in all three of its forms. The two shortcuts
 * anyone opening a dialog will try first, and the exit animation, which needs
 * the dialog to outlive the decision to close it by about a tenth of a second
 * (see useDismiss). The card itself stays with the caller: a form, a wide
 * picker and a confirmation are different enough inside that wrapping them in
 * one body would only mean five props to undo it again.
 *
 * Deliberately not here: a focus trap. Every dialog in the panel puts focus in
 * its first field on mount and the tab order behind them is a handful of
 * controls, so the cost of getting a trap subtly wrong outweighs what it buys.
 */

/** Which dialog is on top. The path picker opens from inside the create
 *  dialog, and one Escape should close the picker and leave the form standing
 *  — so only the last one to mount listens. */
const stack: symbol[] = []

interface Props {
  /** Called once the exit has played. Unmount the dialog here. */
  onClose: () => void
  /** Read out in place of the dialog's own heading where it has none. */
  label?: string
  /**
   * Set while the dialog has a request in flight. Escape and the backdrop stop
   * working, for the same reason its own 取消 button is greyed out: the
   * request is already on its way to the daemon, and closing the box would
   * only take away the one place its result can be reported.
   */
  busy?: boolean
  children: ReactNode
}

export function Modal({ onClose, label, busy, children }: Props) {
  const { leaving, close } = useDismiss(onClose)
  const closeRef = useRef(busy ? () => {} : close)
  closeRef.current = busy ? () => {} : close

  useEffect(() => {
    const token = Symbol('modal')
    stack.push(token)
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (stack[stack.length - 1] !== token) return
      event.preventDefault()
      closeRef.current()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      stack.splice(stack.indexOf(token), 1)
    }
  }, [])

  // Mousedown rather than click, and only when the press *starts* on the
  // backdrop: a selection dragged out of a text field and released over the
  // scrim is not a request to throw the form away.
  const onBackdrop = (event: MouseEvent<HTMLDivElement>) => {
    if (busy) return
    if (event.target === event.currentTarget) close()
  }

  // Rendered into <body>, not where it was written.
  //
  // A dialog is `position: fixed`, which means "relative to the viewport" only
  // as long as nothing above it in the DOM has a transform, a filter or a
  // backdrop-filter — any of those turn an ancestor into the containing block
  // that fixed positioning resolves against. Both are true here: the scrim
  // carries a backdrop blur, and a card that has finished its entrance is left
  // holding an identity matrix, which counts.
  //
  // That only matters for a dialog opened from inside another one, and there is
  // exactly one — the directory picker, opened from 新建实例 and 导入现有目录 —
  // which is precisely how it went unnoticed. It was being laid out inside the
  // card that opened it and clipped by that card's scroll box: no title, no
  // path bar, no buttons, a directory listing cut off top and bottom. Out here
  // there is nothing overhead to be trapped by.
  return createPortal(
    <div
      className="modal"
      data-state={leaving ? 'out' : 'in'}
      role="dialog"
      aria-modal="true"
      aria-label={label}
      onMouseDown={onBackdrop}
    >
      {children}
    </div>,
    document.body,
  )
}
