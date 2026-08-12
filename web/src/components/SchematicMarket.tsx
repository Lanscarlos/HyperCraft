import { useCallback, useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import { formatBytes, formatSince } from '../format'
import { toast } from '../toast'
import type { SchematicItem, SchematicMarketResult, SchematicSource } from '../types'
import type { SchematicController } from '../useSchematics'
import { Page } from './Page'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/**
 * 建筑市场 and 索引源: where builds come from when they do not come from disk.
 *
 * There is no registry for schematics the way there is for plugins. Modrinth,
 * Hangar and SpigotMC publish jars and none of them publish builds; the sites
 * that do have no API to read. So this market is federated rather than central
 * — a source is either a JSON index somebody publishes or a GitHub repository
 * with .schem files in it — which is how builds are actually shared today, and
 * which means a server group can point their panels at their own index and
 * have their own builds sitting in the market next to everything else.
 *
 * Nothing downloads to a server. 入库 puts a build in the panel's library, and
 * installing it onto a server stays a separate, deliberate act on 建筑列表 —
 * the same separation the plugin market keeps, for the same reason.
 */
export function SchematicMarket({
  schematics,
  view,
  onOpenView,
}: {
  schematics: SchematicController
  /** 'browse' is the market, 'source' is the list of places it reads. */
  view: 'browse' | 'source'
  onOpenView: (view: 'browse' | 'source') => void
}) {
  const [result, setResult] = useState<SchematicMarketResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [source, setSource] = useState('')
  const [taking, setTaking] = useState<string | null>(null)

  const read = useCallback(
    async (refresh: boolean) => {
      setLoading(true)
      try {
        setResult(await api.browseSchematics('', '', refresh))
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : '读取建筑市场失败')
      } finally {
        setLoading(false)
      }
    },
    [],
  )

  useEffect(() => {
    void read(false)
  }, [read])

  // Filtered here rather than by asking again: a catalogue is a file and a
  // repository listing, so the whole of it is already in hand — and a request
  // per keystroke would be one per keystroke to somebody else's host.
  const items = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const all = result?.items ?? []
    return all.filter((item) => {
      if (source !== '' && item.sourceId !== source) return false
      if (needle === '') return true
      return [item.name, item.description ?? '', item.author ?? '', item.fileName]
        .concat(item.tags ?? [])
        .some((value) => value.toLowerCase().includes(needle))
    })
  }, [result, query, source])

  const take = async (item: SchematicItem) => {
    setTaking(item.id)
    try {
      const entry = await api.installMarketSchematic(item.sourceId, item.id)
      toast(`已入库：${entry.name}`)
      await schematics.refresh()
      // Marked as held without another round trip to the sources.
      setResult((prev) =>
        prev ? { ...prev, held: { ...(prev.held ?? {}), [item.id]: entry.id } } : prev,
      )
    } catch (err) {
      toast(err instanceof Error ? err.message : '下载失败')
    } finally {
      setTaking(null)
    }
  }

  if (view === 'source') {
    return (
      <SourcesPage
        sources={result?.sources ?? []}
        notes={result?.notes ?? {}}
        onChanged={() => void read(true)}
        onOpenMarket={() => onOpenView('browse')}
      />
    )
  }

  if (loading && result === null) {
    return (
      <SkeletonScreen label="正在读取建筑市场…">
        <SkeletonPanel lines={2} />
        <SkeletonPanel title={false}>
          <Skeleton w="100%" h={120} />
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  const enabled = (result?.sources ?? []).filter((entry) => !entry.disabled)
  const notes = Object.entries(result?.notes ?? {})

  return (
    <Page
      wide
      title="建筑市场"
      lead="从索引源和 GitHub 仓库里找建筑，一键下到建筑库。下载走服务器自己的网络，文件在存进库之前会先解析一遍——读不出来的直接不收。"
      aside={
        <p className="meta-chips">
          <span>{`${enabled.length} 个源`}</span>
          <span>{result ? `${result.total} 个建筑` : '还没读到'}</span>
          {result?.fetchedAt && <span>更新于 {formatSince(result.fetchedAt)}</span>}
        </p>
      }
    >
      {error && <div className="alert alert--error">{error}</div>}

      <section className="panel">
        <div className="schemlib__bar">
          <input
            className="filters__search"
            type="search"
            value={query}
            placeholder="搜名称、简介、标签"
            aria-label="搜索建筑市场"
            onChange={(event) => setQuery(event.target.value)}
          />
          <div className="schemlib__bar-actions">
            <Select
              ariaLabel="按来源筛选"
              value={source}
              onChange={setSource}
              options={[
                { value: '', label: '全部来源' },
                ...enabled.map((entry) => ({ value: entry.id, label: entry.name })),
              ]}
            />
            <button className="btn" onClick={() => void read(true)} disabled={loading}>
              {loading ? '刷新中…' : '刷新'}
            </button>
            <button className="btn" onClick={() => onOpenView('source')}>
              管理索引源
            </button>
          </div>
        </div>

        {/* One source failing is not the market failing: the others answered,
            and saying so under the results is the difference between "this
            index moved" and "建筑市场坏了". */}
        {notes.map(([id, note]) => (
          <div className="alert alert--warn" key={id}>
            {result?.sources.find((entry) => entry.id === id)?.name ?? id}：{note}
          </div>
        ))}

        {items.length === 0 ? (
          <div className="welcome__empty">
            <p>{result && result.total > 0 ? '没有匹配的建筑。' : '这些源里还没有建筑。'}</p>
            <p className="muted">
              建筑市场读的是你添加的源。
              <button className="link" type="button" onClick={() => onOpenView('source')}>
                加一个 GitHub 仓库或索引地址
              </button>
              ，仓库里的每个 .schem 都会出现在这里。
            </p>
          </div>
        ) : (
          <div className="schemlib__grid" role="list">
            {items.map((item) => (
              <MarketCard
                key={`${item.sourceId}:${item.id}`}
                item={item}
                held={Boolean(result?.held?.[item.id])}
                busy={taking === item.id}
                onTake={() => void take(item)}
              />
            ))}
          </div>
        )}
      </section>
    </Page>
  )
}

function MarketCard({
  item,
  held,
  busy,
  onTake,
}: {
  item: SchematicItem
  held: boolean
  busy: boolean
  onTake: () => void
}) {
  const size =
    item.width && item.height && item.length
      ? `${item.width} × ${item.height} × ${item.length}`
      : null

  return (
    <article className="schemcard" role="listitem">
      <div className="schemcard__open schemcard__open--static">
        <span className="schemcard__title">
          <strong title={item.name}>{item.name}</strong>
          <span className="badge">{item.source}</span>
        </span>
        <span className="schemcard__file">
          <code title={item.fileName}>{item.fileName}</code>
        </span>
      </div>

      <p className="schemcard__note">
        {item.description || '这个源没有写简介。下到库里就能看到里面是什么。'}
      </p>

      {(size || item.gameVersion || item.size) && (
        <dl className="schemcard__facts">
          {size && (
            <div>
              <dt>尺寸</dt>
              <dd>{size}</dd>
            </div>
          )}
          {item.gameVersion && (
            <div>
              <dt>版本</dt>
              <dd>{item.gameVersion}</dd>
            </div>
          )}
          {item.size ? (
            <div>
              <dt>体积</dt>
              <dd>{formatBytes(item.size)}</dd>
            </div>
          ) : null}
        </dl>
      )}

      {(item.tags?.length ?? 0) > 0 && (
        <p className="schemcard__tags">
          {item.tags?.map((tag) => (
            <span className="badge" key={tag}>
              {tag}
            </span>
          ))}
        </p>
      )}

      {item.author && <p className="schemcard__meta">{item.author}</p>}

      <footer className="schemcard__actions">
        <button className="btn btn--row" onClick={onTake} disabled={busy || held}>
          {held ? '已在库里' : busy ? '下载中…' : '下载入库'}
        </button>
        {item.page && (
          <a className="link" href={item.page} target="_blank" rel="noreferrer">
            源页面
          </a>
        )}
      </footer>
    </article>
  )
}

/* ------------------------------------------------------------------ 索引源 */

function SourcesPage({
  sources,
  notes,
  onChanged,
  onOpenMarket,
}: {
  sources: SchematicSource[]
  notes: Record<string, string>
  onChanged: () => void
  onOpenMarket: () => void
}) {
  const [kind, setKind] = useState<'github' | 'index'>('github')
  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const add = async () => {
    if (url.trim() === '') return
    setBusy(true)
    setError(null)
    try {
      const added = await api.addSchematicSource(kind, url.trim(), name.trim())
      toast(`已添加：${added.name}`)
      setUrl('')
      setName('')
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '添加失败')
    } finally {
      setBusy(false)
    }
  }

  const toggle = async (source: SchematicSource) => {
    try {
      await api.updateSchematicSource(source.id, { disabled: !source.disabled })
      onChanged()
    } catch (err) {
      toast(err instanceof Error ? err.message : '保存失败')
    }
  }

  const remove = async (source: SchematicSource) => {
    const ok = await ask({
      title: '不再从这个源读建筑？',
      lead: source.name,
      detail: '已经下到建筑库里的建筑不受影响，它们是本地文件。',
      confirmLabel: '移除',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteSchematicSource(source.id)
      onChanged()
    } catch (err) {
      toast(err instanceof Error ? err.message : '移除失败')
    }
  }

  return (
    <Page
      wide
      title="索引源"
      lead="建筑市场读哪些地方。schematic 没有 Modrinth 那样的中央仓库，所以这里是你自己攒的书架：一个 GitHub 仓库，或者一份别人发布的索引。"
      aside={
        <p className="meta-chips">
          <span>{`${sources.length} 个源`}</span>
          <span>{`${sources.filter((source) => !source.disabled).length} 个启用`}</span>
        </p>
      }
    >
      <section className="panel">
        <h2 className="panel__title">添加源</h2>

        <div className="field-row">
          <label className="field">
            <span>类型</span>
            <Select
              className="select--block"
              ariaLabel="源类型"
              value={kind}
              onChange={(value) => setKind(value as 'github' | 'index')}
              options={[
                { value: 'github', label: 'GitHub 仓库', note: '仓库里的每个 .schem 都会列出来' },
                { value: 'index', label: '索引地址', note: '一份 JSON 清单' },
              ]}
            />
          </label>
          <label className="field">
            <span>{kind === 'github' ? '仓库' : '索引地址'}</span>
            <input
              value={url}
              placeholder={
                kind === 'github'
                  ? 'owner/name，或直接粘贴仓库链接'
                  : 'https://example.com/schematics/index.json'
              }
              onChange={(event) => setUrl(event.target.value)}
            />
            <small>
              {kind === 'github'
                ? '可以带分支和子目录：owner/name@main:medieval，或者粘 .../tree/main/medieval 这样的链接。'
                : '相对链接会按索引自己的地址补全，所以清单可以和文件放在一起。'}
            </small>
          </label>
          <label className="field">
            <span>显示名（可选）</span>
            <input
              value={name}
              placeholder="留空就用仓库名或域名"
              onChange={(event) => setName(event.target.value)}
            />
          </label>
        </div>

        {error && <div className="alert alert--error">{error}</div>}

        <div className="actions">
          <button className="btn btn--primary" onClick={() => void add()} disabled={busy}>
            {busy ? '添加中…' : '添加'}
          </button>
          <button className="btn" onClick={onOpenMarket}>
            回到建筑市场
          </button>
        </div>
      </section>

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">已添加的源</h2>
          <p className="chart-head__meta">关掉的源不参与搜索，也不会被读取</p>
        </div>

        {/* Its own row rather than .asset: that one is a six-column grid built
            for the fixed facts a core or a runtime has, and a source has a
            name, an address and — when it failed — a sentence about why. */}
        <div className="schemsources">
          {sources.map((source) => (
            <article
              className={`schemsource${source.disabled ? ' schemsource--off' : ''}`}
              key={source.id}
            >
              <div className="schemsource__head">
                <span className="asset__tile asset__tile--accent">
                  {source.kind === 'github' ? 'GH' : 'JS'}
                </span>
                <div className="schemsource__title">
                  <span className="schemsource__name">
                    <strong>{source.name}</strong>
                    {source.builtin && <span className="badge">面板自带</span>}
                    {source.disabled && <span className="badge">已关闭</span>}
                  </span>
                  <code className="schemsource__url" title={source.url}>
                    {source.url}
                  </code>
                </div>
                <span className="schemsource__actions">
                  <button className="link" onClick={() => void toggle(source)}>
                    {source.disabled ? '启用' : '关闭'}
                  </button>
                  {!source.builtin && (
                    <button className="link link--danger" onClick={() => void remove(source)}>
                      移除
                    </button>
                  )}
                </span>
              </div>

              {notes[source.id] && <p className="schemcard__warn">{notes[source.id]}</p>}
            </article>
          ))}
        </div>
      </section>

      <section className="panel">
        <h2 className="panel__title">索引长什么样</h2>
        <p className="chart-note">
          想把自己的建筑发出去，写一份 JSON 放在任何能 HTTPS 访问的地方就行——不需要跑服务。
        </p>
        <pre className="schemlib__sample">
          <code>{SAMPLE_INDEX}</code>
        </pre>
        <p className="chart-note">
          除了 <code>url</code>，每个字段都可以省。面板下载后会自己解析文件，尺寸和方块用量以文件为准；
          写了 <code>sha256</code> 的话会校验，对不上就不入库。
        </p>
      </section>
    </Page>
  )
}

const SAMPLE_INDEX = `{
  "name": "我的建筑索引",
  "items": [
    {
      "id": "castle",
      "name": "中世纪城堡",
      "author": "someone",
      "description": "带护城河和地牢",
      "tags": ["中世纪", "城堡"],
      "gameVersion": "1.20+",
      "url": "castle.schem",
      "sha256": "……"
    }
  ]
}`
