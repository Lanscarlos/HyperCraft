import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { toast } from '../toast'
import type { InstanceStatus, ServerConfigFile } from '../types'
import { Badge } from './Badge'
import { Button } from './Button'
import { ConfigLayout, ConfigRow, ConfigSaveBar, changedKeys } from './ConfigLayout'
import { Note } from './Note'
import { PageHead } from './Page'
import { PropertiesEditor } from './PropertiesEditor'
import { Section } from './Section'
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
    // server.properties is not one of the daemon's files — it has its own
    // endpoints and its own editor — so its blurb is written here rather than
    // coming down with the rest.
    { id: PROPERTIES, label: 'server.properties', blurb: '端口、难度、白名单', exists: true },
    ...(files ?? []).map((file) => ({
      id: file.id,
      label: file.label,
      blurb: file.blurb,
      exists: file.exists,
    })),
  ]
  const current = (files ?? []).find((file) => file.id === open)
  const currentTab = tabs.find((tab) => tab.id === open)

  return (
    <div className="stack">
      <PageHead
        title="服务器配置"
        lead="server.properties，以及核心自己的几份配置文件。选一个开始改。"
      />

      {/* Rendered even while the list is loading, so the page does not shift
          under the pointer the moment it arrives.

          The panel's one tab bar, with the selected file's blurb on the line
          under it rather than a line under every tab: the question people
          arrive with is not "which file is called what" but "which file holds
          the thing I want", and one line for the open file answers it without
          five cards' worth of small print in a row. */}
      <div className="tabs" role="tablist" aria-label="配置文件">
        {tabs.map((tab) => (
          <button
            className={`tabs__tab${open === tab.id ? ' tabs__tab--on' : ''}`}
            type="button"
            key={tab.id}
            role="tab"
            aria-selected={open === tab.id}
            onClick={() => setOpen(tab.id)}
          >
            {tab.label}
            {/* The server writes these on first boot. Saying so is the
                difference between "this file is empty" and "this panel is
                broken". */}
            {!tab.exists && <> <Badge tone="muted">未创建</Badge></>}
          </button>
        ))}
      </div>
      {currentTab && <p className="muted">{currentTab.blurb}</p>}

      {error && <div className="alert alert--error">{error}</div>}

      {open === PROPERTIES ? (
        <PropertiesEditor instance={instance} />
      ) : current ? (
        <ServerConfigForm
          instance={instance}
          file={current}
          key={current.id}
          onCreated={() =>
            // Saving into a file the server has not written yet creates it, so
            // the card must stop calling it 未创建 — otherwise the page is
            // telling the operator something they just disproved.
            setFiles((list) =>
              (list ?? []).map((entry) =>
                entry.id === current.id ? { ...entry, exists: true } : entry,
              ),
            )
          }
        />
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
  onCreated,
}: {
  instance: InstanceStatus
  file: ServerConfigFile
  /** The first successful save into a file that did not exist. */
  onCreated: () => void
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
  const [busy, setBusy] = useState(false)
  const [onlyChanged, setOnlyChanged] = useState(false)

  const present = useMemo(
    () => new Set(data.entries.map((entry) => entry.key)),
    [data.entries],
  )

  /** What each key says in the file right now. Absent means the file has no
   *  such line, which is a different thing from an empty value. */
  const original = useMemo(
    () => Object.fromEntries(data.entries.map((entry) => [entry.key, entry.value])),
    [data.entries],
  )

  // What the rows are marked by. `dirty` is add-only on purpose — it decides
  // what gets written, and the comment above says why — but a key typed back
  // to what it already was is not a change, and a badge saying otherwise is
  // the page lying about a diff the operator can read.
  const changed = useMemo(() => changedKeys(values, original), [values, original])

  const adopt = (saved: ServerConfigFile) => {
    setData(saved)
    setValues(Object.fromEntries(saved.entries.map((entry) => [entry.key, entry.value])))
    setDirty(new Set())
  }

  /** Back to what the file says, for every key at once. */
  const discard = () => {
    setValues(Object.fromEntries(data.entries.map((entry) => [entry.key, entry.value])))
    setDirty(new Set())
    setError(null)
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
    try {
      const entries = Object.entries(values)
        .filter(([key]) => dirty.has(key))
        .map(([key, value]) => ({ key, value }))
      if (entries.length === 0) {
        toast('没有修改', { key: 'server-config.save' })
        return
      }
      const saved = await api.saveServerConfig(instance.id, data.id, entries)
      adopt(saved)
      if (saved.exists) onCreated()
      toast('已保存，重启服务器后生效', { key: 'server-config.save' })
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
      <p className="muted">{data.lead}</p>

      {!data.exists && (
        <Note>
          <code>{data.path}</code> 还不存在 —— 服务端首次启动时才会生成它。
          下面显示的是服务端自己的默认值；保存只会写入你改过的那几项，
          剩下的等服务端启动时自己补齐。
        </Note>
      )}

      <ConfigLayout
        groups={grouped.filter((group) => group.settings.length > 0)}
        counts={Object.fromEntries(grouped.map((group) => [group.id, group.settings.length]))}
        changed={changed.size}
        onlyChanged={onlyChanged}
        onToggleOnlyChanged={() => setOnlyChanged((on) => !on)}
        note="面板只写入你改动过的键，其余保持文件原样（含注释与顺序）。这里只列常改的项，整个文件在「文件」页里。"
        path={data.path}
      >
        {grouped.map((group) => {
          const rows = group.settings.filter(
            (setting) => !onlyChanged || changed.has(setting.key),
          )
          if (rows.length === 0) return null
          return (
            <Section className="cfg__group" data-group={group.id} key={group.id} title={group.label} note={group.hint}>
              {rows.map((setting) => (
                <ConfigRow
                  key={setting.key}
                  setting={setting}
                  value={values[setting.key] ?? setting.default}
                  unset={!present.has(setting.key) && !dirty.has(setting.key)}
                  changed={changed.has(setting.key)}
                  original={original[setting.key]}
                  onChange={(value) => set(setting.key, value)}
                />
              ))}
            </Section>
          )
        })}

        {error && <div className="alert alert--error">{error}</div>}

        <div className="actions">
          <Button type="button" onClick={() => void reload()}>
            重新读取
          </Button>
        </div>
      </ConfigLayout>

      <ConfigSaveBar changed={changed.size} busy={busy} onDiscard={discard} />
    </form>
  )
}
