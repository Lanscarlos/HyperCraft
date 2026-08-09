import { useState } from 'react'

import type { UpdateController } from '../useUpdate'

interface Props {
  update: UpdateController
  /** Servers that are up right now, and so will be stopped and brought back. */
  runningNames: string[]
}

/** Panel self-update: current version, what is available, and the one button
 *  that installs it. */
export function UpdatePanel({ update, runningNames }: Props) {
  const [confirming, setConfirming] = useState(false)
  const { status, updating, restarting, error, checking } = update

  if (!status) return null

  if (updating) {
    return (
      <section className="update update--busy">
        <h2 className="update__title">正在更新面板</h2>
        <UpdateProgress
          phase={restarting ? 'restarting' : status.phase}
          progress={status.progress}
        />
        <p className="update__note">
          {restarting
            ? '面板正在重启，页面会在它回来后自动刷新。服务器会随之恢复运行。'
            : '正在下载并校验新版本，此时服务器仍在正常运行。'}
        </p>
      </section>
    )
  }

  return (
    <section className="update">
      <div className="update__head">
        <h2 className="update__title">面板更新</h2>
        <button className="btn" onClick={() => void update.check()} disabled={checking}>
          {checking ? '检查中…' : '检查更新'}
        </button>
      </div>

      <dl className="update__meta">
        <div>
          <dt>当前版本</dt>
          <dd>{status.currentVersion}</dd>
        </div>
        <div>
          <dt>最新版本</dt>
          <dd>{status.latestVersion ?? '尚未检查'}</dd>
        </div>
        <div>
          <dt>上次检查</dt>
          <dd>{status.checkedAt ? new Date(status.checkedAt).toLocaleString() : '—'}</dd>
        </div>
      </dl>

      {error && <div className="alert alert--error">{error}</div>}
      {status.checkError && !error && (
        <div className="alert alert--error">检查更新失败：{status.checkError}</div>
      )}

      {!status.eligible ? (
        <p className="update__note">{status.ineligibleWhy}</p>
      ) : status.updateAvailable ? (
        <>
          <div className="alert alert--ok">
            有新版本 <strong>{status.latestVersion}</strong> 可用
            {status.publishedAt && `（发布于 ${new Date(status.publishedAt).toLocaleDateString()}）`}
          </div>
          {status.releaseNotes && (
            <details className="update__notes">
              <summary>更新内容</summary>
              <pre>{status.releaseNotes}</pre>
            </details>
          )}
          <div className="update__actions">
            <button className="btn btn--primary" onClick={() => setConfirming(true)}>
              立即更新到 {status.latestVersion}
            </button>
            {status.releaseUrl && (
              <a className="link" href={status.releaseUrl} target="_blank" rel="noreferrer">
                在 GitHub 上查看
              </a>
            )}
          </div>
        </>
      ) : (
        <p className="update__note">已经是最新版本。</p>
      )}

      {confirming && (
        <ConfirmUpdateDialog
          version={status.latestVersion ?? ''}
          runningNames={runningNames}
          onCancel={() => setConfirming(false)}
          onConfirm={() => {
            setConfirming(false)
            void update.apply()
          }}
        />
      )}
    </section>
  )
}

function UpdateProgress({ phase, progress }: { phase: string; progress: number }) {
  const labels: Record<string, string> = {
    downloading: `下载中 ${progress}%`,
    installing: '正在安装…',
    restarting: '正在重启面板…',
  }
  // The restart has no measurable progress, so the bar is pinned full rather
  // than stuck at whatever the download ended on.
  const width = phase === 'downloading' ? progress : 100
  return (
    <div className="progress">
      <div className="progress__bar" style={{ width: `${width}%` }} />
      <span className="progress__label">{labels[phase] ?? '准备中…'}</span>
    </div>
  )
}

function ConfirmUpdateDialog({
  version,
  runningNames,
  onCancel,
  onConfirm,
}: {
  version: string
  runningNames: string[]
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <div className="modal" role="dialog" aria-modal="true">
      <div className="modal__card">
        <h2 className="modal__title">更新到 {version}</h2>
        <p className="modal__lead">
          面板会先下载并校验新版本，成功后才停止服务器并重启自己。下载失败不会影响正在运行的服务器。
        </p>

        {runningNames.length > 0 ? (
          <>
            <p className="modal__lead">
              下面 {runningNames.length} 个服务器会被<strong>优雅停止</strong>
              （执行 stop、等待存档），面板重启后自动恢复运行：
            </p>
            <ul className="update__list">
              {runningNames.map((name) => (
                <li key={name}>{name}</li>
              ))}
            </ul>
            <p className="modal__lead">
              期间玩家会掉线，请先确认没有人正在游戏中。
            </p>
          </>
        ) : (
          <p className="modal__lead">当前没有服务器在运行，更新不会中断任何人。</p>
        )}

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel}>
            取消
          </button>
          <button className="btn btn--primary" type="button" onClick={onConfirm}>
            确认更新
          </button>
        </div>
      </div>
    </div>
  )
}
