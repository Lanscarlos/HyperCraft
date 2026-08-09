import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { LibraryPlugin, PluginDownloadJob, PluginRelease } from '../types'
import { hasPluginUpdate } from '../types'
import type { PluginController, PluginInput } from '../usePlugins'

/**
 * Panel-wide plugin management: where a plugin comes from, which versions the
 * panel holds, and what is worth updating.
 *
 * Everything about a plugin is decided here and nowhere else. An instance may
 * take a copy, swap which version it holds, or switch one off — it cannot add
 * a plugin, define a download source or delete a version. That is the whole
 * point of the split: a panel where each server manages its own downloads ends
 * up with six subtly different copies of the same plugin and nobody able to
 * say which is which, and the version an operator most wants to roll back to
 * is always the one that was overwritten in place.
 */
export function PluginLibraryPage({
  plugins,
  onOpenInstances,
}: {
  plugins: PluginController
  onOpenInstances: () => void
}) {
  const [editing, setEditing] = useState<LibraryPlugin | null>(null)
  const [adding, setAdding] = useState(false)

  const { library, job, downloading, busy } = plugins
  const tracked = plugins.plugins
  const totalSize = tracked.reduce(
    (sum, item) => sum + item.versions.reduce((n, version) => n + version.size, 0),
    0,
  )
  const versionCount = tracked.reduce((sum, item) => sum + item.versions.length, 0)
  // Worth saying out loud rather than leaving as a per-card check failure: a
  // private plugin with no token can neither check nor download, and the reason
  // is one setting away.
  const privateWithoutToken =
    library !== null && !library.tokenConfigured && tracked.some((item) => item.source.private)

  // A finished download adds a version; the listing has to catch up.
  useEffect(() => {
    if (job?.state !== 'done') return
    void plugins.refresh()
  }, [job?.state, job?.tag, plugins])

  const remove = async (item: LibraryPlugin) => {
    const inUse =
      item.usedBy.length > 0
        ? `实例「${item.usedBy.join('、')}」正在用它，不过它们各自有一份副本，删掉库里的不影响已经装上的。`
        : ''
    if (!window.confirm(`确定要把「${item.name}」和它的 ${item.versions.length} 个版本从插件库删除吗？${inUse}`)) {
      return
    }
    await plugins.remove(item.id)
  }

  return (
    <div className="page page--wide">
      <header className="page__head">
        <div>
          <h1>插件库</h1>
          <p className="page__lead">
            插件在这里统一管理：添加下载源、拉取版本、检查更新。实例那边只负责「用哪个插件、用哪个版本、
            要不要停用」，不能自己下载 —— 这样同一个插件全站只有一份来源，版本回滚时也永远还找得到旧包。
            下载走服务器自己的网络，关掉网页也会继续。
          </p>
        </div>
        <p className="meta-chips">
          <span>{tracked.length > 0 ? `${tracked.length} 个插件` : '插件库还是空的'}</span>
          {versionCount > 0 && <span>{versionCount} 个版本</span>}
          {totalSize > 0 && <span>共 {formatBytes(totalSize)}</span>}
          {library?.root && <span title={library.root}>存放于 {library.root}</span>}
        </p>
      </header>

      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
      {job && <JobStatus job={job} onCancel={() => void plugins.cancel()} busy={busy} />}

      {privateWithoutToken && (
        <div className="alert alert--warn">
          有插件来自私有仓库，但面板还没有 GitHub 访问令牌 —— 在下面填一个，否则这些插件既检查不到更新也下载不了。
        </div>
      )}

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">已跟踪的插件</h2>
          <div className="field__tools">
            <button
              className="btn"
              type="button"
              disabled={busy || tracked.length === 0}
              onClick={() => void plugins.checkAll()}
            >
              检查全部更新
            </button>
            <button className="btn btn--primary" type="button" onClick={() => setAdding(true)}>
              + 添加插件
            </button>
          </div>
        </div>

        {tracked.length === 0 ? (
          <div className="welcome__empty">
            <p>插件库还是空的。</p>
            <p className="muted">
              添加一个插件的 GitHub 仓库（比如 <code>EssentialsX/Essentials</code>，或者直接粘贴仓库地址），
              面板就会从它的 Release 里拉取 jar。装到服务器上是在实例的「插件」标签里做的 ——
              <button className="link" onClick={onOpenInstances}>
                去实例
              </button>
              。
            </p>
          </div>
        ) : (
          <div className="asset-grid">
            {tracked.map((item) => (
              <PluginCard
                key={item.id}
                item={item}
                busy={busy}
                downloading={downloading}
                onCheck={() => void plugins.check(item.id)}
                onDownload={(tag) => void plugins.download(item.id, tag)}
                onRemoveVersion={(tag) => void plugins.removeVersion(item.id, tag)}
                onEdit={() => setEditing(item)}
                onRemove={() => void remove(item)}
              />
            ))}
          </div>
        )}
      </section>

      <GitHubTokenPanel
        configured={library?.tokenConfigured ?? false}
        hint={library?.tokenHint}
        busy={busy}
        onSave={(token) => plugins.setToken(token)}
      />

      {(adding || editing) && (
        <PluginDialog
          item={editing}
          busy={busy}
          onCancel={() => {
            setAdding(false)
            setEditing(null)
          }}
          onSubmit={async (input) => {
            const ok = editing ? await plugins.edit(editing.id, input) : await plugins.add(input)
            if (ok) {
              setAdding(false)
              setEditing(null)
            }
            return ok
          }}
        />
      )}
    </div>
  )
}

/**
 * The credential private repositories are read with.
 *
 * It is write-only on purpose: the panel will say whether it holds a token and
 * show its last four characters, and that is all — a token that can be read
 * back out of a page is a token that leaks with the page. Getting a wrong one
 * right again is done by pasting a new one over it, not by editing it in place.
 */
function GitHubTokenPanel({
  configured,
  hint,
  busy,
  onSave,
}: {
  configured: boolean
  hint?: string
  busy: boolean
  onSave: (token: string) => Promise<boolean>
}) {
  const [token, setToken] = useState('')

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!token.trim()) return
    if (await onSave(token.trim())) setToken('')
  }

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">GitHub 访问令牌</h2>
        {configured && <span className="badge badge--ok">已配置{hint && ` ···${hint}`}</span>}
      </div>
      <p className="chart-note">
        自己写的插件发在私有仓库里时，面板得先能证明「我是你」才看得见它 —— 填一个令牌就行，
        添加插件时把「私有仓库」勾上，检查更新和下载都会走带认证的 GitHub API。
        顺带一提，就算全是公开仓库，配了令牌也值：匿名调用每小时只有 60 次，插件一多就不够用。
      </p>
      <form onSubmit={(event) => void save(event)}>
        <label className="field">
          <span>令牌</span>
          <input
            type="password"
            value={token}
            autoComplete="off"
            spellCheck={false}
            placeholder={configured ? '粘贴新令牌以替换现有的' : 'github_pat_… 或 ghp_…'}
            onChange={(e) => setToken(e.target.value)}
          />
          <small>
            在 GitHub 的 Settings → Developer settings → Personal access tokens 里生成。
            fine-grained 令牌只要给目标仓库的 <code>Contents: Read-only</code> 权限；
            classic 令牌勾 <code>repo</code>。令牌存在面板自己的 panel.json 里（0600），
            只发给 api.github.com，不会经过下载镜像。
          </small>
        </label>
        <div className="field__tools">
          <button className="btn btn--primary" type="submit" disabled={busy || !token.trim()}>
            {configured ? '替换令牌' : '保存令牌'}
          </button>
          {configured && (
            <button
              className="btn"
              type="button"
              disabled={busy}
              onClick={() => {
                if (window.confirm('清除后，私有仓库的插件将无法检查更新或下载。确定吗？')) {
                  void onSave('')
                }
              }}
            >
              清除
            </button>
          )}
        </div>
      </form>
    </section>
  )
}

function PluginCard({
  item,
  busy,
  downloading,
  onCheck,
  onDownload,
  onRemoveVersion,
  onEdit,
  onRemove,
}: {
  item: LibraryPlugin
  busy: boolean
  downloading: boolean
  onCheck: () => void
  onDownload: (tag: string) => void
  onRemoveVersion: (tag: string) => void
  onEdit: () => void
  onRemove: () => void
}) {
  const [releases, setReleases] = useState<PluginRelease[] | null>(null)
  const [loadingReleases, setLoadingReleases] = useState(false)
  const [releaseError, setReleaseError] = useState<string | null>(null)

  const updatable = hasPluginUpdate(item)
  const held = new Set(item.versions.map((version) => version.tag))

  const loadReleases = async () => {
    if (releases || loadingReleases) return
    setLoadingReleases(true)
    setReleaseError(null)
    try {
      setReleases(await api.pluginReleases(item.id))
    } catch (err) {
      setReleaseError(err instanceof Error ? err.message : '获取版本列表失败')
    } finally {
      setLoadingReleases(false)
    }
  }

  return (
    <article className="asset">
      <div className="asset__head">
        <span className="asset__tile asset__tile--accent">{item.name.slice(0, 1).toUpperCase()}</span>
        <div className="asset__title">
          <strong title={item.name}>{item.name}</strong>
          <span className="asset__sub" title={item.source.repo}>
            {item.source.repo}
          </span>
        </div>
        {updatable && <span className="badge badge--update">有更新</span>}
        {item.source.private && <span className="badge">私有</span>}
        {item.targetDir !== 'plugins' && <span className="badge">{item.targetDir}/</span>}
      </div>

      {item.note && <p className="asset__path">{item.note}</p>}

      <dl className="asset__facts">
        <div>
          <dt>已下载</dt>
          <dd>{item.versions.length} 个版本</dd>
        </div>
        <div>
          <dt>最新</dt>
          <dd>{item.latest ? item.latest.version : '未知'}</dd>
        </div>
        <div>
          <dt>检查于</dt>
          <dd>{item.checkedAt ? formatDate(item.checkedAt) : '从未'}</dd>
        </div>
      </dl>

      {item.checkError && <div className="alert alert--warn">检查更新失败：{item.checkError}</div>}

      {updatable && item.latest && (
        <div className="alert alert--ok">
          上游最新是 {item.latest.version}
          {item.latest.prerelease && '（预发布）'}，库里还没有。
          <button
            className="link"
            disabled={busy || downloading}
            onClick={() => onDownload(item.latest?.tag ?? '')}
          >
            下载它
          </button>
        </div>
      )}

      {item.versions.length > 0 && (
        <div className="plugin-versions">
          {item.versions.map((version) => (
            <div className="plugin-version" key={version.tag}>
              <span className="plugin-version__name" title={version.fileName}>
                {version.version}
                {version.prerelease && <span className="badge badge--warn">预发布</span>}
              </span>
              <span className="plugin-version__meta">
                {formatBytes(version.size)} · {formatDate(version.publishedAt)}
              </span>
              <button
                className="link link--danger"
                disabled={busy || downloading}
                onClick={() => {
                  if (window.confirm(`确定要删除 ${item.name} ${version.version} 吗？已经装到实例里的副本不受影响。`)) {
                    onRemoveVersion(version.tag)
                  }
                }}
              >
                删除
              </button>
            </div>
          ))}
        </div>
      )}

      {releaseError && <div className="alert alert--error">{releaseError}</div>}
      {releases && (
        <div className="field">
          <span>上游发布</span>
          <div className="plugin-versions">
            {releases.map((release) => (
              <div className="plugin-version" key={release.tag}>
                <span className="plugin-version__name" title={release.asset.name}>
                  {release.version}
                  {release.prerelease && <span className="badge badge--warn">预发布</span>}
                  {held.has(release.tag) && <span className="badge">已下载</span>}
                </span>
                <span className="plugin-version__meta">
                  {formatBytes(release.asset.size)} · {formatDate(release.publishedAt)}
                </span>
                <button
                  className="link"
                  disabled={busy || downloading}
                  onClick={() => onDownload(release.tag)}
                >
                  {held.has(release.tag) ? '重新下载' : '下载'}
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      <footer className="asset__actions">
        {item.usedBy.length > 0 ? (
          <span className="asset__users">
            使用中：
            {item.usedBy.map((name) => (
              <span className="badge" key={name}>
                {name}
              </span>
            ))}
          </span>
        ) : (
          <span className="muted">暂时没有实例用它</span>
        )}
        <span className="device-row__main">
          <button className="link" disabled={busy} onClick={onCheck}>
            检查更新
          </button>
          <button className="link" disabled={busy || loadingReleases} onClick={() => void loadReleases()}>
            {loadingReleases ? '获取中…' : releases ? '已列出' : '选择版本'}
          </button>
          <button className="link" disabled={busy} onClick={onEdit}>
            编辑
          </button>
          <button className="link link--danger" disabled={busy} onClick={onRemove}>
            删除
          </button>
        </span>
      </footer>
    </article>
  )
}

/** Add and edit share a form: the fields are the same, and so are the rules. */
function PluginDialog({
  item,
  busy,
  onCancel,
  onSubmit,
}: {
  item: LibraryPlugin | null
  busy: boolean
  onCancel: () => void
  onSubmit: (input: PluginInput) => Promise<boolean>
}) {
  const [name, setName] = useState(item?.name ?? '')
  const [repo, setRepo] = useState(item?.source.repo ?? '')
  const [assetPattern, setAssetPattern] = useState(item?.source.assetPattern ?? '')
  const [prerelease, setPrerelease] = useState(item?.source.prerelease ?? false)
  const [isPrivate, setIsPrivate] = useState(item?.source.private ?? false)
  const [targetDir, setTargetDir] = useState(item?.targetDir ?? 'plugins')
  const [note, setNote] = useState(item?.note ?? '')

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    await onSubmit({ name, repo, assetPattern, prerelease, private: isPrivate, targetDir, note })
  }

  return (
    <div className="modal" role="dialog" aria-modal="true">
      <form className="modal__card" onSubmit={(event) => void submit(event)}>
        <h2 className="modal__title">{item ? `编辑「${item.name}」` : '添加插件'}</h2>
        <p className="modal__lead">
          目前只支持 GitHub Release —— 和面板自己更新走的是同一条路，镜像源也跟着设置里的那个走。
        </p>

        <label className="field">
          <span>GitHub 仓库</span>
          <input
            value={repo}
            autoFocus
            required
            placeholder="EssentialsX/Essentials，或直接粘贴仓库地址"
            onChange={(e) => setRepo(e.target.value)}
          />
          <small>填 owner/name 就行；从浏览器地址栏整条粘过来也能识别。</small>
        </label>

        <label className="field">
          <span>显示名称</span>
          <input
            value={name}
            placeholder="留空就用仓库名"
            onChange={(e) => setName(e.target.value)}
          />
        </label>

        <label className="field">
          <span>安装目录</span>
          <input value={targetDir} placeholder="plugins" onChange={(e) => setTargetDir(e.target.value)} />
          <small>
            Bukkit / Spigot / Paper / Velocity / BungeeCord 都是 <code>plugins</code>；
            Fabric、Forge 的模组要填 <code>mods</code>。
          </small>
        </label>

        <label className="field">
          <span>文件名匹配</span>
          <input
            value={assetPattern}
            placeholder="留空自动挑选，例如 EssentialsX-*.jar"
            onChange={(e) => setAssetPattern(e.target.value)}
          />
          <small>
            一个 Release 里挂了好几个 jar 时用它指定要哪个。留空时面板会跳过 sources、javadoc
            这类附带包，从剩下的里挑最大的那个。
          </small>
        </label>

        <label className="checkbox checkbox--stacked">
          <input
            type="checkbox"
            checked={prerelease}
            onChange={(e) => setPrerelease(e.target.checked)}
          />
          <span>包含预发布版本</span>
          <small>作者标了 pre-release 的版本通常是还没准备好上正式服的。</small>
        </label>

        <label className="checkbox checkbox--stacked">
          <input
            type="checkbox"
            checked={isPrivate}
            onChange={(e) => setIsPrivate(e.target.checked)}
          />
          <span>私有仓库</span>
          <small>
            自己的插件发在私有仓库里就勾上，需要先在插件库页面配好 GitHub 访问令牌。
            私有仓库的 jar 只能从 GitHub API 取，所以不走下载镜像 —— 镜像是外人，
            没必要让它知道这个仓库的存在。
          </small>
        </label>

        <label className="field">
          <span>备注</span>
          <input value={note} placeholder="给自己看的，可留空" onChange={(e) => setNote(e.target.value)} />
        </label>

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button className="btn btn--primary" type="submit" disabled={busy || !repo.trim()}>
            {item ? '保存' : '添加'}
          </button>
        </div>
      </form>
    </div>
  )
}

function JobStatus({
  job,
  onCancel,
  busy,
}: {
  job: PluginDownloadJob
  onCancel: () => void
  busy: boolean
}) {
  if (job.state === 'downloading') {
    const fraction = job.total > 0 ? job.downloaded / job.total : 0
    return (
      <div className="download-status">
        <div className="progress">
          <div className="progress__bar" style={{ width: `${Math.round(fraction * 100)}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${Math.round(fraction * 100)}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : formatBytes(job.downloaded)}
          </span>
        </div>
        <p className="chart-note">
          正在下载 {job.pluginName} {job.version}（{job.fileName}）
          <button className="link link--danger" onClick={onCancel} disabled={busy}>
            取消
          </button>
        </p>
      </div>
    )
  }

  if (job.state === 'done') {
    return (
      <div className="alert alert--ok">
        已下载 {job.pluginName} {job.version}，现在可以在实例的「插件」标签里装上。
      </div>
    )
  }

  if (job.state === 'cancelled') {
    return <div className="alert alert--ok">已取消下载 {job.fileName}，未写入任何文件。</div>
  }

  return (
    <div className="alert alert--error">
      下载失败：{job.error ?? '未知错误'}
      {job.fileName && `（${job.fileName}）`}
    </div>
  )
}
