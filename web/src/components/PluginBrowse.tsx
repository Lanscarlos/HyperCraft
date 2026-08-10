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
import { Select } from './Select'

/**
 * 获取插件 — searching the registries, and downloading into the panel library.
 *
 * This page acquires; it does not deploy. What it produces is a jar in the
 * shared library, which is then handed to as many servers as the operator
 * wants from the plugin list or from a server's own page. Keeping the two apart
 * is what makes "the same plugin on five servers" one file with one checksum
 * instead of five downloads nobody can tell apart, and it is why this page has
 * no 安装到 — there is nothing to install to from here.
 *
 * It still asks which server, though, for a different reason: 兼容 is not a
 * property of a plugin. It is a property of a plugin and a game version and a
 * loader, so without a server to compare against every badge on the page reads
 * 未知兼容性 and the whole column is decoration. The chosen server is a lens,
 * not a destination, and the rail says so.
 *
 * Wide rows, not a card grid. Everything that decides a plugin — supported
 * versions, loader, last updated, downloads, whether it is still maintained —
 * is text. A card spends its area on an icon that identifies nothing and then
 * truncates the one line worth reading; a row fits six to eight complete
 * answers on a screen.
 */
export function PluginBrowse({
  against,
  onChooseAgainst,
  onOpenLibrary,
}: {
  /** The server currently being judged against, or "" for none. */
  against: string
  onChooseAgainst: (id: string) => void
  /** Where a finished download went, so the page can point at it. */
  onOpenLibrary: () => void
}) {
  const [text, setText] = useState('')
  const [query, setQuery] = useState('')
  const [sources, setSources] = useState<PluginSourceKind[]>([])
  const [category, setCategory] = useState('')
  const [sort, setSort] = useState('relevance')
  const [onlyCompatible, setOnlyCompatible] = useState(true)

  const [result, setResult] = useState<PluginBrowseResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [open, setOpen] = useState<PluginListing | null>(null)
  const [cursor, setCursor] = useState(-1)
  const listRef = useRef<HTMLDivElement | null>(null)

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
          instance: against,
          onlyCompatible,
        }),
      )
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '搜索失败')
    } finally {
      setLoading(false)
    }
  }, [query, sources, category, sort, against, onlyCompatible])

  useEffect(() => {
    void search()
  }, [search])

  // The servers come from the search response rather than from the caller: the
  // game version and loader are read off each server's own directory, and the
  // rail must show exactly what the badges were computed from. A list assembled
  // in the browser would be a second opinion nobody asked for.
  const targets = result?.targets ?? []
  const reference = targets.find((target) => target.id === against) ?? null
  const listings = result?.listings ?? []
  const allSources = result?.sources ?? []
  // An empty selection means "every source", which is what the rail shows and
  // what the API defaults to. Kept as empty rather than as a full list so the
  // filter summary can tell "全部" from "I happened to tick all three".
  const chosenSources = sources.length === 0 ? allSources.map((entry) => entry.id) : sources
  const filtered = category !== '' || sources.length > 0 || sort !== 'relevance' || !onlyCompatible

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

  const reset = () => {
    setSources([])
    setCategory('')
    setSort('relevance')
    setOnlyCompatible(true)
  }

  return (
    <div className="browse" onKeyDown={onKeyDown}>
      <aside className="browse__rail">
        <ReferenceBlock targets={targets} against={against} onChoose={onChooseAgainst} />

        <div className="browse__filters">
          <div className="browse__filters-head">
            <h3 className="browse__group-label">筛选</h3>
            {filtered && (
              <button className="link" onClick={reset}>
                重置
              </button>
            )}
          </div>

          <RailGroup label="来源">
            <div className="browse__checks">
              {allSources.map((source) => (
                <label className="browse__check" key={source.id} title={source.note}>
                  <input
                    type="checkbox"
                    checked={chosenSources.includes(source.id)}
                    onChange={() => {
                      const next = chosenSources.includes(source.id)
                        ? chosenSources.filter((entry) => entry !== source.id)
                        : [...chosenSources, source.id]
                      setSources(next.length === allSources.length ? [] : next)
                    }}
                  />
                  <span className="browse__check-body">
                    <span>{source.name}</span>
                    <small>{source.note}</small>
                  </span>
                </label>
              ))}
            </div>
          </RailGroup>

          <RailGroup label="分类">
            <Select
              className="select--block"
              value={category}
              ariaLabel="分类"
              options={[
                { value: '', label: '全部分类' },
                ...(result?.categories ?? []).map((entry) => ({
                  value: entry.id,
                  label: entry.name,
                })),
              ]}
              onChange={setCategory}
            />
          </RailGroup>

          <RailGroup label="排序">
            <Select
              className="select--block"
              value={sort}
              ariaLabel="排序"
              options={[
                { value: 'relevance', label: '相关度' },
                { value: 'downloads', label: '下载量' },
                { value: 'updated', label: '最近更新' },
              ]}
              onChange={setSort}
            />
          </RailGroup>

          <RailGroup label="兼容性">
            <label className="browse__check">
              <input
                type="checkbox"
                checked={onlyCompatible}
                onChange={(event) => setOnlyCompatible(event.target.checked)}
                disabled={!reference}
              />
              <span className="browse__check-body">
                <span>仅显示兼容项</span>
                <small>
                  只滤掉加载器不对的 —— 那些在这台服上永远装不起来。游戏版本对不上的会留着并标黄。
                </small>
              </span>
            </label>
          </RailGroup>
        </div>
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
              {(result?.incompatible ?? 0) > 0 && reference?.target.mcVersion && (
                <span className="browse__count-note">
                  · 其中 {result?.incompatible} 项不兼容 {reference.target.mcVersion}，已降透明度保留
                </span>
              )}
              {result?.truncated && (
                <span className="browse__count-note">· 还有更多，缩小范围试试</span>
              )}
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
          against={against}
          reference={reference}
          onClose={() => setOpen(null)}
          onDownloaded={() => void search()}
          onOpenLibrary={onOpenLibrary}
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
 * Which server the badges are measured against.
 *
 * Pinned above the filters and given the accent surface because it is not one
 * filter among four: the other three narrow the list, this one decides what
 * every compatibility badge in it means. Leaving it out is allowed and says
 * what it costs, rather than silently producing a page of grey badges nobody
 * can interpret.
 */
function ReferenceBlock({
  targets,
  against,
  onChoose,
}: {
  targets: InstallTarget[]
  against: string
  onChoose: (id: string) => void
}) {
  const chosen = targets.find((target) => target.id === against) ?? null

  return (
    <section className="browse__reference">
      <h3 className="browse__group-label">按哪台服判断兼容性</h3>
      <Select
        className="select--block"
        value={against}
        ariaLabel="按哪台服判断兼容性"
        options={[
          { value: '', label: '不判断' },
          ...targets.map((target) => ({ value: target.id, label: target.name })),
        ]}
        onChange={onChoose}
      />

      {chosen ? (
        <p className="browse__reference-meta">
          {chosen.target.loader || chosen.target.mcVersion ? (
            <>
              <span className={`status__dot status__dot--${chosen.state}`} />
              {loaderLabel(chosen.target.loader)} {chosen.target.mcVersion}
            </>
          ) : (
            <span className="browse__reference-warn">
              没认出这台服的核心和版本，判断不了兼容性
            </span>
          )}
        </p>
      ) : (
        <p className="browse__reference-meta">
          <span className="browse__reference-warn">不选就全是「未知兼容性」</span>
        </p>
      )}

      <p className="browse__hint">
        这里只决定徽章按谁算。下载到的是面板插件库，不会装进任何一台服。
      </p>
    </section>
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
            {bad ? '不兼容' : '下载'}
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
