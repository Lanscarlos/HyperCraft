import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../api'
import { formatDate } from '../format'
import type {
  InstallTarget,
  PluginBrowseResult,
  PluginListing,
  PluginSourceKind,
} from '../types'
import { CompatBadge } from './PluginCompat'
import { PluginDrawer } from './PluginDrawer'

/**
 * 获取插件 — one component, two entrances.
 *
 * From a server, 安装到 is already answered and every badge on the page is
 * judged against that server's game version and loader. From the panel it is
 * the first thing asked, because nothing here means anything without it: "兼容"
 * is not a property of a plugin, it is a property of a plugin and a server.
 *
 * Why this is not only on the panel-wide page: nobody decides to install a
 * plugin in the abstract. The thought arrives while looking at one server —
 * someone asked for /home, the economy broke, the map needs a renderer — and a
 * panel that answers it by sending you somewhere else has put a navigation step
 * between the intent and the action, at the exact moment the operator had all
 * the context.
 *
 * Wide rows, not a card grid. Everything that decides a plugin — supported
 * versions, loader, last updated, downloads, whether it is still maintained —
 * is text. A card spends its area on an icon that identifies nothing and then
 * truncates the one line worth reading; a row fits six to eight complete
 * answers on a screen.
 */
export function PluginBrowse({
  /** The instance this opened from, when it opened from one. */
  instanceId,
  onOpenInstance,
}: {
  instanceId?: string
  onOpenInstance?: (id: string) => void
}) {
  const [text, setText] = useState('')
  const [query, setQuery] = useState('')
  const [sources, setSources] = useState<PluginSourceKind[]>([])
  const [category, setCategory] = useState('')
  const [sort, setSort] = useState('relevance')
  const [onlyCompatible, setOnlyCompatible] = useState(true)
  // The panel-scope entry can install into several servers at once; the
  // instance-scope entry arrives with exactly one and cannot change it.
  const [selected, setSelected] = useState<string[]>(instanceId ? [instanceId] : [])

  const [result, setResult] = useState<PluginBrowseResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [open, setOpen] = useState<PluginListing | null>(null)
  const [cursor, setCursor] = useState(-1)
  const listRef = useRef<HTMLDivElement | null>(null)

  // The instance whose version and loader every badge is judged against. With
  // several selected it is the first — a compatibility badge can only speak
  // about one server, and the confirmation names the rest.
  const judgedAgainst = instanceId ?? selected[0] ?? ''

  useEffect(() => {
    // Typing must not fire a request per keystroke at three registries.
    const timer = window.setTimeout(() => setQuery(text.trim()), 320)
    return () => window.clearTimeout(timer)
  }, [text])

  const search = useCallback(async () => {
    setLoading(true)
    try {
      setResult(
        await api.browsePlugins({
          q: query,
          sources,
          category,
          sort,
          instance: judgedAgainst,
          onlyCompatible,
        }),
      )
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '搜索失败')
    } finally {
      setLoading(false)
    }
  }, [query, sources, category, sort, judgedAgainst, onlyCompatible])

  useEffect(() => {
    void search()
  }, [search])

  const targets = result?.targets ?? []
  const context = targets.find((target) => target.id === judgedAgainst) ?? null

  // Incompatible rows stay, dimmed. A plugin that has not been updated since
  // 1.16.5 is exactly what someone searching for it needs to be told; hiding
  // it produces a search that returns nothing and reads as a broken panel.
  const listings = result?.listings ?? []

  useEffect(() => {
    setCursor(-1)
  }, [listings])

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (listings.length === 0) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      setCursor((current) => {
        const next = event.key === 'ArrowDown' ? current + 1 : current - 1
        const clamped = Math.max(0, Math.min(listings.length - 1, next))
        listRef.current
          ?.querySelectorAll('.browse-row')
          [clamped]?.scrollIntoView({ block: 'nearest' })
        return clamped
      })
      return
    }
    if (event.key === 'Enter' && cursor >= 0) {
      event.preventDefault()
      setOpen(listings[cursor])
    }
  }

  return (
    <div className="browse" onKeyDown={onKeyDown}>
      <aside className="browse__rail">
        <TargetBlock
          targets={targets}
          selected={selected}
          locked={Boolean(instanceId)}
          onToggle={(id) =>
            setSelected((current) =>
              current.includes(id) ? current.filter((entry) => entry !== id) : [...current, id],
            )
          }
        />

        <RailGroup label="来源">
          {(result?.sources ?? []).map((source) => (
            <label className="browse__check" key={source.id} title={source.note}>
              <input
                type="checkbox"
                checked={sources.length === 0 || sources.includes(source.id)}
                onChange={() =>
                  setSources((current) => {
                    // An empty list means "everything", so the first uncheck
                    // has to expand it before removing one.
                    const base =
                      current.length === 0
                        ? (result?.sources ?? []).map((entry) => entry.id)
                        : current
                    const next = base.includes(source.id)
                      ? base.filter((entry) => entry !== source.id)
                      : [...base, source.id]
                    return next.length === (result?.sources ?? []).length ? [] : next
                  })
                }
              />
              <span>{source.name}</span>
            </label>
          ))}
        </RailGroup>

        <RailGroup label="分类">
          <select
            className="input-slim browse__select"
            value={category}
            onChange={(event) => setCategory(event.target.value)}
            aria-label="分类"
          >
            <option value="">全部分类</option>
            {(result?.categories ?? []).map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.name}
              </option>
            ))}
          </select>
        </RailGroup>

        <RailGroup label="排序">
          <select
            className="input-slim browse__select"
            value={sort}
            onChange={(event) => setSort(event.target.value)}
            aria-label="排序"
          >
            <option value="relevance">相关度</option>
            <option value="downloads">下载量</option>
            <option value="updated">最近更新</option>
          </select>
        </RailGroup>

        <label className="browse__check browse__check--switch">
          <input
            type="checkbox"
            checked={onlyCompatible}
            onChange={(event) => setOnlyCompatible(event.target.checked)}
          />
          <span>仅显示兼容项</span>
        </label>
        <p className="browse__hint">
          只过滤加载器不对的插件 —— 那些在这台服上永远装不起来。游戏版本对不上的会留着并标黄，
          「三年没更新、只支持到 1.16.5」本身就是你要的答案。
        </p>
      </aside>

      <div className="browse__main">
        <div className="browse__search">
          <input
            className="filters__search"
            value={text}
            placeholder="搜索插件名称，比如 EssentialsX、LuckPerms"
            onChange={(event) => setText(event.target.value)}
            aria-label="搜索插件"
          />
        </div>

        {error && <div className="alert alert--error">{error}</div>}

        <p className="browse__count">
          {loading ? (
            '正在搜索…'
          ) : (
            <>
              <span>{listings.length} 个结果</span>
              {(result?.incompatible ?? 0) > 0 && context?.target.mcVersion && (
                <span className="browse__count-note">
                  · 其中 {result?.incompatible} 项不兼容 {context.target.mcVersion}，已降透明度保留
                </span>
              )}
              {result?.truncated && <span className="browse__count-note">· 还有更多，缩小范围试试</span>}
            </>
          )}
        </p>

        {result?.notes &&
          Object.entries(result.notes).map(([source, note]) => (
            <div className="alert alert--warn" key={source}>
              {note}
            </div>
          ))}

        <div className="browse__rows" ref={listRef} role="list">
          {listings.map((listing, index) => (
            <BrowseRow
              key={`${listing.source}:${listing.id}`}
              listing={listing}
              focused={index === cursor}
              onOpen={() => setOpen(listing)}
            />
          ))}
        </div>

        {!loading && listings.length === 0 && (
          <div className="welcome__empty">
            <p>没有搜到插件。</p>
            <p className="muted">
              换个关键词，或者把「仅显示兼容项」关掉看看 —— 也可能是某个源没连上，上面会写。
            </p>
          </div>
        )}
      </div>

      {open && (
        <PluginDrawer
          listing={open}
          instanceId={judgedAgainst}
          targets={targets.filter((target) => selected.includes(target.id))}
          onClose={() => setOpen(null)}
          onInstalled={() => {
            setOpen(null)
            void search()
          }}
          onOpenInstance={onOpenInstance}
        />
      )}
    </div>
  )
}

function RailGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="browse__group">
      <h3 className="browse__group-label">{label}</h3>
      {children}
    </section>
  )
}

/**
 * 安装到 — the filter every other filter is relative to.
 *
 * Pinned to the top and given the most contrast on the rail because it is not
 * one filter among five: it decides what "兼容" means for every row below it.
 * A discovery page whose target is a dropdown somewhere in the middle is one
 * where the badges are read without anyone knowing what they were compared to.
 */
function TargetBlock({
  targets,
  selected,
  locked,
  onToggle,
}: {
  targets: InstallTarget[]
  selected: string[]
  locked: boolean
  onToggle: (id: string) => void
}) {
  const chosen = targets.filter((target) => selected.includes(target.id))

  return (
    <section className="browse__target">
      <h3 className="browse__group-label">安装到</h3>

      {locked && chosen.length === 1 ? (
        <TargetLine target={chosen[0]} />
      ) : (
        <>
          {chosen.length === 0 && (
            <p className="browse__target-empty">
              先选一台服务器 —— 不知道装到哪，就没法判断插件兼不兼容。
            </p>
          )}
          <div className="browse__target-list">
            {targets.map((target) => (
              <label className="browse__check" key={target.id}>
                <input
                  type="checkbox"
                  checked={selected.includes(target.id)}
                  onChange={() => onToggle(target.id)}
                />
                <TargetLine target={target} />
              </label>
            ))}
          </div>
        </>
      )}
    </section>
  )
}

function TargetLine({ target }: { target: InstallTarget }) {
  const { loader, mcVersion } = target.target
  return (
    <span className="browse__target-line">
      <span className="browse__target-name">
        <span className={`status__dot status__dot--${target.state}`} />
        {target.name}
      </span>
      <small>
        {loader || mcVersion ? (
          <>
            {loaderLabel(loader)} {mcVersion}
          </>
        ) : (
          <span className="muted">核心和版本未知</span>
        )}
      </small>
    </span>
  )
}

/**
 * One result.
 *
 * Three lines, in the order the decision is made: is this the plugin and does
 * it fit, what does it do, is it alive and where does it come from. The
 * compatibility badge sits on the first line beside the name because it is the
 * one fact that can end the decision immediately.
 */
function BrowseRow({
  listing,
  focused,
  onOpen,
}: {
  listing: PluginListing
  focused: boolean
  onOpen: () => void
}) {
  const bad = listing.compat?.state === 'bad'
  const stale = isStale(listing.updated)
  // A registry icon is a request to a third-party CDN, which is exactly the
  // host an operator behind a restrictive network cannot reach. A broken image
  // placeholder is worse than no icon, so a failed load falls back to the
  // initial the row would have had anyway.
  const [iconBroken, setIconBroken] = useState(false)
  const icon = listing.iconUrl && !iconBroken

  return (
    <article
      className={`browse-row${bad ? ' browse-row--dim' : ''}${focused ? ' browse-row--focused' : ''}`}
      role="listitem"
    >
      <button className="browse-row__open" onClick={onOpen} title={`查看「${listing.name}」`}>
        {icon ? (
          <img
            className="browse-row__icon"
            src={listing.iconUrl}
            alt=""
            loading="lazy"
            onError={() => setIconBroken(true)}
          />
        ) : (
          <span className="browse-row__icon browse-row__icon--blank">
            {listing.name.slice(0, 1).toUpperCase()}
          </span>
        )}

        <span className="browse-row__body">
          <span className="browse-row__head">
            <strong>{listing.name}</strong>
            {listing.author && <span className="browse-row__author">{listing.author}</span>}
            <CompatBadge compat={listing.compat} />
            <span className="badge">{sourceLabel(listing.source)}</span>
          </span>

          <span className="browse-row__summary">{listing.summary || '这个插件没有写简介。'}</span>

          <span className="browse-row__facts">
            <span>{formatDownloads(listing.downloads)} 次下载</span>
            <span className={stale ? 'browse-row__stale' : undefined}>
              {listing.updated ? `更新于 ${formatDate(listing.updated)}` : '更新时间未知'}
              {stale && ' · 疑似停维'}
            </span>
            {listing.loaders?.length ? (
              <span>{listing.loaders.map(loaderLabel).join(' / ')}</span>
            ) : (
              <span className="muted">未说明加载器</span>
            )}
          </span>
        </span>
      </button>

      <div className="browse-row__action">
        {listing.downloadable ? (
          <button className={bad ? 'btn' : 'btn btn--primary'} disabled={bad} onClick={onOpen}>
            {bad ? '不兼容' : '安装'}
          </button>
        ) : (
          <a className="btn" href={listing.pageUrl} target="_blank" rel="noreferrer">
            前往源站
          </a>
        )}
      </div>
    </article>
  )
}

/** Three years with no release. Not a judgement the panel makes on its own —
 *  it is said next to the date it was read from. */
function isStale(updated?: string): boolean {
  if (!updated) return false
  const when = new Date(updated).getTime()
  if (Number.isNaN(when)) return false
  return Date.now() - when > 3 * 365 * 24 * 60 * 60 * 1000
}

export function formatDownloads(count: number): string {
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`
  if (count >= 1_000) return `${(count / 1_000).toFixed(1)}K`
  return String(count)
}

export function sourceLabel(source: PluginSourceKind): string {
  switch (source) {
    case 'modrinth':
      return 'Modrinth'
    case 'hangar':
      return 'Hangar'
    case 'spigot':
      return 'SpigotMC'
    default:
      return 'GitHub'
  }
}

export function loaderLabel(loader?: string): string {
  switch (loader) {
    case 'paper':
      return 'Paper'
    case 'purpur':
      return 'Purpur'
    case 'folia':
      return 'Folia'
    case 'spigot':
      return 'Spigot'
    case 'bukkit':
      return 'Bukkit'
    case 'velocity':
      return 'Velocity'
    case 'bungeecord':
      return 'BungeeCord'
    case 'waterfall':
      return 'Waterfall'
    case 'fabric':
      return 'Fabric'
    case 'quilt':
      return 'Quilt'
    case 'forge':
      return 'Forge'
    case 'neoforge':
      return 'NeoForge'
    default:
      return loader ?? ''
  }
}

