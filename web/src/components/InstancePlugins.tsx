import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { InstancePlugin, InstanceStatus, LibraryPlugin } from '../types'
import { isLive } from '../types'

/**
 * One instance's plugins.
 *
 * Deliberately not a second plugin manager: everything here either takes a
 * copy of something the panel already downloaded, changes which version this
 * server holds, or switches one off. Adding a plugin, defining where it comes
 * from and deleting versions all live in the panel-wide library — see
 * PluginLibraryPage for why that split is the whole design.
 *
 * Jars the panel did not install are listed anyway, and can be switched off,
 * because pretending they are not there is how a server ends up with a plugin
 * nobody can account for.
 */
export function InstancePlugins({
  instance,
  onOpenLibrary,
}: {
  instance: InstanceStatus
  onOpenLibrary: () => void
}) {
  const [entries, setEntries] = useState<InstancePlugin[]>([])
  const [library, setLibrary] = useState<LibraryPlugin[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [changed, setChanged] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const listing = await api.instancePlugins(instance.id)
      setEntries(listing.entries)
      setLibrary(listing.library)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取插件列表失败')
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const act = async (action: () => Promise<unknown>, fallback: string) => {
    setBusy(true)
    setError(null)
    try {
      await action()
      // Every action here changes what the server would load on its next
      // start, never what it is running now.
      setChanged(true)
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
    } finally {
      setBusy(false)
    }
  }

  const installed = entries.filter((entry) => entry.managed)
  const foreign = entries.filter((entry) => !entry.managed)
  const installedIDs = new Set(installed.map((entry) => entry.pluginId))
  // Only plugins with something downloaded can be installed; one that has been
  // added but never fetched has no file to copy.
  const available = library.filter(
    (item) => item.versions.length > 0 && !installedIDs.has(item.id),
  )

  if (loading) {
    return <p className="muted">正在读取插件…</p>
  }

  return (
    <div className="settings">
      {error && <div className="alert alert--error">{error}</div>}
      {changed && isLive(instance.state) && (
        <div className="alert alert--warn">
          插件的增删和启停都要重启服务器才会生效 —— 正在运行的这个进程已经把当前的插件加载进内存了。
        </div>
      )}

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">已安装</h2>
          <p className="chart-head__meta">
            版本和来源都由插件库统一管理，这里只管用哪个、开不开
          </p>
        </div>

        {installed.length === 0 ? (
          <div className="welcome__empty">
            <p>这个实例还没从插件库装过插件。</p>
            <p className="muted">在下面挑一个装上，或者先去插件库添加。</p>
          </div>
        ) : (
          <div className="device-list">
            {installed.map((entry) => (
              <InstalledRow
                key={entry.key}
                entry={entry}
                item={library.find((candidate) => candidate.id === entry.pluginId)}
                busy={busy}
                onToggle={(enabled) =>
                  void act(
                    () => api.setInstancePluginEnabled(instance.id, entry.key, enabled),
                    '切换失败',
                  )
                }
                onSwitch={(tag) =>
                  void act(
                    () => api.installInstancePlugin(instance.id, entry.pluginId ?? '', tag),
                    '切换版本失败',
                  )
                }
                onRemove={() => {
                  if (!window.confirm(`确定要从这个实例移除「${entry.name}」吗？插件自己的配置目录会留着。`)) {
                    return
                  }
                  void act(() => api.uninstallInstancePlugin(instance.id, entry.key), '移除失败')
                }}
              />
            ))}
          </div>
        )}
      </section>

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">从插件库添加</h2>
          <button className="link" onClick={onOpenLibrary}>
            管理插件库
          </button>
        </div>

        {available.length === 0 ? (
          <p className="muted">
            插件库里没有可以装的插件了 ——{' '}
            <button className="link" onClick={onOpenLibrary}>
              去插件库
            </button>{' '}
            添加一个，或者给已添加的插件下载一个版本。
          </p>
        ) : (
          <div className="device-list">
            {available.map((item) => (
              <AvailableRow
                key={item.id}
                item={item}
                busy={busy}
                onInstall={(tag) =>
                  void act(() => api.installInstancePlugin(instance.id, item.id, tag), '安装失败')
                }
              />
            ))}
          </div>
        )}
      </section>

      {foreign.length > 0 && (
        <section className="panel">
          <div className="chart-head">
            <h2 className="panel__title">自行放入的 jar</h2>
            <p className="chart-head__meta">
              不是从插件库装的，面板不知道它的版本和来源，只能开关和删除
            </p>
          </div>
          <div className="device-list">
            {foreign.map((entry) => (
              <div className="device-row" key={entry.key}>
                <div className="device-row__main">
                  <strong>{entry.name}</strong>
                  {!entry.enabled && <span className="badge badge--warn">已停用</span>}
                  <span className="device-row__spacer" />
                  <button
                    className="link"
                    disabled={busy}
                    onClick={() =>
                      void act(
                        () => api.setInstancePluginEnabled(instance.id, entry.key, !entry.enabled),
                        '切换失败',
                      )
                    }
                  >
                    {entry.enabled ? '停用' : '启用'}
                  </button>
                  <button
                    className="link link--danger"
                    disabled={busy}
                    onClick={() => {
                      if (!window.confirm(`确定要删除 ${entry.fileName} 吗？这会直接删掉这个文件。`)) return
                      void act(() => api.uninstallInstancePlugin(instance.id, entry.key), '删除失败')
                    }}
                  >
                    删除
                  </button>
                </div>
                <div className="device-row__meta">
                  {entry.dir}/{entry.fileName} · {formatBytes(entry.size)}
                  {entry.modified && ` · 修改于 ${formatDate(entry.modified)}`}
                </div>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

function InstalledRow({
  entry,
  item,
  busy,
  onToggle,
  onSwitch,
  onRemove,
}: {
  entry: InstancePlugin
  item: LibraryPlugin | undefined
  busy: boolean
  onToggle: (enabled: boolean) => void
  onSwitch: (tag: string) => void
  onRemove: () => void
}) {
  const versions = item?.versions ?? []
  // A version the library no longer holds still has to appear, or the select
  // would silently claim this server is running something it is not.
  const options = versions.some((version) => version.tag === entry.tag)
    ? versions
    : [
        { tag: entry.tag ?? '', version: `${entry.version ?? '未知'}（库里已删除）` },
        ...versions,
      ]

  return (
    <div className="device-row">
      <div className="device-row__main">
        <strong>{entry.name}</strong>
        {entry.missing ? (
          <span className="badge badge--warn">文件不见了</span>
        ) : (
          !entry.enabled && <span className="badge badge--warn">已停用</span>
        )}
        <span className="device-row__spacer" />

        <select
          className="input-slim"
          value={entry.tag ?? ''}
          disabled={busy || versions.length === 0}
          aria-label={`${entry.name} 的版本`}
          onChange={(e) => {
            if (e.target.value !== entry.tag) onSwitch(e.target.value)
          }}
        >
          {options.map((version) => (
            <option key={version.tag} value={version.tag}>
              {version.version}
            </option>
          ))}
        </select>

        <button className="link" disabled={busy || entry.missing} onClick={() => onToggle(!entry.enabled)}>
          {entry.enabled ? '停用' : '启用'}
        </button>
        <button className="link link--danger" disabled={busy} onClick={onRemove}>
          移除
        </button>
      </div>
      <div className="device-row__meta">
        {entry.missing
          ? `记录里是 ${entry.dir}/${entry.fileName}，但文件已经不在了 —— 重新选一次版本就会补回来`
          : `${entry.dir}/${entry.fileName} · ${formatBytes(entry.size)}`}
        {entry.installedAt && ` · 装于 ${formatDate(entry.installedAt)}`}
      </div>
    </div>
  )
}

function AvailableRow({
  item,
  busy,
  onInstall,
}: {
  item: LibraryPlugin
  busy: boolean
  onInstall: (tag: string) => void
}) {
  // Newest first is how the library lists them, so the default is the version
  // an operator almost always wants.
  const [tag, setTag] = useState(item.versions[0]?.tag ?? '')

  return (
    <div className="device-row">
      <div className="device-row__main">
        <strong>{item.name}</strong>
        <span className="device-row__spacer" />
        <select
          className="input-slim"
          value={tag}
          disabled={busy}
          aria-label={`${item.name} 的版本`}
          onChange={(e) => setTag(e.target.value)}
        >
          {item.versions.map((version) => (
            <option key={version.tag} value={version.tag}>
              {version.version}
            </option>
          ))}
        </select>
        <button className="btn" disabled={busy || !tag} onClick={() => onInstall(tag)}>
          安装
        </button>
      </div>
      <div className="device-row__meta">
        {item.source.repo} · 装到 {item.targetDir}/
        {item.versions.length > 1 && ` · 库里有 ${item.versions.length} 个版本`}
      </div>
    </div>
  )
}
