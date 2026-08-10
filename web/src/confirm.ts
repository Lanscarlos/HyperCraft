import type { ReactNode } from 'react'

/**
 * The panel's own yes/no box, kept in the shape of the one it replaces.
 *
 * Seventeen places used to call window.confirm, and the native box is the
 * wrong instrument for every one of them. It puts 147.139.140.46:19190 显示
 * above the question, which tells the reader that this is the browser talking
 * and not the panel. It cannot render anything — no file list, no size, no red
 * on the button that deletes a world — so the whole warning arrives as one
 * paragraph of plain text with \n\n doing the work of layout. It blocks the
 * main thread while it is up. And on a phone it is browser chrome, dropped
 * from the top of the screen, styled by the vendor.
 *
 * What is kept from it is the calling convention, because that is the part
 * that was right: ask, await, act. A flow reads top to bottom whether the
 * question is answered by Chrome or by a card of ours —
 *
 *     if (!(await ask({ ... }))) return
 *
 * — so replacing the box is a one-line change at each site and nothing has to
 * be turned inside out into callbacks.
 *
 * This module is deliberately not a hook or a context. Half the callers are
 * not components: a couple of module-level helpers in the plugin library, the
 * 409 retry inside useCores, the power confirmation that is a plain function
 * over an instance. A context would have meant threading a prop through all of
 * them to reach the same one dialog. The queue lives here, ConfirmHost is
 * mounted once at the root, and asking is a function call from anywhere.
 */

/** The checkbox some questions carry — see `askWithToggle`. */
export interface ConfirmToggle {
  label: string
  /** The half-line under the label, for what the choice actually costs. */
  note?: ReactNode
  initial?: boolean
}

export interface ConfirmRequest {
  /** The question itself, short enough to be the heading. */
  title: string
  /** What is about to happen, in a sentence or two. */
  lead?: ReactNode
  /** The consequence, set apart in its own block: what survives, what does
   *  not, what this does *not* touch. Reassurance and warning both live here,
   *  and both are easier to find out of the run of the lead. */
  detail?: ReactNode
  confirmLabel: string
  /** Defaults to 取消. Worth setting when the two buttons are two actions
   *  rather than an action and a way out. */
  cancelLabel?: string
  /** Paints the confirming button red. For anything that destroys bytes. */
  danger?: boolean
  toggle?: ConfirmToggle
}

export interface ConfirmAnswer {
  ok: boolean
  /** The state of `toggle` when the answer was given; false when there is
   *  none. Only meaningful together with `ok` — a question that was declined
   *  says nothing about the box on it. */
  toggled: boolean
}

export interface PendingConfirm extends ConfirmRequest {
  id: number
  resolve: (answer: ConfirmAnswer) => void
}

/** Questions waiting to be put, oldest first. Two of them can be in flight —
 *  a rollback asks, then asks again — and a dialog that replaced the one on
 *  screen would drop the first answer on the floor. */
const queue: PendingConfirm[] = []
const listeners = new Set<() => void>()
let nextId = 1

function emit() {
  for (const listener of listeners) listener()
}

function enqueue(request: ConfirmRequest): Promise<ConfirmAnswer> {
  return new Promise<ConfirmAnswer>((resolve) => {
    queue.push({ ...request, id: nextId++, resolve })
    emit()
  })
}

/** Put a question. Resolves true only if the confirming button was pressed —
 *  Escape, the backdrop and 取消 all answer false, as they should. */
export function ask(request: ConfirmRequest): Promise<boolean> {
  return enqueue(request).then((answer) => answer.ok)
}

/**
 * The same, for a question with a second, smaller question inside it.
 *
 * Stacking two boxes is how this used to be done and it reads badly: the
 * second one arrives after the decision is already made, with 确定 and 取消
 * standing for two things that are both "yes". One card with a checkbox on it
 * says the same thing in one reading, and leaves Escape meaning what it means
 * everywhere else — nothing happens.
 */
export function askWithToggle(
  request: ConfirmRequest & { toggle: ConfirmToggle },
): Promise<ConfirmAnswer> {
  return enqueue(request)
}

export function subscribeConfirm(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** The question on screen, or null. Stable between renders while it is the
 *  same question, which is what useSyncExternalStore requires of it. */
export function peekConfirm(): PendingConfirm | null {
  return queue[0] ?? null
}

export function settleConfirm(answer: ConfirmAnswer): void {
  const asked = queue.shift()
  emit()
  asked?.resolve(answer)
}
