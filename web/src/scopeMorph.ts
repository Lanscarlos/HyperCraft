import { DUR, EASE, reducedMotion } from './motion'

/**
 * Entering and leaving a scope, as one movement instead of a swap.
 *
 * The sidebar has three lists — the panel, one server, the machine — and
 * entering a server *replaces* the panel's list rather than expanding an item
 * inside it (see Sidebar). That is the right structure and the wrong cut: one
 * frame you are looking at a list of servers, the next frame at a list of the
 * console/文件/监控 pages, with nothing on screen connecting the row you
 * clicked to the header that row became. You have to re-read the sidebar to
 * find out where you are, every time, and re-reading your navigation is the
 * definition of a hard transition.
 *
 * So the row travels. The entry you clicked flies to the top of the column and
 * lands as the scope header; everything that was above it leaves upwards and
 * everything below it leaves downwards, clearing the space it is moving into;
 * the second-level list comes up underneath. Going back plays the same thing in
 * reverse — the header shrinks back down to the row it came from — because it
 * is the same code: the two elements are paired by a `data-nav-key` they share
 * across the two trees, and neither end needs to know which direction it is.
 *
 * The mechanics are the standard two-measurement trick and one piece of
 * bookkeeping around React. The outgoing list cannot animate, because by the
 * time we know the route changed React has already unmounted it — so a copy of
 * the sidebar is cloned into an overlay *before* the click is passed on, and it
 * is the copy that leaves. The copy is transparent apart from its content, so
 * the real sidebar rebuilding itself underneath shows through as the copy
 * clears.
 *
 * None of this runs when it would not be seen or not be wanted: not for a
 * reduced-motion reader, and not while the sidebar is a drawer, where the whole
 * column is sliding off the screen anyway.
 */

interface Pending {
  key: string
  /** Where the paired element was, in viewport coordinates, before the swap. */
  origin: DOMRect
  ghost: HTMLElement
  /** Cleans up a copy nobody came back for; see captureScope. */
  expiry: number
}

let pending: Pending | null = null

function paired(root: ParentNode, key: string): HTMLElement | null {
  return root.querySelector<HTMLElement>(`[data-nav-key="${CSS.escape(key)}"]`)
}

function discard(): void {
  if (!pending) return
  window.clearTimeout(pending.expiry)
  pending.ghost.remove()
  pending = null
}

/**
 * Called from the handler of a link that changes scope, before it navigates.
 *
 * `key` names the pair: `instance:<id>` or `host`. Whichever of the two ends
 * is on screen right now is the one it measures — a row on the way in, the
 * scope header on the way out.
 */
export function captureScope(key: string): void {
  // A second click while the first is still playing: the half-finished copy is
  // stale the moment the route moves again.
  discard()
  if (reducedMotion()) return

  const sidebar = document.querySelector<HTMLElement>('.sidebar')
  const app = sidebar?.closest<HTMLElement>('.app')
  if (!sidebar || !app) return
  // Drawer mode. The column is about to slide off the screen with everything
  // on it, so rearranging it on the way out is work nobody sees.
  if (app.dataset.nav === 'open') return

  const from = paired(sidebar, key)
  if (!from) return

  const box = sidebar.getBoundingClientRect()
  const clone = sidebar.cloneNode(true) as HTMLElement
  // One id and one tab stop per element in the document, please.
  clone.removeAttribute('id')
  clone.removeAttribute('tabindex')
  // Transparent, and that is the whole trick: the copy contributes its content
  // and nothing else, so the real sidebar rebuilding underneath is what the
  // reader sees through it as the copy's rows clear out. Inline, because it has
  // to beat the drawer's own position/transform rules in the stylesheet.
  clone.style.cssText =
    'position:absolute;inset:0;width:100%;height:100%;margin:0;' +
    'transform:none;visibility:visible;background:transparent;' +
    'border-right-color:transparent;box-shadow:none;'

  const ghost = document.createElement('div')
  ghost.className = 'sidebar__ghost'
  ghost.setAttribute('aria-hidden', 'true')
  ghost.style.cssText =
    `position:fixed;left:${box.left}px;top:${box.top}px;` +
    `width:${box.width}px;height:${box.height}px;` +
    'overflow:hidden;pointer-events:none;z-index:59;'
  ghost.appendChild(clone)
  // Appended to .app rather than to <body> so that `.app[data-rail='on']`
  // still reaches into it: a copy of a folded sidebar that renders unfolded is
  // not a copy. Last child, where React's own insertions never look.
  app.appendChild(ghost)

  // A copy that starts at the top of a list the original had scrolled halfway
  // down is not a copy either. Set after insertion; a detached node has no
  // scroll to set.
  const scrolled = sidebar.querySelector('.sidebar__scroll')
  const copy = clone.querySelector('.sidebar__scroll')
  if (scrolled && copy) copy.scrollTop = scrolled.scrollTop

  split(clone, key)
  pending = {
    key,
    origin: from.getBoundingClientRect(),
    ghost,
    // The copy is opaque nonsense the moment it stops being a picture of what
    // was just on screen, and it sits on top of the real column. If the render
    // it was taken for never arrives — a navigation cancelled, an error thrown
    // between here and the commit — nothing else would ever take it away.
    expiry: window.setTimeout(discard, 600),
  }
}

/**
 * Marks every block of the copy as being above or below the row that was
 * clicked, so the two halves can leave in opposite directions.
 *
 * Walking up from the row rather than down from the sidebar is what makes it
 * work at any depth: at each level the row's own ancestor is skipped and its
 * siblings are tagged, so the groups above it in the scroll region *and* the
 * brand above the scroll region both come out as "above" without this needing
 * to know the sidebar's shape.
 */
function split(clone: HTMLElement, key: string): void {
  const anchor = paired(clone, key)
  if (!anchor) return

  let node: Element = anchor
  while (node !== clone && node.parentElement) {
    const parent = node.parentElement
    let passed = false
    for (const child of Array.from(parent.children)) {
      if (child === node) {
        passed = true
        continue
      }
      ;(child as HTMLElement).dataset.part = passed ? 'below' : 'above'
    }
    node = parent
  }
  anchor.dataset.part = 'anchor'
}

/**
 * Called from the sidebar's layout effect, after the new scope has rendered.
 *
 * Does nothing when there is no copy waiting, which is the ordinary case for a
 * scope reached from ⌘K, a breadcrumb or the browser's back button: there is
 * no row those came from, so there is nothing to pair. The incoming list still
 * gets its own entrance — that part is CSS and does not need this.
 */
export function playScope(sidebar: HTMLElement): void {
  const job = pending
  if (!job) return
  window.clearTimeout(job.expiry)
  pending = null

  const { ghost, origin, key } = job

  // The two halves of the outgoing list, clearing the path.
  //
  // On --ease rather than the accelerating curve an exit would normally take,
  // and this is the one place in the panel where that is right. Both lists
  // occupy the same column, so every millisecond the old one is still legible
  // is a millisecond of two navigations printed over each other. --ease-in
  // spends its first third barely moving, which is exactly the third that has
  // to be over before the new list starts arriving; --ease is gone in half the
  // time and the overlap is a smear rather than a double exposure.
  const leave = (el: HTMLElement, dy: number) =>
    el.animate(
      [
        { transform: 'none', opacity: 1 },
        { transform: `translateY(${dy}px)`, opacity: 0 },
      ],
      { duration: DUR.base, easing: EASE, fill: 'forwards' },
    )
  ghost.querySelectorAll<HTMLElement>('[data-part="above"]').forEach((el) => leave(el, -22))
  ghost.querySelectorAll<HTMLElement>('[data-part="below"]').forEach((el) => leave(el, 22))

  // The row itself stays where it is and dissolves, because the element flying
  // out of that exact spot is its replacement. Two shapes crossing at the same
  // coordinates read as one thing becoming another; the same two with a gap
  // between them read as two.
  ghost
    .querySelector<HTMLElement>('[data-part="anchor"]')
    ?.animate([{ opacity: 1 }, { opacity: 0 }], {
      duration: DUR.fast,
      easing: EASE,
      fill: 'forwards',
    })

  const target = paired(sidebar, key)
  if (target) {
    const to = target.getBoundingClientRect()
    // Marks it as the thing in motion, which is how the stylesheet knows to
    // keep its own entrance off this one element. Removed when it lands, so
    // the next scope change starts from a clean sidebar.
    target.dataset.lead = ''
    const flight = target.animate(
      [
        {
          transform: `translate(${origin.left - to.left}px, ${origin.top - to.top}px)`,
          opacity: 0,
        },
        // Opaque well before it lands, so the last half of the journey is a
        // solid object moving rather than something still fading up.
        { opacity: 1, offset: 0.45 },
        { transform: 'none', opacity: 1 },
      ],
      { duration: DUR.slow, easing: EASE },
    )
    flight.finished.then(() => delete target.dataset.lead).catch(() => {})
  }

  window.setTimeout(() => ghost.remove(), DUR.base + 40)
}
