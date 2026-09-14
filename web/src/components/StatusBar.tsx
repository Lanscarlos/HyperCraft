import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ReactNode } from 'react'

/**
 * The strip along the foot of every page.
 *
 * Everything in it comes from the page: where the caret is in the file being
 * edited, how many buffers are unsaved, which directory is open. That is what
 * the bar exists for, and it is the reason the editor no longer prints a caret
 * position of its own — with two groups on screen there were two of those
 * lines and one caret, and a reader had to work out which half was theirs
 * before reading either.
 *
 * It used to end with the panel's version as well. That went when the product
 * name in the rail learnt to show the version on hover: a fact with two homes
 * is a fact that can disagree with itself, and of the two the rail's is where
 * somebody looks for it — beside the name of the thing it is the version of.
 *
 * A page that contributes nothing leaves both halves empty and the bar still
 * stands. Furniture that comes and goes is furniture the eye has to re-find
 * after every navigation, and this one is 26px.
 */
export function StatusBar() {
  return (
    <footer className="statusbar">
      <div className="statusbar__half" id="statusbar-left" />
      <div className="statusbar__half statusbar__half--end">
        <span id="statusbar-right" className="statusbar__slot" />
      </div>
    </footer>
  )
}

/**
 * What a page puts in the bar, from wherever in the page happens to know it.
 *
 * A portal rather than a context the page writes into with an effect, and the
 * difference is not style: the content is `ReactNode`, which is a new value on
 * every render, so pushing it into state would schedule a render per render.
 * Portalled, React reconciles it the same way it reconciles anything else and
 * there is no state in the middle to get stuck in a loop.
 */
export function StatusSlot({
  side = 'left',
  children,
}: {
  side?: 'left' | 'right'
  children: ReactNode
}) {
  const [host, setHost] = useState<HTMLElement | null>(null)

  // After mount, not during render: the bar is a sibling further down the
  // shell, so on the first pass of a fresh page the node is not there yet.
  useEffect(() => {
    setHost(document.getElementById(`statusbar-${side}`))
  }, [side])

  return host === null ? null : createPortal(children, host)
}
