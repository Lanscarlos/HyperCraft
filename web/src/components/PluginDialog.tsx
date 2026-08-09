import { useState } from 'react'

import type { LibraryPlugin } from '../types'
import type { PluginInput } from '../usePlugins'

/**
 * Add and edit share a form: the fields are the same, and so are the rules.
 *
 * It lives in its own file because the two pages that open it — the library
 * list and one plugin's detail page — have nothing else in common, and a dialog
 * exported from whichever page happened to need it first is how a UI ends up
 * with pages importing each other.
 */
export function PluginDialog({
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
          目前只支持 GitHub Release。私有仓库也可以 —— 在「设置 → 插件源」里填好访问令牌就行，
          下载源也在那里选。
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
            通常不用管：配了访问令牌后，面板每次检查更新和下载前都会问一下 GitHub 这个仓库是不是私有的，
            并按实际情况自动纠正这个勾。私有仓库的 jar 只能从 GitHub API 取，所以不走下载源 ——
            那是外人，没必要让它知道这个仓库的存在。
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
