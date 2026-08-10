import { useRef, useState, useSyncExternalStore } from 'react'

import type { ConfirmAnswer, PendingConfirm } from '../confirm'
import { peekConfirm, settleConfirm, subscribeConfirm } from '../confirm'
import { Modal } from './Modal'

/**
 * Where the panel's confirmations are drawn.
 *
 * Mounted once, at the root, above the router — a question can be asked from
 * anywhere, including from code that is not a component at all, and this has
 * to be on screen for all of them. It renders whatever is at the head of the
 * queue in confirm.ts and nothing when the queue is empty, so the cost of
 * having it there is one subscription.
 */
export function ConfirmHost() {
  const pending = useSyncExternalStore(subscribeConfirm, peekConfirm, peekConfirm)
  if (!pending) return null
  // Keyed on the question, not the position: a second question opening behind
  // the first is a new card, and it should enter and take focus like one
  // rather than have its text swapped into the card already standing there.
  return <ConfirmDialog key={pending.id} request={pending} onAnswer={settleConfirm} />
}

function ConfirmDialog({
  request,
  onAnswer,
}: {
  request: PendingConfirm
  onAnswer: (answer: ConfirmAnswer) => void
}) {
  const [toggled, setToggled] = useState(request.toggle?.initial ?? false)
  // Every exit routes through Modal's close, and the answer has to survive the
  // ~150ms the card takes to leave, so it is written down before the exit
  // starts and read once it has finished. No is the default: Escape, a click
  // on the backdrop and an unmount all mean the same thing as 取消.
  const answer = useRef(false)
  const settle = (ok: boolean, close: () => void) => {
    answer.current = ok
    close()
  }

  return (
    <Modal
      onClose={() => onAnswer({ ok: answer.current, toggled: answer.current && toggled })}
      label={request.title}
    >
      {(close) => (
        <div className="modal__card confirm" data-tone={request.danger ? 'danger' : undefined}>
          <h2 className="modal__title">{request.title}</h2>
          {request.lead && <div className="modal__lead">{request.lead}</div>}
          {request.detail && <div className="confirm__note">{request.detail}</div>}

          {request.toggle && (
            <label className="checkbox checkbox--stacked">
              <input
                type="checkbox"
                checked={toggled}
                onChange={(event) => setToggled(event.target.checked)}
              />
              <span>{request.toggle.label}</span>
              {request.toggle.note && <small>{request.toggle.note}</small>}
            </label>
          )}

          <div className="modal__actions">
            {/* Focus starts on the way out, not on the way through. Enter is
                the reflex answer to a box that has just appeared, and on a
                dialog that deletes a world the reflex must not be the one that
                does it — so a dangerous question opens with 取消 focused and
                the red button a deliberate move away. */}
            <button
              className="btn"
              type="button"
              onClick={() => settle(false, close)}
              autoFocus={request.danger}
            >
              {request.cancelLabel ?? '取消'}
            </button>
            <button
              className={request.danger ? 'btn btn--danger' : 'btn btn--primary'}
              type="button"
              onClick={() => settle(true, close)}
              autoFocus={!request.danger}
            >
              {request.confirmLabel}
            </button>
          </div>
        </div>
      )}
    </Modal>
  )
}
