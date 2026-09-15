import { useMemo, useState } from 'react'

import { coreFileURL } from '../api'
import { ask } from '../confirm'
import { formatBytes } from '../format'
import { toast, toastError } from '../toast'
import type { ServerCore } from '../types'
import type { CoreController } from '../useCores'
import type { JavaController } from '../useJava'
import { AddCoreDialog } from './AddCoreDialog'
import { Badge } from './Badge'
import { Button } from './Button'
import { isRecommended } from './CoreCatalogue'
import { DataTableEmpty } from './DataTable'
import { EmptyState } from './EmptyState'
import { Page } from './Page'
import {
  ResourceHint,
  ResourcePendingRow,
  ResourceRow,
  ResourceTable,
  StorageHygiene,
  confirmResourceDelete,
} from './ResourceList'
import type { ResourceEntry } from './ResourceList'
import { Select } from './Select'
import { Toolbar } from './Toolbar'

/**
 * The panel's server cores: what is on the shelf.
 *
 * The page used to be two cards, and the ratio between them was backwards. The
 * shelf — the thing you come here to look at — was two rows and about 200px of
 * a screen; the download form under it, which anybody opens perhaps once a
 * month, had five hundred. So the form is a dialog now (see AddCoreDialog) and
 * the list is the page.
 *
 * The two paragraphs that used to stand at the top went with it. "下载一次，
 * 开十个服" describes how the library is implemented, which is not a thing
 * anybody reads twice; "把自己的 jar 丢进目录也会出现在这里" was the only way
 * in for a core the catalogue does not offer, and an instruction with no
 * button on it. Now there is a button, and what is left of the sentence sits
 * under the list where it has something to act on.
 *
 * The columns are the panel's shared shelf shape — see ResourceList — and the
 * one that earns its place here is 运行要求: a Java that is too old is the
 * commonest reason a core will not boot, and until now the only way to find
 * out was to start the server and read the stack trace.
 */

/** How the list can be ordered. Newest first is what a shelf is for. */
type Sort = 'recent' | 'name' | 'size' | 'used'

const SORTS: { value: Sort; label: string }[] = [
  { value: 'recent', label: '最近加入' },
  { value: 'name', label: '名称' },
  { value: 'size', label: '体积' },
  { value: 'used', label: '使用最多' },
]

/** The segments across the top. `idle` is not a type but it is the question
 *  people come to a full shelf with, so it sits with them. */
type Segment = 'all' | 'server' | 'proxy' | 'imported' | 'idle'

const SEGMENTS: { id: Segment; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'server', label: '服务端' },
  { id: 'proxy', label: '代理端' },
  { id: 'imported', label: '手动放入' },
  { id: 'idle', label: '未使用' },
]

export function CoreLibraryPage({
  cores,
  java,
  onOpenJava,
  onOpenInstances,
}: {
  cores: CoreController
  java: JavaController
  /** Java 运行时, with `major` already picked when the warning sent them. */
  onOpenJava: (major?: number) => void
  /** 所有实例, narrowed to one name when there is exactly one to look at. */
  onOpenInstances: (query: string) => void
}) {
  // Open, or not. There used to be a second value for "open on the upload
  // tile", behind a 上传 jar button that stood in the page head and again in
  // the empty state — three doors into one room, counting the tile itself,
  // for a page whose own footnote tells you the way in is
  // 「添加核心 → 上传自定义 jar」. The dialog's tile row is where a core comes
  // from; that is the one place, and 插件列表 settled the same argument the
  // same way (see the Menu in PluginLibraryPage).
  const [adding, setAdding] = useState(false)
  const [query, setQuery] = useState('')
  const [segment, setSegment] = useState<Segment>('all')
  const [sort, setSort] = useState<Sort>('recent')

  const { job, downloading, busy } = cores
  const stored = cores.cores
  const total = stored.reduce((sum, core) => sum + core.size, 0)
  /** The empty state is on screen, so it owns the way in and the head lets go
   *  of it. Two 添加核心 a hand apart, both opening the same dialog, is the
   *  same duplication the 上传 jar pair was — one entrance means one, not one
   *  per region that could plausibly hold it. */
  const bare = stored.length === 0 && !downloading
  const idle = stored.filter((core) => core.usedBy.length === 0)

  const counts = useMemo(
    () => ({
      all: stored.length,
      server: stored.filter((core) => core.kind === 'server').length,
      proxy: stored.filter((core) => core.kind === 'proxy').length,
      imported: stored.filter((core) => core.imported).length,
      idle: idle.length,
    }),
    [stored, idle.length],
  )

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const matched = stored.filter((core) => {
      if (segment === 'server' && core.kind !== 'server') return false
      if (segment === 'proxy' && core.kind !== 'proxy') return false
      if (segment === 'imported' && !core.imported) return false
      if (segment === 'idle' && core.usedBy.length > 0) return false
      if (!needle) return true
      return (
        core.fileName.toLowerCase().includes(needle) ||
        core.projectName.toLowerCase().includes(needle) ||
        core.version.toLowerCase().includes(needle)
      )
    })

    const sorted = [...matched]
    switch (sort) {
      case 'name':
        sorted.sort((a, b) => nameOf(a).localeCompare(nameOf(b), 'zh-CN'))
        break
      case 'size':
        sorted.sort((a, b) => b.size - a.size)
        break
      case 'used':
        sorted.sort((a, b) => b.usedBy.length - a.usedBy.length)
        break
      default:
        // The listing already arrives newest first.
        break
    }
    return sorted
  }, [stored, segment, query, sort])

  const remove = async (core: ServerCore) => {
    const ok = await confirmResourceDelete({
      name: `${nameOf(core)}（${formatBytes(core.size)}）`,
      detail:
        '库里的这份 jar 会被删掉，之后想再要就得重新下载一次。已经复制进实例目录的副本不受影响。',
      usedBy: core.usedBy,
      // Deliberately not "删除后该实例无法启动": on this shelf it would be a
      // lie. An instance launches its own copy, so what is actually lost is
      // the ability to stamp out another server exactly like it — which is
      // the whole reason the library exists, and reason enough to refuse.
      // A plain string rather than a fragment: a JSX line break between two
      // Chinese characters renders as a space, and this sentence is long
      // enough to need three of them.
      usedByNote: (names) =>
        `实例「${names.join('、')}」正在用这个 jar 启动。它们各自有一份副本，` +
        '所以删掉库里的这一份不会让它们停机 —— 但也就没法再复制出一台一模一样的服了。' +
        '要删的话，先把这些实例换到别的核心上。',
      onInspect: (names) => onOpenInstances(names.length === 1 ? names[0] : ''),
    })
    if (!ok) return
    await cores.remove(core.id)
  }

  const clean = async () => {
    const names = idle.slice(0, 3).map((core) => core.fileName)
    const bytes = idle.reduce((sum, core) => sum + core.size, 0)
    const ok = await ask({
      title: `清理 ${idle.length} 个没人用的核心？`,
      lead: names.join('、') + (idle.length > 3 ? ` 等 ${idle.length} 项` : ''),
      detail: `合计 ${formatBytes(bytes)}。只删没有实例在用的，正在被使用的一个都不动。`,
      confirmLabel: '清理',
      danger: true,
    })
    if (!ok) return
    await cores.removeMany(idle.map((core) => core.id))
  }

  return (
    <Page
      wide
      // The top bar's trail already ends on 服务端核心 in bold. Saying it again
      // at 23px directly underneath cost about ninety pixels of the first
      // screen and two rows that neither of them filled. The h1 stays for the
      // outline and the screen reader; it just is not painted twice.
      titleHidden
      title="服务端核心"
      facts={
        <>
          <span>{stored.length > 0 ? `${stored.length} 个 · ${formatBytes(total)}` : '还是空的'}</span>
          {cores.library?.root && (
            <span title={cores.library.root}>
              <code>{cores.library.root}</code>
            </span>
          )}
        </>
      }
      actions={
        <>
          {/* 取消下载 is the only other thing this head ever offers, and it is
              genuinely a different action rather than a second way in. */}
          {downloading && (
            <Button variant="danger" type="button" onClick={() => void cores.cancel()} disabled={busy}>
              取消下载
            </Button>
          )}
          {/* Never filled. check-ui's rulePrimaryButtons says the screen's
              single filled button is "what the screen is asking for right
              now" — an empty state's call to action, not the standing entrance
              in a page head. On a shelf with cores on it nothing is being
              asked, so nothing here is filled; on an empty one the entrance
              is not here at all. */}
          {!bare && (
            <Button type="button" onClick={() => setAdding(true)}>
              添加核心
            </Button>
          )}
        </>
      }
    >
      {cores.error && <div className="alert">{cores.error}</div>}

      {/* The table stays when the shelf is empty. It used to be swapped out
          for a placard, which left a 1440px band with three lines floating in
          the middle of it and told nobody what this page looks like once it has
          something in it. The header is the page's promise — these are the
          columns a core is judged by — so the frame and the header stay and the
          absence goes inside them, where the rows would have been.

          The toolbar is the one thing that does go: filtering nothing is a row
          of controls that cannot do anything. */}
      {stored.length > 0 && (
        <Toolbar>
          <input
            className="toolbar__search"
            type="search"
            value={query}
            placeholder="筛选核心"
            aria-label="筛选核心"
            onChange={(event) => setQuery(event.target.value)}
          />
          <div className="toolbar__chips">
            {SEGMENTS.map((item) => (
              <button
                key={item.id}
                type="button"
                className={`chip${segment === item.id ? ' chip--on' : ''}`}
                aria-pressed={segment === item.id}
                onClick={() => setSegment(item.id)}
              >
                {item.label}
                <b>{counts[item.id]}</b>
              </button>
            ))}
          </div>
          {/* The count, then the sort beside it: a Select alone at the end
              of a wide toolbar is a control floating in a corner, seven
              hundred pixels from the chips it belongs with and lined up
              against nothing. `.toolbar__count + .toolbar__tools` drops the
              auto margin so the two read as one cluster. */}
          <span className="toolbar__count">
            {shown.length} / {stored.length}
          </span>
          <div className="toolbar__tools">
            <Select
              value={sort}
              onChange={(value) => setSort(value as Sort)}
              options={SORTS}
              className="input-slim"
              ariaLabel="排序"
            />
          </div>
        </Toolbar>
      )}

      <ResourceTable heads={{ compat: '支持 MC', requires: '运行要求' }} label="核心库">
        {downloading && job && (
          <ResourcePendingRow
            title={`${job.title}${job.subtitle ? ` ${job.subtitle}` : ''}`}
            fileName={job.fileName}
            downloaded={job.downloaded}
            total={job.total}
          />
        )}
        {bare ? (
          <EmptyState
            inline
            title="还没有任何核心"
            action={
              <Button variant="primary" type="button" onClick={() => setAdding(true)}>
                添加核心
              </Button>
            }
          >
            下载一个 Paper 或 Velocity，或者把自己的 jar（Forge、Fabric、整合包自带的服务端）上传进来 ——
            两条路都在「添加核心」里。核心下好之后，新建实例时选它就行。
          </EmptyState>
        ) : shown.length === 0 ? (
          <DataTableEmpty>没有符合条件的核心。</DataTableEmpty>
        ) : (
          shown.map((core) => (
            <ResourceRow key={core.id} entry={entryOf(core, () => void remove(core))} />
          ))
        )}
      </ResourceTable>

      {/* The hygiene card only when there is something to clean. It used
          to stand there reading 「0 个核心没有被任何实例使用，合计 0 B」
          beside a disabled button — half a row spent telling the operator
          that the thing they did not ask about has not happened. A clean
          shelf says nothing. */}
      <div className="rescards">
        {idle.length > 0 && (
          <StorageHygiene
            idle={idle.length}
            bytes={idle.reduce((sum, core) => sum + core.size, 0)}
            unit="个核心"
            onClean={() => void clean()}
            busy={busy}
          />
        )}
        <ResourceHint>
          手动丢进核心库目录的 jar 也会出现在这里，但它的版本和 Java 要求需要你补一下 ——
          从「添加核心 → 上传自定义 jar」传进来的会带上这些信息。
        </ResourceHint>
      </div>

      {adding && (
        <AddCoreDialog
          java={java}
          busy={busy}
          onClose={() => setAdding(false)}
          onDownload={(project, version, build) => cores.download(project, version, build)}
          onUploaded={() => void cores.refresh()}
          onOpenJava={(major) => {
            setAdding(false)
            onOpenJava(major)
          }}
        />
      )}
    </Page>
  )
}

/** An imported jar has no project, so its file name is the only name it has —
 *  which is also why it is the one row that does not repeat the file name
 *  underneath. Its version, where somebody filled one in, is in the version
 *  column like everyone else's. */
function nameOf(core: ServerCore): string {
  if (core.imported) return core.fileName
  return `${core.projectName} ${core.version}`
}

function entryOf(core: ServerCore, onRemove: () => void): ResourceEntry {
  // 信息不全 is about the two columns that decide whether it will start, not
  // about the row being sparse: a jar with no Java requirement on it is one
  // nobody can check before pressing 启动.
  const incomplete = core.javaMinimum <= 0 || core.minecraft === ''

  return {
    id: core.id,
    tile: core.projectName || core.fileName,
    name: nameOf(core),
    fileName: core.imported ? undefined : core.fileName,
    chips: (
      <>
        {core.kind === 'proxy' && <Badge>代理端</Badge>}
        {core.imported && <Badge>手动放入</Badge>}
        {!core.imported && !isRecommended(core.channel) && <Badge tone="warn">{core.channel}</Badge>}
        {incomplete && <Badge tone="warn">信息不全</Badge>}
      </>
    ),
    version: core.imported ? (
      core.version || <span className="reslist__idle">未知</span>
    ) : (
      <>
        {core.version}
        <span className="reslist__sub">#{core.build}</span>
      </>
    ),
    compat: core.minecraft || <span className="reslist__idle">未知</span>,
    requires:
      core.javaMinimum > 0 ? (
        `Java ${core.javaMinimum}+`
      ) : (
        <span className="reslist__idle">未知</span>
      ),
    size: core.size,
    usedBy: core.usedBy,
    addedAt: core.addedAt,
    menu: [
      {
        label: '复制文件路径',
        onSelect: () => void copy(core.fileName, '文件名已复制'),
      },
      {
        label: '复制 SHA-256',
        onSelect: () => void copy(core.sha256, '校验和已复制'),
        disabled: core.sha256 === '',
      },
      {
        label: '下载到本地',
        // The panel downloads onto the machine it runs on, which leaves no way
        // to get a build back off it — and an operator moving a server
        // elsewhere should not have to go and find it upstream again.
        onSelect: () => window.open(coreFileURL(core.id), '_blank', 'noopener'),
      },
      { label: '删除', onSelect: onRemove, danger: true },
    ],
  }
}

async function copy(text: string, done: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast(done)
  } catch {
    // Clipboard access is refused outside a secure context, which is exactly
    // where a self-hosted panel on a LAN address lives.
    toastError('这个浏览器不让复制，手动选一下吧。')
  }
}
