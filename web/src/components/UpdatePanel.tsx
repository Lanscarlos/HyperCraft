import { useState } from 'react'

import { UPDATE_CHANNELS, UPDATE_MIRRORS } from '../types'
import type { UpdateChannel, UpdateShutdown, UpdateStatus } from '../types'
import type { UpdateController } from '../useUpdate'
import { Modal } from './Modal'

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
    const phase = restarting ? 'restarting' : status.phase
    return (
      <section className="update update--busy">
        <h2 className="update__title">正在更新面板</h2>
        <UpdateProgress phase={phase} progress={status.progress} />
        <ShutdownProgress shutdown={status.shutdown} />
        <p className="update__note">
          {restarting
            ? '面板正在重启，页面会在它回来后自动刷新。服务器会随之恢复运行。'
            : phase === 'installing'
              ? '服务器都停好了，正在替换二进制。'
              : '第一步：一边下载校验新版本，一边优雅停止服务器。两件事都完成之后，才会替换二进制并重启。'}
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
          <dd>
            {status.currentVersion}
            {status.currentIsSnapshot && <span className="badge">快照</span>}
          </dd>
        </div>
        <div>
          <dt>最新版本</dt>
          <dd>
            {status.latestVersion ?? '尚未检查'}
            {status.latestIsPrerelease && <span className="badge">快照</span>}
          </dd>
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
          <div className={status.downgrade ? 'alert' : 'alert alert--ok'}>
            {status.downgrade ? (
              <>
                可以回到正式版 <strong>{status.latestVersion}</strong>
                （比当前运行的快照旧，装完就回到正式版轨道）
              </>
            ) : (
              <>
                有新版本 <strong>{status.latestVersion}</strong> 可用
                {status.publishedAt &&
                  `（发布于 ${new Date(status.publishedAt).toLocaleDateString()}）`}
              </>
            )}
          </div>
          {status.latestIsPrerelease && (
            <p className="update__note">
              这是 main 分支的自动构建，通过了 CI 但没有经过发布前的验证。
            </p>
          )}
          {status.releaseNotes && (
            <details className="update__notes">
              <summary>更新内容</summary>
              <pre>{status.releaseNotes}</pre>
            </details>
          )}
          <div className="update__actions">
            <button className="btn btn--primary" onClick={() => setConfirming(true)}>
              {status.downgrade ? '回到' : '立即更新到'} {status.latestVersion}
            </button>
            {status.releaseUrl && (
              <a className="link" href={status.releaseUrl} target="_blank" rel="noreferrer">
                在 GitHub 上查看
              </a>
            )}
          </div>
        </>
      ) : (
        <p className="update__note">
          {status.channel === 'snapshot'
            ? '已经是最新的快照。'
            : '已经是最新版本。'}
        </p>
      )}

      <ChannelPicker
        status={status}
        busy={checking}
        onChange={(next) => void update.setChannel(next)}
      />

      <MirrorPicker
        mirror={status.mirror}
        onChange={(next) => void update.setMirror(next)}
      />

      {confirming && (
        <ConfirmUpdateDialog
          version={status.latestVersion ?? ''}
          prerelease={status.latestIsPrerelease}
          downgrade={status.downgrade}
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

/** Chooses which releases this panel is offered: only the tagged ones, or also
 *  the snapshot built from every green commit on main. */
function ChannelPicker({
  status,
  busy,
  onChange,
}: {
  status: UpdateStatus
  busy: boolean
  onChange: (channel: UpdateChannel) => void
}) {
  const current = UPDATE_CHANNELS.find((c) => c.value === status.channel)

  return (
    <details className="update__mirror" open={status.channel === 'snapshot'}>
      <summary>更新通道：{current?.label ?? status.channel}</summary>

      <div className="update__mirror-body">
        {UPDATE_CHANNELS.map((option) => (
          <label className="checkbox" key={option.value}>
            <input
              type="radio"
              name="update-channel"
              checked={status.channel === option.value}
              disabled={busy}
              onChange={() => onChange(option.value)}
            />
            <span>
              {option.label}
              <small style={{ display: 'block' }}>{option.note}</small>
            </span>
          </label>
        ))}

        <p className="update__note">
          快照通道也会看到正式版 —— 哪个版本号更新就更新到哪个，所以正式版发布后会自动
          从快照回到正式版。
          {status.currentIsSnapshot && status.channel === 'stable' && (
            <>
              {' '}
              当前运行的是快照，切回正式版通道后面板会提示装回最新的正式版，那一步是往回装的。
            </>
          )}
        </p>
      </div>
    </details>
  )
}

/** Chooses the proxy the release archive is downloaded through. GitHub's
 *  release CDN is slow enough from parts of Asia that this is the difference
 *  between a 20-second update and a failed one. */
function MirrorPicker({
  mirror,
  onChange,
}: {
  mirror: string
  onChange: (mirror: string) => void
}) {
  const known = UPDATE_MIRRORS.find((m) => m.value === mirror)
  const [custom, setCustom] = useState(known ? '' : mirror)
  const [editing, setEditing] = useState(!known)

  const selection = editing ? 'custom' : mirror

  return (
    <details className="update__mirror">
      <summary>
        下载源：{known ? known.label : mirror || '直连 GitHub'}
      </summary>

      <div className="update__mirror-body">
        {UPDATE_MIRRORS.map((option) => (
          <label className="checkbox" key={option.value || 'direct'}>
            <input
              type="radio"
              name="update-mirror"
              checked={selection === option.value}
              onChange={() => {
                setEditing(false)
                onChange(option.value)
              }}
            />
            <span>
              {option.label}
              <small style={{ display: 'block' }}>{option.note}</small>
            </span>
          </label>
        ))}

        <label className="checkbox">
          <input
            type="radio"
            name="update-mirror"
            checked={selection === 'custom'}
            onChange={() => setEditing(true)}
          />
          <span>自定义</span>
        </label>

        {editing && (
          <div className="update__mirror-custom">
            <input
              value={custom}
              onChange={(e) => setCustom(e.target.value)}
              placeholder="https://example.com/"
              aria-label="自定义镜像源"
            />
            <button className="btn" onClick={() => onChange(custom.trim())}>
              保存
            </button>
          </div>
        )}

        <p className="update__note">
          镜像只用来下载压缩包。校验用的 <code>SHA256SUMS.txt</code> 优先从 GitHub
          直接取，所以镜像换不掉二进制 —— 它给的包对不上 GitHub 的哈希就会被拒绝。
          只有 GitHub 完全连不上时才会退而从镜像取校验文件，那种情况下这一次更新等于
          信任镜像，面板日志里会记一条警告。检查更新本身始终直连（这些镜像不代理
          api.github.com）。
        </p>
      </div>
    </details>
  )
}

function UpdateProgress({ phase, progress }: { phase: string; progress: number }) {
  const labels: Record<string, string> = {
    downloading: `下载中 ${progress}%`,
    stopping: '下载完成，正在等服务器停好…',
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

/**
 * The other half of step one.
 *
 * The download has a byte count and so gets the bar; the shutdown has a list of
 * servers, and which one the update is still waiting on is the only question
 * anyone asks while watching this — a world that takes two minutes to save is
 * the difference between "it's working" and "it's stuck".
 */
function ShutdownProgress({ shutdown }: { shutdown?: UpdateShutdown }) {
  if (!shutdown || shutdown.total === 0) return null
  const pending = shutdown.pending ?? []

  return (
    <p className="update__note">
      服务器 {shutdown.stopped}/{shutdown.total} 已停止
      {pending.length > 0 ? ` · 正在等待 ${pending.join('、')} 存档退出` : ' · 都停好了'}
    </p>
  )
}

function ConfirmUpdateDialog({
  version,
  prerelease,
  downgrade,
  runningNames,
  onCancel,
  onConfirm,
}: {
  version: string
  prerelease: boolean
  downgrade: boolean
  runningNames: string[]
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <Modal onClose={onCancel}>
      <div className="modal__card">
        <h2 className="modal__title">
          {downgrade ? '回到' : '更新到'} {version}
        </h2>
        <p className="modal__lead">
          更新分两步：先<strong>一边下载校验新版本、一边优雅停止服务器</strong>，
          等服务器全部正常停下之后，才替换二进制并重启面板。任何一步失败都不会替换文件，
          停掉的服务器会被重新拉起来。
        </p>

        {prerelease && (
          <p className="modal__lead">
            <strong>{version} 是快照，不是正式发布版本。</strong>
            它由 main 分支的提交自动构建，通过了 CI，但没有经过发布前的验证。
          </p>
        )}
        {downgrade && (
          <p className="modal__lead">
            这一步会把面板从快照装回正式版，也就是<strong>版本号往回走</strong>。
            如果快照写过正式版还不认识的配置，请先备份 <code>data</code> 目录。
          </p>
        )}

        {runningNames.length > 0 ? (
          <>
            <p className="modal__lead">
              下面 {runningNames.length} 个服务器会在下载开始的同时被<strong>优雅停止</strong>
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
    </Modal>
  )
}
