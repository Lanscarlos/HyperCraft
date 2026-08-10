import { useState } from 'react'

import { api } from '../api'
import { formatDate } from '../format'
import type { InstanceStatus, LibraryPlugin } from '../types'
import { isLive } from '../types'
import { Modal } from './Modal'
import { Select } from './Select'

/**
 * Handing a library plugin to one or more servers.
 *
 * The only place a jar moves from the shared library into a server directory,
 * reached from either end — the plugin list, where the operator is thinking
 * about one plugin across the fleet, and a server's own page, where they are
 * thinking about one server. Both arrive here because the decision is the same
 * one and getting it wrong costs the same thing.
 *
 * A version has to be picked explicitly. "Whichever is newest" is exactly the
 * ambiguity a pinned plugin version exists to remove, and the newest is only
 * preselected because it is right nine times out of ten — not because the
 * choice is a formality.
 */
export function PluginInstallDialog({
  item,
  instances,
  /** Preselected server, when opened from one. */
  preselect,
  onCancel,
  onInstalled,
}: {
  item: LibraryPlugin
  instances: InstanceStatus[]
  preselect?: string
  onCancel: () => void
  onInstalled: (summary: string) => void
}) {
  const [tag, setTag] = useState(item.versions[0]?.tag ?? '')
  const [chosen, setChosen] = useState<string[]>(preselect ? [preselect] : [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const version = item.versions.find((entry) => entry.tag === tag)
  const targets = instances.filter((instance) => chosen.includes(instance.id))
  const live = targets.filter((instance) => isLive(instance.state))

  const install = async () => {
    setBusy(true)
    setError(null)

    // Carries on past a failure rather than stopping at the first: the servers
    // it already reached have the new jar either way, and "one of these
    // failed" without saying which means checking all of them by hand.
    const failures: string[] = []
    let applied = 0
    for (const instance of targets) {
      try {
        await api.installInstancePlugin(instance.id, item.id, tag)
        applied++
      } catch (err) {
        failures.push(`${instance.name}：${err instanceof Error ? err.message : '安装失败'}`)
      }
    }
    setBusy(false)

    if (failures.length > 0) {
      setError(failures.join('；'))
      return
    }
    onInstalled(
      `已把 ${item.name} ${version?.version ?? ''} 装到 ${targets.map((t) => t.name).join('、')}` +
        (live.length > 0 ? '，重启后生效' : ''),
    )
  }

  return (
    <Modal onClose={onCancel} busy={busy} label={`安装 ${item.name}`}>
      <div className="modal__card">
        <h2 className="modal__title">把 {item.name} 装到实例</h2>

        {error && <div className="alert alert--error">{error}</div>}

        {item.versions.length === 0 ? (
          <div className="alert alert--warn">
            插件库里还没有这个插件的 jar —— 先去「获取插件」下载一个版本。
          </div>
        ) : (
          <label className="field">
            <span>版本</span>
            <Select
              ariaLabel="版本"
              value={tag}
              options={item.versions.map((entry) => ({
                value: entry.tag,
                label: `${entry.version}${entry.prerelease ? '（预发布）' : ''}`,
                note: formatDate(entry.publishedAt),
              }))}
              onChange={setTag}
            />
          </label>
        )}

        <div className="field">
          <span>装到哪几台</span>
          {instances.length === 0 ? (
            <p className="muted">面板里还没有实例。</p>
          ) : (
            <div className="pick-list">
              {instances.map((instance) => (
                <label className="pick-list__row" key={instance.id}>
                  <input
                    type="checkbox"
                    checked={chosen.includes(instance.id)}
                    onChange={() =>
                      setChosen((current) =>
                        current.includes(instance.id)
                          ? current.filter((id) => id !== instance.id)
                          : [...current, instance.id],
                      )
                    }
                  />
                  <span className={`status__dot status__dot--${instance.state}`} />
                  <span className="pick-list__name">{instance.name}</span>
                  {item.usedBy.includes(instance.name) && (
                    <span className="badge">已装，会换成这个版本</span>
                  )}
                </label>
              ))}
            </div>
          )}
        </div>

        {live.length > 0 && (
          <div className="alert alert--warn">
            {live.map((instance) => instance.name).join('、')} 正在运行 ——
            jar 会立刻写进去，但要重启才会加载。面板不会自动重启。
          </div>
        )}

        <div className="modal__actions">
          <button className="btn" disabled={busy} onClick={onCancel}>
            取消
          </button>
          <button
            className="btn btn--primary"
            disabled={busy || !tag || targets.length === 0}
            onClick={() => void install()}
          >
            {busy ? '安装中…' : targets.length > 1 ? `装到 ${targets.length} 台` : '安装'}
          </button>
        </div>
      </div>
    </Modal>
  )
}
