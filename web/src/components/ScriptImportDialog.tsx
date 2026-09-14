import { useState } from 'react'

import { api } from '../api'
import type { LaunchDraft, ParsedScript } from '../types'
import { Button } from './Button'
import { Modal } from './Modal'
import { PathPicker } from './PathPicker'
import { ScriptDraft } from './ScriptDraft'

/**
 * Reading an existing start script for the launch settings inside it.
 *
 * This used to be an input and a button sitting in the middle of the form,
 * which put a control that rewrites five fields under the heading of one of
 * them. A dialog can say what it is about to touch before it touches it, which
 * is the whole reason the read is worth offering: the panel never runs these
 * scripts, so what comes back is a draft somebody has to believe.
 *
 * Three sources, because a script is in one of three places. In the instance's
 * own directory, which is the common case and the default. Somewhere else on
 * the panel's host, for a server being moved in from another layout. Or on the
 * operator's own laptop — read in the browser and posted as text, never
 * uploaded, because a run.sh left in the server directory is a second and
 * stale answer to "how does this start".
 */
type Source = 'instance' | 'host' | 'upload'

const SOURCES: { value: Source; label: string; note: string }[] = [
  { value: 'instance', label: '实例目录里', note: '默认的 run.sh / 启动.sh' },
  { value: 'host', label: '面板本机', note: '这台机器上的其他路径' },
  { value: 'upload', label: '上传文件', note: '你自己电脑上的脚本' },
]

/** Matches the server's own ceiling. Checked here too so a 40 MB pick fails
 *  before it is read into a string and posted. */
const MAX_BYTES = 1 << 20

/** Keeps the file name when a directory is picked, so 浏览… does not throw
 *  away the half of the path the picker cannot choose. */
function withDirectory(path: string, directory: string): string {
  const name = path.trim().split('/').pop() || 'run.sh'
  return `${directory.replace(/\/+$/, '')}/${name}`
}

export function ScriptImportDialog({
  instanceId,
  onApply,
  onClose,
}: {
  instanceId: string
  /** Handed the draft to fill the form with. The dialog does not know which
   *  fields survive that — see LaunchSettings.applyDraft. */
  onApply: (draft: LaunchDraft) => void
  onClose: () => void
}) {
  const [source, setSource] = useState<Source>('instance')
  const [instancePath, setInstancePath] = useState('run.sh')
  const [hostPath, setHostPath] = useState('')
  const [upload, setUpload] = useState<{ name: string; text: string } | null>(null)
  const [picking, setPicking] = useState(false)
  const [parsed, setParsed] = useState<ParsedScript | null>(null)
  const [parsing, setParsing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Switching source throws the draft away. A preview labelled with one file
  // while a different one is selected is the kind of thing somebody confirms
  // without reading.
  const pick = (next: Source) => {
    setSource(next)
    setParsed(null)
    setError(null)
  }

  const ready =
    source === 'instance'
      ? instancePath.trim() !== ''
      : source === 'host'
        ? hostPath.trim() !== ''
        : upload !== null

  const onFile = async (file: File | undefined) => {
    setParsed(null)
    setError(null)
    if (!file) {
      setUpload(null)
      return
    }
    if (file.size > MAX_BYTES) {
      setUpload(null)
      setError('这个文件太大，不像启动脚本。')
      return
    }
    try {
      setUpload({ name: file.name, text: await file.text() })
    } catch {
      setUpload(null)
      setError('读不了这个文件。')
    }
  }

  const read = async () => {
    setParsing(true)
    setParsed(null)
    setError(null)
    try {
      if (source === 'instance') {
        setParsed(await api.parseInstanceScript(instanceId, instancePath.trim()))
      } else if (source === 'host') {
        setParsed(await api.parseHostScript(hostPath.trim()))
      } else if (upload) {
        setParsed(await api.parseInstanceScriptText(instanceId, upload.name, upload.text))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '读不了这个脚本')
    } finally {
      setParsing(false)
    }
  }

  return (
    <Modal onClose={onClose} busy={parsing} label="从启动脚本读参数">
      {(close) => (
        <div className="modal__card modal__card--wide">
          <h2 className="modal__title">从启动脚本读参数</h2>
          <p className="modal__lead">
            面板<strong>不会执行</strong>这个脚本，只把里面的参数读出来给你看。确认之后才填进表单，
            再点保存才真的生效。
          </p>

          <div className="segmented" role="group" aria-label="脚本在哪">
            {SOURCES.map((entry) => (
              <button
                key={entry.value}
                type="button"
                className={`segmented__option${
                  source === entry.value ? ' segmented__option--active' : ''
                }`}
                aria-pressed={source === entry.value}
                onClick={() => pick(entry.value)}
              >
                <strong>{entry.label}</strong>
                <small>{entry.note}</small>
              </button>
            ))}
          </div>

          {source === 'instance' && (
            <label className="field">
              <span>脚本文件名</span>
              <input
                value={instancePath}
                onChange={(event) => {
                  setInstancePath(event.target.value)
                  setParsed(null)
                }}
                placeholder="run.sh"
                spellCheck={false}
                autoFocus
              />
              <small>路径从实例目录算起，比如 <code>run.sh</code>、<code>启动.sh</code>。</small>
            </label>
          )}

          {source === 'host' && (
            <div className="field">
              <span>脚本绝对路径</span>
              <div className="field__with-button">
                <input
                  value={hostPath}
                  onChange={(event) => {
                    setHostPath(event.target.value)
                    setParsed(null)
                  }}
                  placeholder="/opt/minecraft/survival/run.sh"
                  spellCheck={false}
                />
                <Button type="button" onClick={() => setPicking(true)}>
                  浏览…
                </Button>
              </div>
              <small>
                浏览只能选到目录，文件名自己补上——面板读它，不动它。
              </small>
            </div>
          )}

          {source === 'upload' && (
            <div className="field">
              <span>从你的电脑选一个脚本</span>
              <input
                type="file"
                accept=".sh,.bat,.cmd,.txt,text/plain"
                disabled={parsing}
                onChange={(event) => void onFile(event.target.files?.[0])}
              />
              <small>
                <code>.sh</code> 还是 <code>.bat</code> 决定了按哪种语法读，所以文件名别改。
                文件只在浏览器里读一遍，内容发过来解析完就丢——
                <strong>不会存进服务器目录</strong>，免得目录里多出一个面板永远不会执行的脚本。
              </small>
            </div>
          )}

          {error && <div className="alert">{error}</div>}

          <ScriptDraft parsed={parsed} parsing={parsing} mode="settings" />

          <div className="modal__actions">
            <Button type="button" onClick={close} disabled={parsing}>
              取消
            </Button>
            {parsed?.ok ? (
              <Button
                variant="primary"
                type="button"
                onClick={() => {
                  onApply(parsed.draft)
                  close()
                }}
              >
                填进表单
              </Button>
            ) : (
              <Button
                variant="primary"
                type="button"
                disabled={!ready || parsing}
                onClick={read}
              >
                {parsing ? '读取中…' : '读一下'}
              </Button>
            )}
          </div>

          {picking && (
            <PathPicker
              initialPath={hostPath.trim().replace(/\/[^/]*$/, '')}
              onCancel={() => setPicking(false)}
              onPick={(directory) => {
                setHostPath((prev) => withDirectory(prev, directory))
                setParsed(null)
                setPicking(false)
              }}
            />
          )}
        </div>
      )}
    </Modal>
  )
}
