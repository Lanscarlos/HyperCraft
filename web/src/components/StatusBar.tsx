import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ReactNode } from 'react'

/**
 * The strip along the foot of every page.
 *
 * It carries two kinds of fact. One the shell always knows — which version of
 * the panel this is. The other only the page knows: where the caret is in the
 * file being edited, how many buffers are unsaved, which directory is open.
 * The second kind is what the bar exists for, and it is the reason the editor
 * no longer prints a caret position of its own: with two groups on screen
 * there were two of those lines and one caret, and a reader had to work out
 * which half was theirs before reading either.
 *
 * A page that contributes nothing leaves the halves empty and the bar still
 * stands. Furniture that comes and goes is furniture the eye has to re-find
 * after every navigation, and this one is 26px.
 */
export function StatusBar({ version }: { version: string }) {
  return (
    <footer className="statusbar">
      <div className="statusbar__half" id="statusbar-left" />
      <div className="statusbar__half statusbar__half--end">
        <span id="statusbar-right" className="statusbar__slot" />
        <span className="statusbar__version">{version}</span>
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
