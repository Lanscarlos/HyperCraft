import { useRef, useState } from 'react'

import { importPluginJars } from '../api'
import { formatBytes } from '../format'
import type { ImportedPlugin } from '../types'
import { Modal } from './Modal'
import { loaderNote } from './PluginInstallDialog'

/**
 * Putting a jar the operator already has into the library.
 *
 * The catalogues plus GitHub reach almost everything, and "almost" is the
 * problem: a plugin bought on a marketplace, one built from a fork, one a
 * friend sent over, one whose author only ever posts it in a Discord. Until
 * now those had exactly one way in — the file manager, into one server's
 * plugins directory, where the panel could see a file and say nothing about
 * it. Doing that for four servers is four uploads of the same bytes, four
 * files nothing can compare, and no answer at all to "are they the same
 * version".
 *
 * Here it is one upload. After that the jar is an ordinary library plugin:
 * checksummed once, installable onto as many servers as asked, and counted in
 * the cross-instance version view like everything else. The one thing it does
 * not get is update checking, because there is nowhere to check — and the
 * dialog says so rather than leaving it to be discovered.
 *
 * Nothing is typed. Every server plugin ships a descriptor naming itself, its
 * version and the platform it is built for, and the panel already reads all
 * five formats — so the import asks the jar, and then shows what it heard.
 * What the operator would otherwise type is exactly what the panel would then
 * have to take their word for.
 */
export function PluginImportDialog({
  onCancel,
  onImported,
}: {
  onCancel: () => void
  /** Called once with a summary, after the operator has read the results. */
  onImported: (summary: string) => void
}) {
  const [files, setFiles] = useState<File[]>([])
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [results, setResults] = useState<ImportedPlugin[] | null>(null)
  const picker = useRef<HTMLInputElement | null>(null)

  const upload = async () => {
    setBusy(true)
    setError(null)
    try {
      const answer = await importPluginJars(files, setProgress)
      setResults(answer.results)
    } catch (err) {
      setError(err instanceof Error ? err.message : '导入失败')
    } finally {
      setBusy(false)
      setProgress(0)
    }
  }

  // Read once, when the results come back: the caller closes the dialog on it.
  const finish = () => {
    const ok = (results ?? []).filter((entry) => entry.imported)
    onImported(
      ok.length === 0
        ? '没有导入任何 jar'
        : `已导入 ${ok.length} 个 jar 到插件库` +
            (ok.length < (results?.length ?? 0) ? `，${(results?.length ?? 0) - ok.length} 个失败` : ''),
    )
  }

  return (
    <Modal onClose={onCancel} busy={busy} label="导入 jar">
      <div className="modal__card">
        <h2 className="modal__title">导入 jar 到插件库</h2>

        {results ? (
          <>
            <ul className="import-results">
              {results.map((entry) => (
                <li key={entry.fileName} className={entry.error ? 'import-results__bad' : undefined}>
                  <strong>{entry.fileName}</strong>
                  {entry.imported ? (
                    <small>
                      {/* What the jar said about itself. Shown rather than
                          silently stored, because if the panel read the wrong
                          name this is the only moment anyone would notice. */}
                      认出是 {entry.imported.plugin.name} {entry.imported.version.version}
                      {loaderNote(entry.imported.version.loaders) &&
                        ` · 给 ${loaderNote(entry.imported.version.loaders)} 用的`}
                      {entry.imported.info.apiVersion && ` · 声明支持 ${entry.imported.info.apiVersion}`}
                      {entry.imported.replaced && ' · 库里已经有一模一样的一份，已覆盖'}
                    </small>
                  ) : (
                    <small>{entry.error}</small>
                  )}
                </li>
              ))}
            </ul>

            {results.some((entry) => entry.imported && !entry.imported.info.name) && (
              <p className="chart-note">
                有 jar 没带描述文件，面板读不出它的名字和版本，只能按文件名登记 ——
                Forge / NeoForge 的模组就是这样（它们的描述文件是 TOML，面板不去猜）。
                装是能装的，只是列表里显示的名字要将就一下。
              </p>
            )}

            <div className="modal__actions">
              <button className="btn btn--primary" onClick={finish}>
                完成
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="chart-note">
              市场买的、自己编译的、朋友发的 —— 这些插件站上找不到的 jar 从这里进库。
              进库之后它和下载来的插件一模一样：只存一份、算一次校验和、想装几台服就装几台，
              也一样出现在「跨实例总览」里。
              <strong>唯一的区别是它没有上游，所以永远不会有更新提示</strong>，换版本要再传一次。
            </p>

            <div className="field">
              <span>选择 jar</span>
              <input
                ref={picker}
                type="file"
                accept=".jar,application/java-archive"
                multiple
                disabled={busy}
                onChange={(event) => setFiles(Array.from(event.target.files ?? []))}
              />
              <small>
                可以一次选好几个。面板会打开每个 jar 读它自己的描述文件，
                名字、版本号和适用的服务端核心都从里面取 —— 不用你填。
              </small>
            </div>

            {files.length > 0 && (
              <ul className="import-results">
                {files.map((file) => (
                  <li key={file.name}>
                    <strong>{file.name}</strong>
                    <small>{formatBytes(file.size)}</small>
                  </li>
                ))}
              </ul>
            )}

            {busy && (
              <div className="progress">
                <div className="progress__bar" style={{ width: `${Math.round(progress * 100)}%` }} />
                <span className="progress__label">{Math.round(progress * 100)}%</span>
              </div>
            )}

            {error && <div className="alert alert--error">{error}</div>}

            <div className="modal__actions">
              <button className="btn" disabled={busy} onClick={onCancel}>
                取消
              </button>
              <button
                className="btn btn--primary"
                disabled={busy || files.length === 0}
                onClick={() => void upload()}
              >
                {busy ? '上传中…' : files.length > 1 ? `导入 ${files.length} 个 jar` : '导入'}
              </button>
            </div>
          </>
        )}
      </div>
    </Modal>
  )
}
