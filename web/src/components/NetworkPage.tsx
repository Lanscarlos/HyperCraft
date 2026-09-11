import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import { toast } from '../toast'
import type {
  InstanceStatus,
  NetworkLink,
  NetworkProxy,
  NetworkResponse,
  NetworkServer,
} from '../types'
import { useMediaQuery } from '../useMediaQuery'
import { Page } from './Page'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/**
 * 代理连线 — which proxy stands in front of which servers.
 *
 * Two columns and a line between them, because that is what the thing *is*:
 * players arrive at one address and end up on one of several servers, and the
 * question anybody has about a network is which ones. Drawing the line does the
 * six edits that make it true — the proxy's [servers] table, the forwarding
 * mode and its secret, the backend's online-mode and its own forwarding switch
 * — and the same six read backwards are how a network somebody wired by hand
 * shows up here without ever having touched this page.
 *
 * Below 900px the canvas is not a canvas: two columns of cards with curves
 * between them need both columns on screen at once, and on a phone they are
 * not. What is left is the same information as a list — which proxy, which
 * servers, what is wrong with each link — and the same actions as buttons. The
 * picture is the luxury; the wiring is the point.
 */
const CANVAS_QUERY = '(min-width: 900px)'

interface Props {
  instances: InstanceStatus[]
  onOpenInstance: (id: string) => void
  onCreate: () => void
  /**
   * The instance this page is being read from, when it is a section of one
   * rather than a page of its own.
   *
   * A link is a fact about two instances and neither owns it, so both ends
   * carry this section and each sees the half it can act on: a proxy keeps its
   * own card and every server it could reach, a server keeps its own card and
   * every proxy that could stand in front of it. Which half is which is read
   * off the data rather than passed in — the daemon has already decided what a
   * core is, and a second answer here could disagree with it.
   */
  focus?: string
  /**
   * Rendered inside an instance pane, which owns the scroll and the heading
   * rhythm. Page would put a second scroll container and a second h1 inside
   * one that already has both.
   */
  embed?: boolean
}

export function NetworkPage({ instances, onOpenInstance, onCreate, focus, embed }: Props) {
  const [data, setData] = useState<NetworkResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [notes, setNotes] = useState<string[]>([])
  const canvas = useMediaQuery(CANVAS_QUERY)

  const load = useCallback(async () => {
    try {
      setData(await api.getNetwork())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取网络拓扑失败')
    }
  }, [])

  // Also re-read when the instance list changes length: a link's health depends
  // on files this page does not own, and a server created or deleted elsewhere
  // must not leave this page describing a network that no longer exists.
  useEffect(() => {
    void load()
  }, [load, instances.length])

  const run = async (
    action: () => Promise<{ notes: string[] } & NetworkResponse>,
    done: string,
  ) => {
    setBusy(true)
    setError(null)
    // A failure must not leave the previous success's "改了这些" list sitting
    // above the error, as though it were an account of what just happened.
    setNotes([])
    try {
      const result = await action()
      setData(result)
      setNotes(result.notes)
      toast(done)
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const link = (proxyId: string, serverId: string) =>
    run(() => api.linkNetwork(proxyId, serverId), '已连接')

  const repair = (proxyId: string, serverId: string) =>
    run(() => api.repairNetwork(proxyId, serverId), '已修复')

  const unlink = async (proxy: NetworkProxy, server: NetworkServer) => {
    const ok = await ask({
      title: `断开 ${server.name}？`,
      lead: `会把它从 ${proxy.name} 的子服列表里去掉，并且把它自己的转发关掉。`,
      detail:
        '同时把它的正版验证开回来 —— 前面没有代理端挡着的话，' +
        '关着验证谁都能用任意 ID 进服。世界和插件不动。',
      confirmLabel: '断开',
      danger: true,
    })
    if (!ok) return
    await run(() => api.unlinkNetwork(proxy.id, server.id), '已断开')
  }

  const side = sideOf(data, focus)
  const reload = (
    <div className="actions">
      <button className="btn" type="button" onClick={() => void load()} disabled={busy}>
        重新读取
      </button>
    </div>
  )

  if (!data) {
    return (
      <Frame embed={embed} title={headingOf(side)} lead={leadOf(side, canvas)} aside={reload}>
        {error ? (
          <div className="alert alert--error">{error}</div>
        ) : (
          <SkeletonScreen label="正在读取网络拓扑…">
            <SkeletonPanel title={false}>
              <Skeleton w="34%" h={15} />
              <Skeleton w="100%" h={72} />
              <Skeleton w="100%" h={72} />
            </SkeletonPanel>
          </SkeletonScreen>
        )}
      </Frame>
    )
  }

  const view = focused(data, focus, side)
  // Read off the half that is on screen, not off the whole machine: from a
  // server's side "还没有代理端" is the true and useful thing to say when the
  // panel runs none, and it is the only case where this column can be empty.
  const empty = view.proxies.length === 0

  return (
    <Frame embed={embed} title={headingOf(side)} lead={leadOf(side, canvas)} aside={reload}>
      {error && <div className="alert alert--error">{error}</div>}

      {notes.length > 0 && (
        <div className="alert alert--ok">
          <strong>改了这些：</strong>
          <ul className="netnotes">
            {notes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        </div>
      )}

      {empty ? (
        <section className="panel">
          <h3 className="panel__title">还没有代理端</h3>
          <p className="muted">
            代理端（Velocity）站在所有服务端前面：玩家只连它一个地址，
            再由它把人送到大厅、生存、创造去 —— 玩家在服务器之间跳的时候不用退出重连。
            新建实例时选一个 Velocity 核心就有了。
          </p>
          <div className="actions">
            <button className="btn btn--primary" type="button" onClick={onCreate}>
              新建代理端
            </button>
          </div>
        </section>
      ) : canvas ? (
        <NetworkCanvas
          data={view}
          all={data}
          focus={focus}
          side={side}
          busy={busy}
          onLink={link}
          onRepair={repair}
          onUnlink={unlink}
          onOpenInstance={onOpenInstance}
        />
      ) : (
        <NetworkList
          data={view}
          all={data}
          focus={focus}
          side={side}
          busy={busy}
          onLink={link}
          onRepair={repair}
          onUnlink={unlink}
          onOpenInstance={onOpenInstance}
        />
      )}
    </Frame>
  )
}

/** Which end of a link the reader is standing on, or null for the whole
 *  machine. Answered from the daemon's own lists so that an instance it does
 *  not count as either — a proxy whose config has not been written yet — falls
 *  back to the full picture instead of to an empty one. */
function sideOf(data: NetworkResponse | null, focus: string | undefined): Side {
  if (!data || !focus) return null
  if (data.proxies.some((proxy) => proxy.id === focus)) return 'proxy'
  if (data.servers.some((server) => server.id === focus)) return 'server'
  return null
}

type Side = 'proxy' | 'server' | null

/**
 * The machine as seen from one end of a link.
 *
 * One card of your own on your side, every card you could legally connect to on
 * the other. Filtering rather than a second layout because the canvas is
 * already symmetric: a proxy on the left and a column of servers on the right
 * is the same picture as a column of proxies on the left and one server on the
 * right, read from the other end.
 *
 * The *cards* are filtered and the links are not, which is deliberate and was
 * not true at first. A proxy's sub-server table is one setting with several
 * rows in it, and showing a server only its own row described a file that does
 * not exist: you could not see that you are lobby #3 of four, and from the
 * proxy's side you could not see that the server you are about to connect is
 * already behind a different proxy. The rows a reader has no business editing
 * from here are shown and not editable — see `own` in the two card lists —
 * rather than hidden, because a table with rows missing is the kind of picture
 * you make a decision against and get wrong.
 */
function focused(data: NetworkResponse, focus: string | undefined, side: Side): NetworkResponse {
  if (side === 'proxy') {
    return { ...data, proxies: data.proxies.filter((proxy) => proxy.id === focus) }
  }
  if (side === 'server') {
    return { ...data, servers: data.servers.filter((server) => server.id === focus) }
  }
  return data
}

/** Named for what the reader is looking at, not for the feature: the sidebar
 *  row and the breadcrumb above this already say 代理连线, and repeating it
 *  here would spend the heading on a word that is already on screen twice. */
function headingOf(side: Side): string {
  if (side === 'proxy') return '后面的服务端'
  if (side === 'server') return '前面的代理端'
  return '代理连线'
}

function leadOf(side: Side, canvas: boolean): string {
  if (side === 'proxy') {
    return canvas
      ? '左边是这个代理端，右边是这台机器上的服务端。拖一条线过去，面板会把两边的配置一起改好。'
      : '这个代理端后面挂着哪些服务端。连一个过来，面板会把两边的配置一起改好。'
  }
  if (side === 'server') {
    return canvas
      ? '左边是这台机器上的代理端，右边是这个服务端。拖一条线过来，面板会把两边的配置一起改好。'
      : '这个服务端挂在哪个代理端后面。连过去，面板会把两边的配置一起改好。'
  }
  return canvas
    ? '左边是代理端，右边是服务端。拖一条线过去，面板会把两边的配置一起改好。'
    : '每个代理端后面挂着哪些服务端。连一个过来，面板会把两边的配置一起改好。'
}

/**
 * The page's own frame, or none at all.
 *
 * Standing on its own it is a wide Page like any other. Inside an instance it
 * is a section among six, and the pane around it already owns the scroll, the
 * width cap and the h1 — so it borrows the heading rhythm the other sections
 * use rather than nesting a second page inside the first.
 */
function Frame({
  embed,
  title,
  lead,
  aside,
  children,
}: {
  embed: boolean | undefined
  title: string
  lead: string
  aside: ReactNode
  children: ReactNode
}) {
  if (!embed) {
    return (
      <Page title={title} lead={lead} wide aside={aside}>
        {children}
      </Page>
    )
  }
  return (
    <div className="stack">
      <header className="chart-head">
        <h2 className="panel__title">{title}</h2>
        {aside}
      </header>
      <p className="page__lead">{lead}</p>
      {children}
    </div>
  )
}

interface ViewProps {
  /** Which cards to draw — one end of a link and everything it could reach,
   *  or the whole machine. */
  data: NetworkResponse
  /** The machine as the daemon reported it, for the rows that name an instance
   *  whose card is not on this screen: a proxy's other sub-servers, or the
   *  proxy some server is already behind. Filtering `data` is how a card is
   *  kept off the canvas; it must not also cost that instance its name. */
  all: NetworkResponse
  /** The instance the reader is standing on, with the side it stands on. Rows
   *  belonging to neither are shown and not editable. */
  focus?: string
  side: Side
  busy: boolean
  onLink: (proxyId: string, serverId: string) => void
  onRepair: (proxyId: string, serverId: string) => void
  onUnlink: (proxy: NetworkProxy, server: NetworkServer) => void
  onOpenInstance: (id: string) => void
}

/**
 * Whether this row is the reader's to change.
 *
 * Only one case says no: standing on a server, looking at the proxy's table of
 * sub-servers, at a row about some *other* server. That row is a fact about two
 * instances you are not one of — 断开 on it would disconnect somebody else's
 * server from a page about yours — so it stays legible and inert, and its name
 * is the way to the page where it *is* editable.
 */
function editable(link: NetworkLink, focus: string | undefined, side: Side): boolean {
  return side !== 'server' || link.serverId === focus
}

/** Where one card's connector sits, in the canvas's own coordinates. */
interface Anchor {
  x: number
  y: number
}

/** A line being dragged, before it has landed on anything. */
interface Dragging {
  kind: 'proxy' | 'server'
  id: string
  x: number
  y: number
  /** The card under the pointer right now, if it is a legal target. */
  over: string | null
}

function NetworkCanvas({
  data,
  all,
  focus,
  side,
  busy,
  onLink,
  onRepair,
  onUnlink,
  onOpenInstance,
}: ViewProps) {
  const canvasRef = useRef<HTMLDivElement | null>(null)
  const nodes = useRef(new Map<string, HTMLElement>())
  const [anchors, setAnchors] = useState<Record<string, Anchor>>({})
  const [drag, setDrag] = useState<Dragging | null>(null)
  // Set by a click rather than a drag: press one connector, then press the
  // other. This is the keyboard path — the connectors are real buttons — and
  // it is also what a shaky hand on a trackpad ends up using.
  const [armed, setArmed] = useState<{ kind: 'proxy' | 'server'; id: string } | null>(null)
  // A pointer that moved was a drag, and the click the browser sends after it
  // is not a second gesture. Without this, every completed drag also armed its
  // own source and left the page waiting for a second press that would undo
  // what just happened.
  const dragged = useRef(false)

  const measure = useCallback(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const base = canvas.getBoundingClientRect()
    const next: Record<string, Anchor> = {}
    for (const [key, element] of nodes.current) {
      const rect = element.getBoundingClientRect()
      // The proxy's line leaves its right edge and the server's enters on its
      // left, which is what makes the curves read as a direction rather than
      // as a web.
      const right = key.startsWith('proxy:')
      next[key] = {
        x: (right ? rect.right : rect.left) - base.left,
        y: rect.top + rect.height / 2 - base.top,
      }
    }
    setAnchors(next)
  }, [])

  useLayoutEffect(() => {
    measure()
    const canvas = canvasRef.current
    if (!canvas || typeof ResizeObserver === 'undefined') return
    // The cards change height on their own — an issue list appears, a link is
    // removed — so a resize listener on the window is not enough.
    const observer = new ResizeObserver(measure)
    observer.observe(canvas)
    for (const element of nodes.current.values()) observer.observe(element)
    return () => observer.disconnect()
  }, [measure, data])

  const register = (key: string) => (element: HTMLElement | null) => {
    if (element) nodes.current.set(key, element)
    else nodes.current.delete(key)
  }

  const finish = (kind: 'proxy' | 'server', id: string) => {
    if (dragged.current) {
      dragged.current = false
      return
    }
    const start = armed
    if (!start) {
      setArmed({ kind, id })
      return
    }
    if (start.kind === kind) {
      // Pressing another connector on the same side is a change of mind, not
      // an impossible link.
      setArmed(start.id === id ? null : { kind, id })
      return
    }
    setArmed(null)
    const proxyId = kind === 'proxy' ? id : start.id
    const serverId = kind === 'server' ? id : start.id
    onLink(proxyId, serverId)
  }

  const startDrag = (kind: 'proxy' | 'server', id: string) => (event: React.PointerEvent) => {
    if (event.button !== 0) return
    const canvas = canvasRef.current
    if (!canvas) return
    const base = canvas.getBoundingClientRect()
    event.currentTarget.setPointerCapture(event.pointerId)
    dragged.current = false
    setDrag({
      kind,
      id,
      x: event.clientX - base.left,
      y: event.clientY - base.top,
      over: null,
    })
  }

  const moveDrag = (event: React.PointerEvent) => {
    if (!drag) return
    const canvas = canvasRef.current
    if (!canvas) return
    const base = canvas.getBoundingClientRect()

    // The pointer is captured by the connector, so the element under it has to
    // be asked for rather than read off the event.
    if (Math.abs(event.clientX - base.left - drag.x) + Math.abs(event.clientY - base.top - drag.y) > 3) {
      dragged.current = true
    }

    const under = document.elementFromPoint(event.clientX, event.clientY)
    const card = under?.closest<HTMLElement>('[data-node]')
    const key = card?.dataset.node ?? ''
    const wanted = drag.kind === 'proxy' ? 'server:' : 'proxy:'
    setDrag({
      ...drag,
      x: event.clientX - base.left,
      y: event.clientY - base.top,
      over: key.startsWith(wanted) ? key.slice(wanted.length) : null,
    })
  }

  const endDrag = () => {
    if (!drag) return
    const { kind, id, over } = drag
    setDrag(null)
    if (!over) return
    setArmed(null)
    onLink(kind === 'proxy' ? id : over, kind === 'server' ? id : over)
  }

  useEffect(() => {
    if (!armed && !drag) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setArmed(null)
      setDrag(null)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [armed, drag])

  // Off `all`, not off `data`: the cards on screen are a filtered view, and a
  // row naming one that was filtered out still has to be able to say its name.
  const serverOf = (id: string) => all.servers.find((server) => server.id === id)
  const proxyOf = (id: string) => all.proxies.find((proxy) => proxy.id === id)

  return (
    <div className="netmap" ref={canvasRef} data-armed={armed?.kind}>
      {/* Under the cards, not over them: the lines are the picture, the cards
          are what you press. */}
      <svg className="netmap__wires" aria-hidden="true">
        {data.links.map((link) => {
          const from = anchors[`proxy:${link.proxyId}`]
          const to = anchors[`server:${link.serverId}`]
          if (!from || !to) return null
          return (
            // Keyed by the sub-server's name, not the instance: one backend
            // listed twice under two names is a real setup (an alias for a
            // warp plugin), and two links keyed by the same instance are two
            // React children claiming one key.
            <path
              key={`${link.proxyId}-${link.name}`}
              className={`netwire netwire--${link.status}`}
              d={curve(from, to)}
            />
          )
        })}
        {drag &&
          (() => {
            const from = anchors[`${drag.kind}:${drag.id}`]
            if (!from) return null
            const to = drag.over
              ? (anchors[`${drag.kind === 'proxy' ? 'server' : 'proxy'}:${drag.over}`] ?? {
                  x: drag.x,
                  y: drag.y,
                })
              : { x: drag.x, y: drag.y }
            return (
              <path
                className="netwire netwire--draft"
                d={drag.kind === 'proxy' ? curve(from, to) : curve(to, from)}
              />
            )
          })()}
      </svg>

      <div className="netmap__column">
        <h2 className="netmap__heading">代理端</h2>
        {data.proxies.map((proxy) => (
          <ProxyCard
            key={proxy.id}
            proxy={proxy}
            data={all}
            focus={focus}
            side={side}
            busy={busy}
            nodeRef={register(`proxy:${proxy.id}`)}
            armed={armed?.kind === 'proxy' && armed.id === proxy.id}
            target={drag?.kind === 'server' && drag.over === proxy.id}
            onConnector={{
              onPointerDown: startDrag('proxy', proxy.id),
              onPointerMove: moveDrag,
              onPointerUp: endDrag,
              onClick: () => finish('proxy', proxy.id),
            }}
            onRepair={onRepair}
            onUnlink={onUnlink}
            onOpenInstance={onOpenInstance}
            serverOf={serverOf}
          />
        ))}
      </div>

      <div className="netmap__column">
        <h2 className="netmap__heading">服务端</h2>
        {data.servers.length === 0 && (
          <p className="muted">还没有服务端。代理端后面一个服务端都没有，玩家连上来会立刻被踢。</p>
        )}
        {data.servers.map((server) => (
          <ServerCard
            key={server.id}
            server={server}
            links={all.links.filter((link) => link.serverId === server.id)}
            proxyOf={proxyOf}
            busy={busy}
            nodeRef={register(`server:${server.id}`)}
            armed={armed?.kind === 'server' && armed.id === server.id}
            target={drag?.kind === 'proxy' && drag.over === server.id}
            onConnector={{
              onPointerDown: startDrag('server', server.id),
              onPointerMove: moveDrag,
              onPointerUp: endDrag,
              onClick: () => finish('server', server.id),
            }}
            onOpenInstance={onOpenInstance}
          />
        ))}
      </div>

      <p className="netmap__hint muted">
        {armed
          ? '再点另一边的接口就连上了，按 Esc 取消。'
          : '从一边的接口拖到另一边即可连线；点一下接口再点另一边也行。'}
      </p>
    </div>
  )
}

/** A cubic from one anchor to another, flattened when the two are close so a
 *  short hop does not loop out into the gutter. */
function curve(from: Anchor, to: Anchor): string {
  const reach = Math.max(28, Math.min(120, Math.abs(to.x - from.x) / 2))
  return `M ${from.x} ${from.y} C ${from.x + reach} ${from.y} ${to.x - reach} ${to.y} ${to.x} ${to.y}`
}

interface ConnectorProps {
  onPointerDown: (event: React.PointerEvent) => void
  onPointerMove: (event: React.PointerEvent) => void
  onPointerUp: () => void
  onClick: () => void
}

function Connector({
  side,
  label,
  armed,
  handlers,
}: {
  side: 'left' | 'right'
  label: string
  armed: boolean
  handlers: ConnectorProps
}) {
  return (
    <button
      className={`netport netport--${side}${armed ? ' netport--armed' : ''}`}
      type="button"
      aria-label={label}
      aria-pressed={armed}
      {...handlers}
    >
      <span aria-hidden="true" />
    </button>
  )
}

function ProxyCard({
  proxy,
  data,
  focus,
  side,
  busy,
  nodeRef,
  armed,
  target,
  onConnector,
  onRepair,
  onUnlink,
  onOpenInstance,
  serverOf,
}: {
  proxy: NetworkProxy
  data: NetworkResponse
  focus: string | undefined
  side: Side
  busy: boolean
  nodeRef: (element: HTMLElement | null) => void
  armed: boolean
  target: boolean
  onConnector: ConnectorProps
  onRepair: (proxyId: string, serverId: string) => void
  onUnlink: (proxy: NetworkProxy, server: NetworkServer) => void
  onOpenInstance: (id: string) => void
  serverOf: (id: string) => NetworkServer | undefined
}) {
  const links = data.links.filter((link) => link.proxyId === proxy.id)
  const foreign = proxy.entries.filter((entry) => !entry.instanceId)

  return (
    <article
      className={`netcard netcard--proxy${target ? ' netcard--target' : ''}`}
      ref={nodeRef}
      data-node={`proxy:${proxy.id}`}
    >
      <header className="netcard__head">
        <span className={`status__dot status__dot--${proxy.state}`} />
        <button className="netcard__name" type="button" onClick={() => onOpenInstance(proxy.id)}>
          {proxy.name}
        </button>
      </header>

      <p className="netcard__meta">
        <span title="监听地址">{proxy.bind || '0.0.0.0:25577'}</span>
        <span className={`badge${proxy.forwarding === 'none' ? ' badge--warn' : ''}`}>
          {forwardingLabel(proxy.forwarding)}
        </span>
        {!proxy.onlineMode && <span className="badge badge--warn">离线模式</span>}
      </p>

      {links.length === 0 && foreign.length === 0 ? (
        <p className="muted">还没有子服。玩家连上来会立刻被踢。</p>
      ) : (
        <ul className="netlinks">
          {links.map((link) => {
            const server = serverOf(link.serverId)
            const mine = editable(link, focus, side)
            return (
              <li className={`netlink${mine ? '' : ' netlink--other'}`} key={link.name}>
                <div className="netlink__head">
                  <span className={`netlink__dot netlink__dot--${link.status}`} aria-hidden="true" />
                  <strong>{link.name}</strong>
                  {/* A row you cannot act on here can still be acted on
                      somewhere, and the name is the way there. */}
                  {mine || !server ? (
                    <span className="netlink__to">{server?.name ?? link.address}</span>
                  ) : (
                    <button
                      className="netlink__to netlink__open"
                      type="button"
                      onClick={() => onOpenInstance(server.id)}
                    >
                      {server.name}
                    </button>
                  )}
                  {link.try && <span className="badge">落点</span>}
                </div>
                {/* Somebody else's sub-server keeps its status dot and loses
                    the list behind it: what is wrong with it is a sentence for
                    its own page, and four of them stacked here would bury the
                    one row on this card that is actually yours. */}
                {mine && link.issues.length > 0 && (
                  <ul className="netlink__issues">
                    {link.issues.map((issue) => (
                      <li key={issue}>{issue}</li>
                    ))}
                  </ul>
                )}
                {mine && (
                  <div className="netlink__actions">
                    {link.status !== 'ok' && (
                      <button
                        className="btn btn--row"
                        type="button"
                        disabled={busy}
                        onClick={() => onRepair(proxy.id, link.serverId)}
                      >
                        修复
                      </button>
                    )}
                    <button
                      className="btn btn--row btn--danger"
                      type="button"
                      disabled={busy || !server}
                      onClick={() => server && onUnlink(proxy, server)}
                    >
                      断开
                    </button>
                  </div>
                )}
              </li>
            )
          })}

          {/* Sub-servers pointing somewhere this panel does not manage —
              another machine — are a real setup, and hiding them would make
              the picture lie about how many servers are behind this proxy. */}
          {foreign.map((entry) => (
            <li className="netlink netlink--foreign" key={entry.name}>
              <div className="netlink__head">
                <span className="netlink__dot netlink__dot--foreign" aria-hidden="true" />
                <strong>{entry.name}</strong>
                <span className="netlink__to">{entry.address}</span>
                <span className="badge">面板外</span>
              </div>
            </li>
          ))}
        </ul>
      )}

      <Connector
        side="right"
        label={`从 ${proxy.name} 连一条线`}
        armed={armed}
        handlers={onConnector}
      />
    </article>
  )
}

function ServerCard({
  server,
  links,
  proxyOf,
  nodeRef,
  armed,
  target,
  onConnector,
  onOpenInstance,
}: {
  server: NetworkServer
  links: NetworkLink[]
  proxyOf: (id: string) => NetworkProxy | undefined
  busy: boolean
  nodeRef: (element: HTMLElement | null) => void
  armed: boolean
  target: boolean
  onConnector: ConnectorProps
  onOpenInstance: (id: string) => void
}) {
  return (
    <article
      className={`netcard netcard--server${target ? ' netcard--target' : ''}`}
      ref={nodeRef}
      data-node={`server:${server.id}`}
    >
      <Connector
        side="left"
        label={`把 ${server.name} 连到代理端`}
        armed={armed}
        handlers={onConnector}
      />

      <header className="netcard__head">
        <span className={`status__dot status__dot--${server.state}`} />
        <button className="netcard__name" type="button" onClick={() => onOpenInstance(server.id)}>
          {server.name}
        </button>
      </header>

      <p className="netcard__meta">
        <span title="子服地址">{server.address}</span>
        <span className="badge">{server.paper ? 'Paper 系' : 'Spigot 系'}</span>
        {server.onlineMode ? (
          <span className="badge">正版验证开</span>
        ) : (
          <span className="badge badge--warn">正版验证关</span>
        )}
      </p>

      {links.length === 0 ? (
        <p className="muted">独立运行，没挂在代理端后面。</p>
      ) : (
        <p className="muted">
          在{' '}
          {links
            .map((link) => `${proxyOf(link.proxyId)?.name ?? '代理端'} 上叫 ${link.name}`)
            .join('，')}
        </p>
      )}
    </article>
  )
}

/**
 * The same thing without the canvas.
 *
 * Not a degraded copy: on a phone the useful question is "what is behind this
 * proxy and is any of it broken", and that is a list. Connecting is a picker
 * and a button, which is also the path a keyboard takes on a wide screen.
 */
function NetworkList({
  data,
  all,
  focus,
  side,
  busy,
  onLink,
  onRepair,
  onUnlink,
  onOpenInstance,
}: ViewProps) {
  return (
    <div className="stack">
      {data.proxies.map((proxy) => {
        const links = all.links.filter((link) => link.proxyId === proxy.id)
        const linked = new Set(links.map((link) => link.serverId))
        // Against the cards on screen, not against the machine: from a server's
        // side the only thing this picker may offer is that server.
        const free = data.servers.filter((server) => !linked.has(server.id))
        return (
          <section className="panel" key={proxy.id}>
            <h3 className="panel__title">
              <span className={`status__dot status__dot--${proxy.state}`} /> {proxy.name}
            </h3>
            <p className="netcard__meta">
              <span>{proxy.bind || '0.0.0.0:25577'}</span>
              <span className={`badge${proxy.forwarding === 'none' ? ' badge--warn' : ''}`}>
                {forwardingLabel(proxy.forwarding)}
              </span>
            </p>

            {links.length === 0 ? (
              <p className="muted">还没有子服。玩家连上来会立刻被踢。</p>
            ) : (
              <ul className="netlinks">
                {links.map((link) => {
                  const server = all.servers.find((entry) => entry.id === link.serverId)
                  const mine = editable(link, focus, side)
                  return (
                    <li className={`netlink${mine ? '' : ' netlink--other'}`} key={link.name}>
                      <div className="netlink__head">
                        <span
                          className={`netlink__dot netlink__dot--${link.status}`}
                          aria-hidden="true"
                        />
                        <button
                          className="netcard__name"
                          type="button"
                          onClick={() => onOpenInstance(link.serverId)}
                        >
                          {server?.name ?? link.address}
                        </button>
                        <span className="netlink__to">{link.name}</span>
                      </div>
                      {mine && link.issues.length > 0 && (
                        <ul className="netlink__issues">
                          {link.issues.map((issue) => (
                            <li key={issue}>{issue}</li>
                          ))}
                        </ul>
                      )}
                      {mine && (
                        <div className="netlink__actions">
                          {link.status !== 'ok' && (
                            <button
                              className="btn btn--row"
                              type="button"
                              disabled={busy}
                              onClick={() => onRepair(proxy.id, link.serverId)}
                            >
                              修复
                            </button>
                          )}
                          <button
                            className="btn btn--row btn--danger"
                            type="button"
                            disabled={busy || !server}
                            onClick={() => server && onUnlink(proxy, server)}
                          >
                            断开
                          </button>
                        </div>
                      )}
                    </li>
                  )
                })}
              </ul>
            )}

            <AddServer proxy={proxy} servers={free} side={side} busy={busy} onLink={onLink} />
          </section>
        )
      })}
    </div>
  )
}

function AddServer({
  proxy,
  servers,
  side,
  busy,
  onLink,
}: {
  proxy: NetworkProxy
  servers: NetworkServer[]
  side: Side
  busy: boolean
  onLink: (proxyId: string, serverId: string) => void
}) {
  const [picked, setPicked] = useState('')
  if (servers.length === 0) {
    // The picker is empty for two different reasons and they are not the same
    // sentence: on the panel-wide page every server is already behind this
    // proxy, and from one server's side the only candidate there ever was is
    // that server, which is already connected.
    return (
      <p className="muted">
        {side === 'server' ? '这个服务端已经挂在它后面了。' : '这台机器上的服务端都已经连过来了。'}
      </p>
    )
  }
  return (
    <div className="netadd">
      <label className="field">
        <span>连一个服务端过来</span>
        <Select
          ariaLabel={`把哪个服务端连到 ${proxy.name}`}
          value={picked}
          options={[
            { value: '', label: '选一个…' },
            ...servers.map((server) => ({
              value: server.id,
              label: `${server.name} · ${server.address}`,
            })),
          ]}
          onChange={setPicked}
        />
      </label>
      <button
        className="btn btn--primary"
        type="button"
        disabled={busy || picked === ''}
        onClick={() => onLink(proxy.id, picked)}
      >
        连接
      </button>
    </div>
  )
}

function forwardingLabel(mode: string): string {
  switch (mode) {
    case 'modern':
      return 'modern 转发'
    case 'legacy':
      return 'legacy 转发'
    case 'bungeeguard':
      return 'bungeeguard 转发'
    default:
      return '未开转发'
  }
}
