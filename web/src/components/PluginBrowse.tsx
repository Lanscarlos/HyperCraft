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
import { PluginIcon } from './PluginIcon'
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
 * It still asks which servers, though, for a different reason: 兼容 is not a
 * property of a plugin. It is a property of a plugin and a game version and a
 * loader, so the rail's server ticks are what every badge on the page is
 * measured against — a lens, not a destination, and the rail says so.
 *
 * Wide rows, not a card grid. Everything that decides a plugin — supported
 * versions, loader, last updated, downloads, whether it is still maintained —
 * is text. A card spends its area on an icon that identifies nothing and then
 * truncates the one line worth reading; a row fits ten to twelve complete
 * answers on a screen.
 *
 * Two things here are deliberately *not* live:
 *
 *   - The filters. They used to fire a search on every tick, which is three
 *     upstream requests per click and a results list that reshuffles under the
 *     hand still moving through the rail. Narrowing a search is several
 *     decisions made together; it gets one button.
 *   - The first screen. An empty query used to be sent upstream as an empty
 *     query, and what comes back from Modrinth for that is the all-time
 *     download chart — Sodium, Iris, Lithium, client rendering mods a server
 *     will never load. There is a curated shelf for that case instead; see
 *     plugin/picks.go.
 */

/** How many results are shown before asking. A search is scanned, not read:
 *  ten is about what fits without scrolling, and the ones past it are almost
 *  never the answer — if they are, the query was wrong. */
const PAGE = 10

/** Everything the rail decides, as one value. Kept whole so "has anything
 *  changed since the last search" is one comparison rather than six. */
interface Filters {
  q: string
  sources: PluginSourceKind[]
  category: string
  sort: string
  onlyCompatible: boolean
  clientMods: boolean
  /** Which servers the badges are measured against. */
  against: string[]
}

const BLANK: Filters = {
  q: '',
  sources: [],
  category: '',
  sort: 'relevance',
  onlyCompatible: true,
  clientMods: false,
  against: [],
}

function same(a: Filters, b: Filters): boolean {
  return (
    a.q === b.q &&
    a.category === b.category &&
    a.sort === b.sort &&
    a.onlyCompatible === b.onlyCompatible &&
    a.clientMods === b.clientMods &&
    a.sources.join(',') === b.sources.join(',') &&
    a.against.join(',') === b.against.join(',')
  )
}

export function PluginBrowse({
  against,
  recents,
  onChooseAgainst,
  onOpenLibrary,
}: {
  /** The servers currently being judged against, from the URL. */
  against: string[]
  /** Most recently opened servers, newest first — where the default comes
   *  from. Nobody arrives at this page without a server in mind. */
  recents: string[]
  onChooseAgainst: (ids: string[]) => void
  /** Where a finished download went, so the page can point at it. */
  onOpenLibrary: () => void
}) {
  // What the rail is showing, and what was last searched. The gap between them
  // is the whole reason 搜索 is a button.
  const [draft, setDraft] = useState<Filters>({ ...BLANK, against })
  const [applied, setApplied] = useState<Filters>({ ...BLANK, against })

  const [result, setResult] = useState<PluginBrowseResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [open, setOpen] = useState<PluginListing | null>(null)
  const [cursor, setCursor] = useState(-1)
  const [shown, setShown] = useState(PAGE)
  const listRef = useRef<HTMLDivElement | null>(null)
  const box = useRef<HTMLInputElement | null>(null)

  const edit = (patch: Partial<Filters>) => setDraft((current) => ({ ...current, ...patch }))

  const search = useCallback(async (filters: Filters) => {
    setLoading(true)
    try {
      setResult(
        await api.browsePlugins({
          q: filters.q,
          sources: filters.sources,
          category: filters.category,
          sort: filters.sort,
          instances: filters.against,
          onlyCompatible: filters.onlyCompatible,
          clientMods: filters.clientMods,
        }),
      )
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '搜索失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    setShown(PAGE)
    void search(applied)
  }, [applied, search])

  const targets = result?.targets ?? []
  const chosen = targets.filter((target) => applied.against.includes(target.id))
  const listings = result?.listings ?? []
  const picks = result?.picks ?? []
  const allSources = result?.sources ?? []
  // An empty selection means "every source", which is what the rail shows and
  // what the API defaults to. Kept as empty rather than as a full list so the
  // filter summary can tell "全部" from "I happened to tick all three".
  const chosenSources = draft.sources.length === 0 ? allSources.map((entry) => entry.id) : draft.sources
  const dirty = !same(draft, applied)

  // The default nobody should have to set.
  //
  // Arriving here with nothing ticked used to mean a page of grey badges, and
  // the operator almost always did have a server in mind — they came from it.
  // So one gets ticked as soon as the target list is known: the server last
  // opened, or failing that whichever the panel lists first, because a
  // slightly arbitrary reference produces badges that can be read and checked
  // while no reference produces a page that cannot say anything at all. Runs
  // once — deliberately clearing every tick is an answer and must stick.
  const defaulted = useRef(false)
  useEffect(() => {
    if (defaulted.current || targets.length === 0) return
    defaulted.current = true
    if (applied.against.length > 0) return

    const pick =
      recents.find((id) => targets.some((target) => target.id === id)) ?? targets[0].id
    if (!pick) return
    setDraft((current) => ({ ...current, against: [pick] }))
    setApplied((current) => ({ ...current, against: [pick] }))
    onChooseAgainst([pick])
  }, [targets, recents, applied.against, onChooseAgainst])

  useEffect(() => {
    setCursor(-1)
  }, [listings])

  const apply = () => {
    if (!dirty) return
    setApplied(draft)
    // The reference servers are part of the page's address, so a link to these
    // results is a link to these badges.
    onChooseAgainst(draft.against)
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (listings.length === 0) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      setCursor((current) => {
        const next = event.key === 'ArrowDown' ? current + 1 : current - 1
        const clamped = Math.max(0, Math.min(Math.min(listings.length, shown) - 1, next))
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

  // `/` focuses the search box, the way it does in every other list on the
  // web. Ignored while something else already has the keyboard, or the first
  // slash of a path typed into a filter would be swallowed.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) return
      const active = document.activeElement
      if (active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement) return
      event.preventDefault()
      box.current?.focus()
      box.current?.select()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const reset = () => setDraft({ ...BLANK, against: draft.against })
  const filtered =
    draft.category !== '' ||
    draft.sources.length > 0 ||
    draft.sort !== 'relevance' ||
    draft.clientMods ||
    !draft.onlyCompatible

  return (
    <div className="browse" onKeyDown={onKeyDown}>
      <div className="browse__main">
        <form
          className="browse__search"
          onSubmit={(event) => {
            event.preventDefault()
            apply()
          }}
        >
          <input
            ref={box}
            className="filters__search"
            value={draft.q}
            placeholder="搜索插件名称，比如 EssentialsX、LuckPerms —— 按 / 直接聚焦"
            onChange={(event) => edit({ q: event.target.value })}
            aria-label="搜索插件"
          />
          <button className="btn btn--primary" type="submit" disabled={loading || !dirty}>
            {loading ? '搜索中…' : '搜索'}
          </button>
        </form>

        {/* The filters, as a row rather than a rail.

            They were a 260px sidebar, which at 1440px left the results — the
            thing the page is — with less than half the window, permanently, in
            exchange for six controls that get touched once a session. Up here
            they take one line, and the two that are actually adjusted often
            (which server to judge against, and whether to hide what will not
            run on it) are the two that are always visible. The rest are behind
            更多筛选, where a control you set once belongs. */}
        <div className="chips chips--filters">
          <ReferenceChip
            targets={targets}
            chosen={draft.against}
            onChoose={(ids) => edit({ against: ids })}
          />

          <SourceChip
            sources={allSources}
            chosen={chosenSources}
            query={draft.q}
            onToggle={(id) => {
              const next = chosenSources.includes(id)
                ? chosenSources.filter((entry) => entry !== id)
                : [...chosenSources, id]
              edit({ sources: next.length === allSources.length ? [] : next })
            }}
          />

          <button
            className={`chip${draft.onlyCompatible ? ' chip--on' : ''}`}
            aria-pressed={draft.onlyCompatible}
            disabled={draft.against.length === 0}
            title={
              draft.against.length === 0
                ? targets.length === 0
                  ? '面板里还没有实例，没有可比的服务器'
                  : '先选一台参照实例'
                : '滤掉加载器不对的 —— 那些在这台服上永远装不起来。游戏版本对不上的会留着并标黄。'
            }
            onClick={() => edit({ onlyCompatible: !draft.onlyCompatible })}
          >
            仅兼容
          </button>

          <MoreFilters
            draft={draft}
            categories={result?.categories ?? []}
            onEdit={edit}
          />

          {filtered && (
            <button className="link chips__reset" onClick={reset}>
              重置筛选
            </button>
          )}
        </div>

        {/* The filters are only what will be searched, not what is on screen,
            so the gap has to be visible or the chips read as broken. */}
        {dirty && !loading && (
          <p className="browse__pending">筛选条件改了，点「搜索」才会生效。</p>
        )}

        {error && <div className="alert alert--error">{error}</div>}

        {result?.notes &&
          Object.entries(result.notes).map(([source, note]) => (
            <div className="alert alert--warn" key={source}>
              {note}
            </div>
          ))}

        {picks.length > 0 ? (
          <PickShelf
            groups={picks}
            loading={loading}
            hasTargets={chosen.length > 0}
            onOpen={setOpen}
          />
        ) : (
          <>
            <p className="browse__count">
              {loading ? (
                '正在搜索…'
              ) : (
                <>
                  <span>
                    {listings.length} 个结果
                    {listings.length > shown && ` · 先显示前 ${shown} 个`}
                  </span>
                  {(result?.incompatible ?? 0) > 0 && chosen.length > 0 && (
                    <span className="browse__count-note">
                      · 其中 {result?.incompatible} 项不兼容，已降透明度保留
                    </span>
                  )}
                  {result?.truncated && (
                    <span className="browse__count-note">· 上游还有更多，缩小范围试试</span>
                  )}
                </>
              )}
            </p>

            <div className="browse__rows" ref={listRef} role="list">
              {listings.slice(0, shown).map((listing, index) => (
                <BrowseRow
                  key={`${listing.source}:${listing.id}`}
                  listing={listing}
                  focused={index === cursor}
                  judged={chosen.length > 0}
                  onOpen={() => setOpen(listing)}
                />
              ))}
            </div>

            {listings.length > shown && (
              <div className="browse__more">
                <button className="btn" onClick={() => setShown((count) => count + PAGE)}>
                  再显示 {Math.min(PAGE, listings.length - shown)} 个（共 {listings.length}）
                </button>
              </div>
            )}

            {!loading && listings.length === 0 && (
              <div className="welcome__empty">
                <p>没有搜到插件。</p>
                <p className="muted">下面三样通常有一样是原因：</p>
                <ul className="browse__advice">
                  <li>关键词太具体 —— 插件的名字往往和它做的事没什么关系，试试少打几个字。</li>
                  {draft.onlyCompatible && draft.against.length > 0 && (
                    <li>
                      「仅显示兼容项」把加载器不对的都滤掉了。
                      <button className="link" onClick={() => edit({ onlyCompatible: false })}>
                        关掉再搜一次
                      </button>
                    </li>
                  )}
                  {draft.sources.length > 0 && (
                    <li>
                      只勾了 {draft.sources.length} 个来源。
                      <button className="link" onClick={() => edit({ sources: [] })}>
                        全都搜
                      </button>
                    </li>
                  )}
                  <li>某个源没连上 —— 真是这样的话上面会写出来是哪个。</li>
                </ul>
              </div>
            )}
          </>
        )}
      </div>

      {open && (
        <PluginDrawer
          listing={open}
          against={applied.against}
          reference={chosen.length === 1 ? chosen[0] : null}
          onClose={() => setOpen(null)}
          onDownloaded={() => void search(applied)}
          onOpenLibrary={onOpenLibrary}
        />
      )}
    </div>
  )
}

/**
 * A chip that opens a small sheet of checkboxes.
 *
 * Written here rather than reusing Menu because Menu closes on selection,
 * which is right for an action list and wrong for a filter: ticking three
 * sources is one decision made in three clicks, and a sheet that shuts after
 * each one turns it into three decisions.
 */
function FilterChip({
  label,
  summary,
  on,
  disabled,
  title,
  children,
}: {
  label: string
  summary: string
  on?: boolean
  disabled?: boolean
  title?: string
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const wrap = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (event: MouseEvent) => {
      if (!wrap.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    window.addEventListener('pointerdown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('pointerdown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="menu" ref={wrap}>
      <button
        className={`chip${on ? ' chip--on' : ''}`}
        aria-haspopup="true"
        aria-expanded={open}
        disabled={disabled}
        title={title}
        onClick={() => setOpen(!open)}
      >
        <span className="chip__label">{label}</span>
        {summary}
        <span aria-hidden="true"> ▾</span>
      </button>
      {open && <div className="menu__sheet menu__sheet--filter">{children}</div>}
    </div>
  )
}

/**
 * Which servers the badges are measured against.
 *
 * Not a destination — nothing is installed anywhere from this page — but the
 * single most consequential control on it, because 兼容 is not a property of a
 * plugin. It is a property of a plugin, a game version and a loader, so with
 * nothing ticked there are no badges at all. Multi-select, because the question
 * an operator with four servers has is "does this fit my servers", and asking
 * it one server at a time meant four visits.
 */
function ReferenceChip({
  targets,
  chosen,
  onChoose,
}: {
  targets: InstallTarget[]
  chosen: string[]
  onChoose: (ids: string[]) => void
}) {
  if (targets.length === 0) return null

  const picked = targets.filter((target) => chosen.includes(target.id))
  const summary =
    picked.length === 0 ? '未选' : picked.length === 1 ? picked[0].name : `${picked.length} 台`

  return (
    <FilterChip
      label="参照"
      summary={summary}
      on={picked.length > 0}
      title="兼容性徽章按这几台服判断。只决定徽章怎么算，不决定装到哪。"
    >
      <p className="menu__note">兼容性徽章按这几台判断。下载到的是插件库，不会装进任何一台服。</p>
      {targets.map((target) => (
        <label className="menu__check" key={target.id}>
          <input
            type="checkbox"
            checked={chosen.includes(target.id)}
            onChange={() =>
              onChoose(
                chosen.includes(target.id)
                  ? chosen.filter((entry) => entry !== target.id)
                  : [...chosen, target.id],
              )
            }
          />
          <span>
            <span className={`status__dot status__dot--${target.state}`} />
            {target.name}
            {target.target.loader || target.target.mcVersion ? (
              <small>
                {loaderLabel(target.target.loader)} {target.target.mcVersion}
              </small>
            ) : (
              <small className="browse__check-warn">没认出这台服的核心和版本</small>
            )}
          </span>
        </label>
      ))}
    </FilterChip>
  )
}

function SourceChip({
  sources,
  chosen,
  query,
  onToggle,
}: {
  sources: { id: PluginSourceKind; name: string; note: string }[]
  chosen: PluginSourceKind[]
  query: string
  onToggle: (id: PluginSourceKind) => void
}) {
  if (sources.length === 0) return null
  const all = chosen.length === sources.length
  const summary = all ? '全部' : chosen.length === 1 ? sources.find((s) => s.id === chosen[0])?.name ?? '1 个' : `${chosen.length} 个`

  return (
    <FilterChip label="来源" summary={summary} on={!all}>
      {sources.map((source) => (
        <label className="menu__check" key={source.id}>
          <input type="checkbox" checked={chosen.includes(source.id)} onChange={() => onToggle(source.id)} />
          <span>
            {source.name}
            <small>{source.note}</small>
            {/* The constraint belongs to the source, so it is said where the
                source is, not as a banner over results it had nothing to do
                with. */}
            {source.id === 'spigot' && query.trim() === '' && chosen.includes(source.id) && (
              <small className="browse__check-warn">只能按关键词搜，不输名字它就不参与</small>
            )}
          </span>
        </label>
      ))}
    </FilterChip>
  )
}

/** The controls that get set once and then left. Category, sort order and
 *  whether client-side mods count — none of them worth a permanent 260px. */
function MoreFilters({
  draft,
  categories,
  onEdit,
}: {
  draft: Filters
  categories: { id: string; name: string }[]
  onEdit: (patch: Partial<Filters>) => void
}) {
  const changed = draft.category !== '' || draft.sort !== 'relevance' || draft.clientMods

  return (
    <FilterChip label="更多筛选" summary={changed ? '已调整' : ''} on={changed}>
      <label className="menu__field">
        <span>分类</span>
        <Select
          className="select--block"
          value={draft.category}
          ariaLabel="分类"
          options={[
            { value: '', label: '全部分类' },
            ...categories.map((entry) => ({ value: entry.id, label: entry.name })),
          ]}
          onChange={(next) => onEdit({ category: next })}
        />
      </label>

      <label className="menu__field">
        <span>排序</span>
        <Select
          className="select--block"
          value={draft.sort}
          ariaLabel="排序"
          options={[
            { value: 'relevance', label: '相关度' },
            { value: 'downloads', label: '下载量' },
            { value: 'updated', label: '最近更新' },
          ]}
          onChange={(next) => onEdit({ sort: next })}
        />
      </label>

      <label className="menu__check">
        <input
          type="checkbox"
          checked={draft.clientMods}
          onChange={(event) => onEdit({ clientMods: event.target.checked })}
        />
        <span>
          包含客户端模组
          <small>
            默认不含。Modrinth 的目录里大半是渲染、光影一类只跑在客户端的模组，装到服务端不会加载。
          </small>
        </span>
      </label>
    </FilterChip>
  )
}

/**
 * The first screen: a short shelf of what a server actually gets built out of.
 *
 * Grouped by the job rather than sorted by popularity, because "what should I
 * install" is a question about jobs — an operator who does not yet know they
 * need Vault is not going to find it in a download chart, and the chart is
 * where the client mods live.
 */
function PickShelf({
  groups,
  loading,
  hasTargets,
  onOpen,
}: {
  groups: { id: string; name: string; note?: string; listings: PluginListing[] }[]
  loading: boolean
  hasTargets: boolean
  onOpen: (listing: PluginListing) => void
}) {
  return (
    <div className="browse__shelf" aria-busy={loading}>
      <p className="browse__count">
        还没搜什么 —— 先放几个几乎每台服都会用到的。上面输入名字就是正常搜索。
      </p>

      {groups.map((group) => (
        <section className="browse__shelf-group" key={group.id}>
          <h3 className="browse__shelf-label">
            {group.name}
            {group.note && <small>{group.note}</small>}
          </h3>
          <div className="browse__rows" role="list">
            {group.listings.map((listing) => (
              <BrowseRow
                key={`${listing.source}:${listing.id}`}
                listing={listing}
                focused={false}
                judged={hasTargets}
                onOpen={() => onOpen(listing)}
              />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

/**
 * One result.
 *
 * Three lines, in the order the decision is made: is this the plugin and does
 * it fit, what does it do, is it alive and where does it come from. The
 * compatibility badge sits on the first line beside the name because it is the
 * one fact that can end the decision immediately — and when there is nothing to
 * judge against it is not there at all, so the name moves left into the space
 * rather than a whole column reading 未知兼容性 down the page.
 */
function BrowseRow({
  listing,
  focused,
  judged,
  onOpen,
}: {
  listing: PluginListing
  focused: boolean
  /** Whether any server was chosen to judge against. Decides between a missing
   *  badge and a line explaining why there is no verdict. */
  judged: boolean
  onOpen: () => void
}) {
  const bad = listing.compat?.state === 'bad'
  const stale = isStale(listing.updated)
  // Chosen a server, and the source still did not say enough to judge. That is
  // a fact about the source, so it goes in the meta line with the other facts
  // about the source rather than into the badge slot.
  const unjudgeable = judged && listing.compat?.state === 'unknown'

  return (
    <article
      className={`browse-row${bad ? ' browse-row--dim' : ''}${focused ? ' browse-row--focused' : ''}`}
      role="listitem"
    >
      <button className="browse-row__open" onClick={onOpen} title={`查看「${listing.name}」`}>
        <PluginIcon className="browse-row__icon" src={listing.iconUrl} name={listing.name} />

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
            {unjudgeable && (
              <span className="muted" title={listing.compat?.detail}>
                该来源未提供版本信息
              </span>
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
    case 'local':
      return '手动导入'
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
