import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { SourcePreview } from '../types'
import type { PluginInput } from '../usePlugins'
import { Modal } from './Modal'

/**
 * Adding a GitHub repository, after looking at it.
 *
 * The old form asked for a repository, an asset pattern and a display name, and
 * then found out whether any of it worked on the next screen — as a library
 * entry reading 检查更新失败. Four completely different problems arrive that
 * way and look identical: a typo in the owner, a private repository with no
 * token configured, a repository that publishes source tarballs and no jar at
 * all, and an asset pattern that matches nothing. All four are answered by one
 * API call, so the dialog makes it before anybody commits.
 *
 * The preview is also the only place the asset pattern can be filled in
 * honestly. It is a rule about file names that will not exist until the next
 * release, written by somebody who has not seen this release's file names —
 * so it shows them, says which one the rule picks today, and offers a pattern
 * derived from that choice. Offered, not applied: which of four platform
 * builds a server wants is not a thing the panel can work out.
 */
export function PluginSourceDialog({
  busy,
  onCancel,
  onSubmit,
}: {
  busy: boolean
  onCancel: () => void
  onSubmit: (input: PluginInput) => Promise<boolean>
}) {
  const [repo, setRepo] = useState('')
  const [name, setName] = useState('')
  const [assetPattern, setAssetPattern] = useState('')
  const [prerelease, setPrerelease] = useState(false)
  const [targetDir, setTargetDir] = useState('plugins')
  const [note, setNote] = useState('')

  const [preview, setPreview] = useState<SourcePreview | null>(null)
  const [looking, setLooking] = useState(false)
  const [lookError, setLookError] = useState<string | null>(null)

  // A new repository invalidates what was seen of the last one. Kept as an
  // explicit clear rather than a keyed fetch, so the panel never shows one
  // repository's releases under another's name.
  useEffect(() => {
    setPreview(null)
    setLookError(null)
  }, [repo])

  const look = async () => {
    if (repo.trim() === '') return
    setLooking(true)
    setLookError(null)
    try {
      setPreview(await api.previewPluginSource(repo, assetPattern, prerelease))
    } catch (err) {
      setLookError(err instanceof Error ? err.message : '看不了这个仓库')
    } finally {
      setLooking(false)
    }
  }

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    await onSubmit({ name, repo, assetPattern, prerelease, targetDir, note })
  }

  const usable = preview?.reachable && !preview.error

  return (
    <Modal onClose={onCancel} busy={busy} label="从 GitHub 仓库添加">
      <form className="modal__card" onSubmit={(event) => void submit(event)}>
        <h2 className="modal__title">从 GitHub 仓库添加</h2>
        <p className="modal__lead">
          面板会跟着这个仓库的 Release 走。私有仓库也可以 —— 令牌在「面板设置 → 插件源与令牌」里配。
        </p>

        <div className="srcform">
          <label className="field srcform__repo">
            <span>GitHub 仓库</span>
            <input
              value={repo}
              autoFocus
              required
              placeholder="EssentialsX/Essentials，或直接粘贴仓库地址"
              onChange={(event) => setRepo(event.target.value)}
              onBlur={() => !preview && void look()}
            />
            <small>填 owner/name 就行；从浏览器地址栏整条粘过来也能识别。</small>
          </label>
          <button
            className="btn"
            type="button"
            disabled={looking || repo.trim() === ''}
            onClick={() => void look()}
          >
            {looking ? '查看中…' : '查看'}
          </button>
        </div>

        {lookError && <div className="alert alert--error">{lookError}</div>}
        {preview && <Preview preview={preview} onUsePattern={setAssetPattern} />}

        {/* The rest of the form only matters once there is something to add,
            and showing six fields above a repository nobody has checked is how
            the old dialog got them all filled in wrongly. */}
        {usable && (
          <>
            <div className="field-row">
              <label className="field">
                <span>显示名称</span>
                <input
                  value={name}
                  placeholder={repoName(preview.repo)}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <label className="field">
                <span>安装目录</span>
                <input
                  value={targetDir}
                  placeholder="plugins"
                  onChange={(event) => setTargetDir(event.target.value)}
                />
                <small>
                  Bukkit 系是 <code>plugins</code>，Fabric、Forge 的模组是 <code>mods</code>。
                </small>
              </label>
            </div>

            <label className="field">
              <span>文件名匹配</span>
              <input
                value={assetPattern}
                placeholder="留空自动挑选"
                onChange={(event) => setAssetPattern(event.target.value)}
                onBlur={() => void look()}
              />
              <small>上面列出的就是这次 Release 的 jar —— 改完这里再点「查看」能看到会挑哪个。</small>
            </label>

            <label className="checkbox checkbox--stacked">
              <input
                type="checkbox"
                checked={prerelease}
                onChange={(event) => {
                  setPrerelease(event.target.checked)
                  setPreview(null)
                }}
              />
              <span>包含预发布版本</span>
              <small>作者标了 pre-release 的版本通常是还没准备好上正式服的。</small>
            </label>

            <label className="field">
              <span>备注</span>
              <input
                value={note}
                placeholder="给自己看的，可留空"
                onChange={(event) => setNote(event.target.value)}
              />
            </label>
          </>
        )}

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button
            className="btn btn--primary"
            type="submit"
            disabled={busy || repo.trim() === '' || looking}
            title={preview ? undefined : '先「查看」一下，确认面板真的能读到这个仓库'}
          >
            {usable ? `添加 ${repoName(preview.repo)}` : '添加'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

/**
 * What the panel can see, before anybody agrees to anything.
 *
 * Two shapes, because there are two answers. It could not read the repository,
 * in which case the only useful content is why and what would fix it. Or it
 * could, in which case the useful content is the newest release and its jars —
 * the evidence that the source will work, and the material the asset pattern
 * is written against.
 */
function Preview({
  preview,
  onUsePattern,
}: {
  preview: SourcePreview
  onUsePattern: (pattern: string) => void
}) {
  if (!preview.reachable || preview.error) {
    return (
      <div className="alert alert--error">
        <strong>读不到 {preview.repo}</strong>
        <p>{preview.error || '仓库不存在，或者面板没有权限。'}</p>
        {preview.needsToken && (
          <p>
            如果它是私有仓库，去「面板设置 → 插件源与令牌」配一个有 <code>repo</code> 权限的
            GitHub 令牌，再回来查看一次。
          </p>
        )}
      </div>
    )
  }

  return (
    <div className="preview">
      <p className="preview__head">
        <span className="badge badge--ok">能访问</span>
        {preview.private && <span className="badge">私有仓库</span>}
        <span>
          {preview.releases} 个可用 Release，最新是 <b>{preview.version}</b>
        </span>
        {preview.publishedAt && <span className="muted">{formatDate(preview.publishedAt)}</span>}
      </p>

      {preview.releases === 1 && (
        <p className="preview__warn">
          只看到一个 Release。可能这个仓库刚开始发布，也可能它把版本发在别处 —— 加进来之前值得确认一下。
        </p>
      )}

      <ul className="preview__assets">
        {preview.assets.map((asset) => (
          <li key={asset.name} className={asset.name === preview.picked ? 'preview__pick' : undefined}>
            <span>{asset.name}</span>
            <span className="muted">{formatBytes(asset.size)}</span>
            {asset.name === preview.picked && <span className="badge badge--ok">会挑这个</span>}
          </li>
        ))}
      </ul>

      {preview.assets.length > 1 && (
        <p className="preview__warn">
          这次 Release 挂了 {preview.assets.length} 个 jar。
          {preview.pattern ? (
            <>
              当前规则 <code>{preview.pattern}</code> 挑中了 <b>{preview.picked}</b>。
            </>
          ) : (
            <>
              没有规则的话面板按「跳过附带包、挑最大的」来选，下次发布文件名一变就可能挑到别的。
              {preview.suggest && (
                <>
                  {' '}
                  建议填{' '}
                  <button className="link" type="button" onClick={() => onUsePattern(preview.suggest!)}>
                    <code>{preview.suggest}</code>
                  </button>
                  。
                </>
              )}
            </>
          )}
        </p>
      )}
    </div>
  )
}

function repoName(repo: string): string {
  const slash = repo.lastIndexOf('/')
  return slash >= 0 ? repo.slice(slash + 1) : repo
}
