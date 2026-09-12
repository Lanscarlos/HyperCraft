import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../api'
import { formatDate } from '../format'
import { useMediaQuery } from '../useMediaQuery'
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
 * 插件市场 — searching the registries, and downloading into the panel library.
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
 * A card grid with a filter rail down the left, rather than the wide rows this
 * page used to be. The rows did pack more answers onto a screen, and that is a
 * real thing to have given up; what they were bad at is the pass that comes
 * first. Scanning a registry is not reading ten results end to end — it is
 * finding the four or five worth reading at all, and a card gives that pass
 * something to catch on: the icon a project is actually known by, a name at a
 * size the eye can sweep, and two lines of what it does instead of one line
 * cut off mid-sentence. Every fact the rows carried is still here on the
 * card's meta line; what was traded away is density, deliberately.
 *
 * The filters are a rail again for the same reason. As chips they were four
 * closed sheets, and a closed sheet cannot say it is narrowing anything —
 * 分类 and 排序 in particular got set once and were invisible for the rest of
 * the session. Open down the left they are readable without a click, and the
 * results still get the width that matters, because a card stops improving at
 * about 320px whereas a row never stopped wanting more. What is actually in
 * force is also said above the grid now, as chips that come off one at a time.
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
 *  twelve is about what fits without scrolling, and it divides evenly by the
 *  two, three and four columns the grid actually lands on, so 再显示 never
 *  leaves a ragged last row. The ones past it are almost never the answer — if
 *  they are, the query was wrong. */
const PAGE = 12

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
          ?.querySelectorAll('.browse-card')
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

  // Taking a chip off above the results is not the same decision as setting the
  // rail. It is one narrowing, removed whole, and making it wait for 搜索 would
  // leave a chip on screen that no longer describes the list under it — so it
  // applies at once. Widening never costs the reader a list they were still
  // reading; it only gives them more of it.
  const drop = (patch: Partial<Filters>) => {
    const next = { ...applied, ...patch }
    setDraft((current) => ({ ...current, ...patch }))
    setApplied(next)
    if (patch.against) onChooseAgainst(next.against)
  }

  return (
    <div className="browse" onKeyDown={onKeyDown}>
      {/* The box and the button that fires it, across the top of both columns.
          The button is not a convenience — it is the page's contract: nothing
          goes upstream until it is pressed, and that holds for the rail too. */}
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

      <div className="browse__body">
        <FilterRail
          draft={draft}
          targets={targets}
          categories={result?.categories ?? []}
          sources={allSources}
          chosenSources={chosenSources}
          filtered={filtered}
          onEdit={edit}
          onReset={reset}
          onToggleSource={(id) => {
            const next = chosenSources.includes(id)
              ? chosenSources.filter((entry) => entry !== id)
              : [...chosenSources, id]
            edit({ sources: next.length === allSources.length ? [] : next })
          }}
        />

        <div className="browse__main">
          {/* The rail is only what *will* be searched, not what is on screen,
              so the gap has to be said or the rail reads as broken. */}
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
              <div className="browse__result-head">
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

                <AppliedChips
                  applied={applied}
                  targets={targets}
                  categories={result?.categories ?? []}
                  sources={allSources}
                  onDrop={drop}
                />
              </div>

              <div className="browse__grid" ref={listRef} role="list">
                {listings.slice(0, shown).map((listing, index) => (
                  <BrowseCard
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

/** Below this the rail has already wrapped onto its own full-width line, and a
 *  screen and a half of filters between the search box and the first result is
 *  not what someone who opened the market on a phone came for — so there it
 *  folds behind a button instead. Behaviour only: there is no matching rule in
 *  styles.css to keep in step, because above this width the fold is simply
 *  never drawn. */
const FOLD_QUERY = '(max-width: 780px)'

/** The sort orders, named once. The rail draws them and the applied-chips row
 *  has to be able to name the one in force. */
const SORTS = [
  { value: 'relevance', label: '相关度' },
  { value: 'downloads', label: '下载量' },
  { value: 'updated', label: '最近更新' },
]

/**
 * The filters, down the left.
 *
 * They were four chips over closed sheets, which cost one click to read and
 * gave nothing back when shut: 分类 and 排序 are set once and then decide every
 * result on the page, and a chip reading 更多筛选 cannot say which. Laid open
 * they are five lines of text that answer "what am I looking at" without being
 * touched, and the 参照 servers — the single most consequential control here,
 * because 兼容 is a property of a plugin *and* a game version *and* a loader —
 * are ticked in place rather than behind a sheet.
 *
 * Still nothing fires. The rail edits the draft; 搜索 is what goes upstream.
 */
function FilterRail({
  draft,
  targets,
  categories,
  sources,
  chosenSources,
  filtered,
  onEdit,
  onReset,
  onToggleSource,
}: {
  draft: Filters
  targets: InstallTarget[]
  categories: { id: string; name: string }[]
  sources: { id: PluginSourceKind; name: string; note: string }[]
  chosenSources: PluginSourceKind[]
  /** Whether anything is narrowed, which is when 重置筛选 is worth drawing. */
  filtered: boolean
  onEdit: (patch: Partial<Filters>) => void
  onReset: () => void
  onToggleSource: (id: PluginSourceKind) => void
}) {
  // Open is the only state a rail beside the results can be in — the toggle
  // exists solely for the folded case, so the preference is not remembered and
  // resizing back to a desktop cannot leave the filters hidden.
  const folded = useMediaQuery(FOLD_QUERY)
  const [shown, setShown] = useState(false)
  const open = !folded || shown

  return (
    <aside className="browse__rail" aria-label="筛选">
      {folded && (
        <button
          className="btn browse__rail-toggle"
          aria-expanded={open}
          onClick={() => setShown(!shown)}
        >
          {open ? '收起筛选' : '筛选'}
          {filtered && !open && <span className="browse__rail-dot" aria-hidden="true" />}
        </button>
      )}

      {open && (
        <>
          {categories.length > 0 && (
            <section className="browse__rail-card">
              <h3 className="browse__rail-label">分类</h3>
              <div className="browse__cats">
                <button
                  className={`browse__cat${draft.category === '' ? ' browse__cat--on' : ''}`}
                  aria-pressed={draft.category === ''}
                  onClick={() => onEdit({ category: '' })}
                >
                  全部分类
                </button>
                {categories.map((entry) => (
                  <button
                    key={entry.id}
                    className={`browse__cat${draft.category === entry.id ? ' browse__cat--on' : ''}`}
                    aria-pressed={draft.category === entry.id}
                    onClick={() => onEdit({ category: entry.id })}
                  >
                    {entry.name}
                  </button>
                ))}
              </div>
            </section>
          )}

          {targets.length > 0 && (
            <section className="browse__rail-card">
              <h3 className="browse__rail-label">参照实例</h3>
              <p className="browse__rail-note">
                兼容性徽章按这几台判断。下载到的是插件库，不会装进任何一台服。
              </p>
              <div className="browse__rail-list">
                {targets.map((target) => (
                  <label className="browse__check" key={target.id}>
                    <input
                      type="checkbox"
                      checked={draft.against.includes(target.id)}
                      onChange={() =>
                        onEdit({
                          against: draft.against.includes(target.id)
                            ? draft.against.filter((entry) => entry !== target.id)
                            : [...draft.against, target.id],
                        })
                      }
                    />
                    <span className="browse__check-body">
                      <span className="browse__check-name">
                        <span className={`status__dot status__dot--${target.state}`} />
                        {target.name}
                      </span>
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
              </div>

              <label className="browse__check">
                <input
                  type="checkbox"
                  checked={draft.onlyCompatible}
                  disabled={draft.against.length === 0}
                  onChange={() => onEdit({ onlyCompatible: !draft.onlyCompatible })}
                />
                <span className="browse__check-body">
                  <span className="browse__check-name">仅显示兼容项</span>
                  <small>
                    {draft.against.length === 0
                      ? '先勾一台参照实例，不然没有可比的对象。'
                      : '滤掉加载器不对的 —— 那些在这台服上永远装不起来。游戏版本对不上的会留着并标黄。'}
                  </small>
                </span>
              </label>
            </section>
          )}

          {sources.length > 0 && (
            <section className="browse__rail-card">
              <h3 className="browse__rail-label">来源</h3>
              <div className="browse__rail-chips">
                {sources.map((source) => (
                  <button
                    key={source.id}
                    className={`chip${chosenSources.includes(source.id) ? ' chip--on' : ''}`}
                    aria-pressed={chosenSources.includes(source.id)}
                    title={source.note}
                    onClick={() => onToggleSource(source.id)}
                  >
                    {source.name}
                  </button>
                ))}
              </div>
              {/* The constraint belongs to the source, so it is said where the
                  source is, not as a banner over results it had nothing to do
                  with. */}
              {chosenSources.includes('spigot') && draft.q.trim() === '' && (
                <p className="browse__rail-note browse__check-warn">
                  SpigotMC 只能按关键词搜，不输名字它就不参与。
                </p>
              )}
            </section>
          )}

          <section className="browse__rail-card">
            <h3 className="browse__rail-label">排序</h3>
            <Select
              className="select--block"
              value={draft.sort}
              ariaLabel="排序"
              options={SORTS}
              onChange={(next) => onEdit({ sort: next })}
            />

            <label className="browse__check">
              <input
                type="checkbox"
                checked={draft.clientMods}
                onChange={(event) => onEdit({ clientMods: event.target.checked })}
              />
              <span className="browse__check-body">
                <span className="browse__check-name">包含客户端模组</span>
                <small>
                  默认不含。Modrinth 的目录里大半是渲染、光影一类只跑在客户端的模组，装到服务端不会加载。
                </small>
              </span>
            </label>
          </section>

          {filtered && (
            <button className="link browse__rail-reset" onClick={onReset}>
              重置筛选
            </button>
          )}
        </>
      )}
    </aside>
  )
}

/**
 * What is actually in force, above the results it produced.
 *
 * The rail says what *will* be searched; this says what *was*, which is a
 * different sentence and the one that explains a short list. Only narrowings
 * appear — the things that can hide a plugin. 排序 and 包含客户端模组 are in the
 * rail, permanently visible now, and neither of them is why something is
 * missing.
 */
function AppliedChips({
  applied,
  targets,
  categories,
  sources,
  onDrop,
}: {
  applied: Filters
  targets: InstallTarget[]
  categories: { id: string; name: string }[]
  sources: { id: PluginSourceKind; name: string; note: string }[]
  onDrop: (patch: Partial<Filters>) => void
}) {
  const chips: { key: string; label?: string; value: string; drop: () => void }[] = []

  for (const id of applied.against) {
    const target = targets.find((entry) => entry.id === id)
    if (!target) continue
    chips.push({
      key: `against:${id}`,
      label: '参照',
      value: target.name,
      drop: () => onDrop({ against: applied.against.filter((entry) => entry !== id) }),
    })
  }

  if (applied.category !== '') {
    const name = categories.find((entry) => entry.id === applied.category)?.name ?? applied.category
    chips.push({ key: 'category', label: '分类', value: name, drop: () => onDrop({ category: '' }) })
  }

  if (applied.sources.length > 0) {
    chips.push({
      key: 'sources',
      label: '来源',
      value: applied.sources
        .map((id) => sources.find((entry) => entry.id === id)?.name ?? id)
        .join('、'),
      drop: () => onDrop({ sources: [] }),
    })
  }

  if (applied.onlyCompatible && applied.against.length > 0) {
    chips.push({
      key: 'compat',
      value: '仅兼容',
      drop: () => onDrop({ onlyCompatible: false }),
    })
  }

  if (chips.length === 0) return null

  return (
    <div className="browse__applied">
      {chips.map((chip) => (
        <button
          key={chip.key}
          className="chip chip--on browse__applied-chip"
          title={`不再按「${chip.value}」筛选`}
          onClick={chip.drop}
        >
          {chip.label && <span className="chip__label">{chip.label}</span>}
          {chip.value}
          <span className="browse__applied-x" aria-hidden="true">
            ×
          </span>
        </button>
      ))}

      {chips.length > 1 && (
        <button
          className="link browse__applied-reset"
          onClick={() => onDrop({ category: '', sources: [], onlyCompatible: false })}
        >
          清除
        </button>
      )}
    </div>
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
          <div className="browse__grid" role="list">
            {group.listings.map((listing) => (
              <BrowseCard
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
 * Read top to bottom in the order the decision is made: is this the plugin and
 * does it fit, what does it do, is it alive and where did it come from, and
 * only then the thing to press. The compatibility badge sits on the first line
 * beside the name because it is the one fact that can end the decision
 * immediately — and when there is nothing to judge against it is not drawn at
 * all, so the name keeps the width rather than a whole column reading
 * 未知兼容性 down the page.
 *
 * The summary gets two lines and reserves both whether or not it fills them.
 * A clamp that collapses to one line on short descriptions puts every card's
 * footer at a different height, and a grid of cards whose buttons do not line
 * up is read as a grid of cards that are not the same kind of thing.
 */
function BrowseCard({
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
      className={`browse-card${bad ? ' browse-card--dim' : ''}${focused ? ' browse-card--focused' : ''}`}
      role="listitem"
    >
      <button className="browse-card__open" onClick={onOpen} title={`查看「${listing.name}」`}>
        <span className="browse-card__head">
          <PluginIcon className="browse-card__icon" src={listing.iconUrl} name={listing.name} />
          <span className="browse-card__ident">
            <span className="browse-card__name">
              <strong>{listing.name}</strong>
              <CompatBadge compat={listing.compat} />
            </span>
            {listing.author && <span className="browse-card__author">{listing.author}</span>}
          </span>
        </span>

        <span className="browse-card__summary">{listing.summary || '这个插件没有写简介。'}</span>

        <span className="browse-card__facts">
          <span>{formatDownloads(listing.downloads)} 次下载</span>
          <span className={stale ? 'browse-card__stale' : undefined}>
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
      </button>

      <div className="browse-card__action">
        {listing.downloadable ? (
          <button className={bad ? 'btn' : 'btn btn--primary'} disabled={bad} onClick={onOpen}>
            {bad ? '不兼容' : '下载'}
          </button>
        ) : (
          <a className="btn" href={listing.pageUrl} target="_blank" rel="noreferrer">
            前往源站
          </a>
        )}
        <span className="badge browse-card__source">{sourceLabel(listing.source)}</span>
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
