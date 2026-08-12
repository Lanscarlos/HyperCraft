import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import type { InstanceStatus, ServerConfigFile, ServerConfigSetting } from '../types'
import { PropertiesEditor } from './PropertiesEditor'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/** The first tab is not a file the daemon lists — it is server.properties,
 *  which has its own endpoints and its own editor. */
const PROPERTIES = 'properties'

/**
 * 服务器配置 as one page with one file open at a time.
 *
 * A server's settings live in four files, not one, and the three that are not
 * server.properties were reachable only through the file manager — which means
 * reading nine hundred lines of YAML to change one boolean. They are here for
 * the same reason server.properties is: the settings worth a control get one,
 * and the rest stay in the file for the editor to handle.
 *
 * One file at a time rather than four stacked panels. Stacked, the page is
 * three screens of form where every heading looks like every other one, and
 * the only way to find 生物生成上限 is to scroll past 提示消息 — while the
 * switch itself answers the question people actually arrive with, which is
 * "which file is this setting in".
 */
export function ServerConfigPage({ instance }: { instance: InstanceStatus }) {
  const [files, setFiles] = useState<ServerConfigFile[] | null>(null)
  const [open, setOpen] = useState<string>(PROPERTIES)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    api
      .getServerConfigs(instance.id)
      .then((loaded) => {
        if (!live) return
        setFiles(loaded.files)
        setError(null)
      })
      .catch((err: unknown) => {
        if (!live) return
        // Not fatal: server.properties is on its own endpoints and still
        // works, so the page degrades to what it was before these existed.
        setError(err instanceof Error ? err.message : '读取配置文件失败')
        setFiles([])
      })
    return () => {
      live = false
    }
  }, [instance.id])

  const tabs = [
    { id: PROPERTIES, label: 'server.properties' },
    ...(files ?? []).map((file) => ({ id: file.id, label: file.label })),
  ]
  const current = (files ?? []).find((file) => file.id === open)

  return (
    <div className="stack">
      {/* Rendered even while the list is loading, so the page does not shift
          under the pointer the moment it arrives. */}
      <div className="configtabs" role="tablist" aria-label="配置文件">
        {tabs.map((tab) => (
          <button
            className={`chip${open === tab.id ? ' chip--active' : ''}`}
            type="button"
            key={tab.id}
            role="tab"
            aria-selected={open === tab.id}
            onClick={() => setOpen(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      {open === PROPERTIES ? (
        <PropertiesEditor instance={instance} />
      ) : current ? (
        <ServerConfigForm instance={instance} file={current} key={current.id} />
      ) : (
        <SkeletonScreen label="正在读取配置文件…">
          <SkeletonPanel title={false}>
            <Skeleton w="30%" h={15} />
            {Array.from({ length: 4 }, (_, index) => (
              <div className="field" key={index}>
                <Skeleton w={`${24 + ((index * 31) % 16)}%`} h={12} />
                <Skeleton w="100%" h={32} />
              </div>
            ))}
          </SkeletonPanel>
        </SkeletonScreen>
      )}
    </div>
  )
}

/** One file's form, grouped the way the daemon grouped it. */
function ServerConfigForm({
  instance,
  file,
}: {
  instance: InstanceStatus
  file: ServerConfigFile
}) {
  const [data, setData] = useState<ServerConfigFile>(file)
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(file.entries.map((entry) => [entry.key, entry.value])),
  )
  // Only the keys somebody actually touched are sent. Opening a page and
  // pressing save must not write forty defaults into a file the server has
  // been running happily without — for 反矿透 or 漏斗事件 that is a behaviour
  // change nobody asked for.
  const [dirty, setDirty] = useState<Set<string>>(new Set())
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const present = useMemo(
    () => new Set(data.entries.map((entry) => entry.key)),
    [data.entries],
  )

  const adopt = (saved: ServerConfigFile) => {
    setData(saved)
    setValues(Object.fromEntries(saved.entries.map((entry) => [entry.key, entry.value])))
    setDirty(new Set())
  }

  const grouped = useMemo(() => {
    return data.groups.map((group) => ({
      ...group,
      settings: data.known.filter((setting) => setting.group === group.id),
    }))
  }, [data])

  const set = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }))
    setDirty((prev) => new Set(prev).add(key))
  }

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    setStatus(null)
    try {
      const entries = Object.entries(values)
        .filter(([key]) => dirty.has(key))
        .map(([key, value]) => ({ key, value }))
      if (entries.length === 0) {
        setStatus('没有修改')
        return
      }
      adopt(await api.saveServerConfig(instance.id, data.id, entries))
      setStatus('已保存，重启服务器后生效')
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const reload = async () => {
    try {
      const loaded = await api.getServerConfigs(instance.id)
      const fresh = loaded.files.find((entry) => entry.id === data.id)
      if (fresh) adopt(fresh)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取失败')
    }
  }

  return (
    <form className="stack" onSubmit={save}>
      <p className="muted">
        {data.lead} 文件在 <code>{data.path}</code>。
      </p>

      {!data.exists && (
        <div className="alert">
          <code>{data.path}</code> 还不存在 —— 服务端首次启动时才会生成它。
          下面显示的是服务端自己的默认值；保存只会写入你改过的那几项，
          剩下的等服务端启动时自己补齐。
        </div>
      )}

      {grouped.map((group) =>
        group.settings.length === 0 ? null : (
          <section className="panel panel--form" key={group.id}>
            <h3 className="panel__title">{group.label}</h3>
            {group.hint && <p className="muted">{group.hint}</p>}
            {group.settings.map((setting) => (
              <ConfigField
                key={setting.key}
                setting={setting}
                value={values[setting.key] ?? setting.default}
                unset={!present.has(setting.key) && !dirty.has(setting.key)}
                onChange={(value) => set(setting.key, value)}
              />
            ))}
          </section>
        ),
      )}

      <p className="muted">
        这里只列了常改的项。整个文件在「文件」页里，改完回来这一页会读到新值。
      </p>

      {error && <div className="alert alert--error">{error}</div>}
      {status && <div className="alert alert--ok">{status}</div>}

      <div className="actions">
        <button className="btn btn--primary" type="submit" disabled={busy}>
          保存配置
        </button>
        <button className="btn" type="button" onClick={() => void reload()}>
          重新读取
        </button>
      </div>
    </form>
  )
}

function ConfigField({
  setting,
  value,
  unset,
  onChange,
}: {
  setting: ServerConfigSetting
  value: string
  unset: boolean
  onChange: (value: string) => void
}) {
  const hint = [setting.hint, unset ? '当前使用默认值，未写入文件' : null]
    .filter(Boolean)
    .join(' · ')

  if (setting.type === 'boolean') {
    return (
      <label className="checkbox">
        <input
          type="checkbox"
          checked={value === 'true'}
          onChange={(e) => onChange(e.target.checked ? 'true' : 'false')}
        />
        <span>
          {setting.label}
          {hint && <small> — {hint}</small>}
        </span>
      </label>
    )
  }

  return (
    <label className="field">
      <span>{setting.label}</span>
      {setting.type === 'select' ? (
        <Select
          ariaLabel={setting.label}
          value={value}
          options={[
            // An unset key must not silently become the first option.
            ...(setting.options?.includes(value) ? [] : [{ value, label: value || '(未设置)' }]),
            ...(setting.options ?? []).map((option) => ({ value: option, label: option })),
          ]}
          onChange={onChange}
        />
      ) : (
        <input
          type={setting.type === 'number' ? 'number' : 'text'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          spellCheck={false}
        />
      )}
      {hint && <small>{hint}</small>}
    </label>
  )
}
