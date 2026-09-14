import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { toast } from '../toast'
import type {
  EulaStatus,
  InstanceStatus,
  KnownProperty,
  PropertiesResponse,
} from '../types'
import { Button } from './Button'
import { ConfigLayout, ConfigRow, ConfigSaveBar, changedKeys } from './ConfigLayout'
import { Note } from './Note'
import { Section } from './Section'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/**
 * Edits server.properties.
 *
 * Well-known keys get a proper control; everything else is listed as a plain
 * text row so modded or future keys stay editable instead of invisible. Saving
 * never deletes keys the panel did not render.
 */
export function PropertiesEditor({ instance }: { instance: InstanceStatus }) {
  const [data, setData] = useState<PropertiesResponse | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  // Keys the operator actually touched. Saving is limited to these plus the
  // keys already in the file, so opening the tab and hitting save cannot write
  // out a wall of defaults — which for online-mode or pvp would quietly flip
  // the server's behaviour from what vanilla does.
  const [dirty, setDirty] = useState<Set<string>>(new Set())
  const [present, setPresent] = useState<Set<string>>(new Set())
  const [eula, setEula] = useState<EulaStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Hides every row that still reads the way it did on disk. Off by default:
  // the page is also how you find a setting you have never touched.
  const [onlyChanged, setOnlyChanged] = useState(false)

  const load = async () => {
    try {
      const [props, eulaStatus] = await Promise.all([
        api.getProperties(instance.id),
        api.getEula(instance.id),
      ])
      setData(props)
      setEula(eulaStatus)
      setValues(Object.fromEntries(props.entries.map((e) => [e.key, e.value])))
      setPresent(new Set(props.entries.map((e) => e.key)))
      setDirty(new Set())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取配置失败')
    }
  }

  useEffect(() => {
    void load()
  }, [instance.id])

  const { known, extras } = useMemo(() => {
    if (!data) return { known: [] as KnownProperty[], extras: [] as string[] }
    const knownKeys = new Set(data.known.map((k) => k.key))
    return {
      known: data.known,
      extras: data.entries.map((e) => e.key).filter((k) => !knownKeys.has(k)),
    }
  }, [data])

  /** What each key says on disk right now. Absent from this map means the file
   *  has no such line, which is a different thing from an empty value. */
  const original = useMemo(
    () => Object.fromEntries((data?.entries ?? []).map((e) => [e.key, e.value])),
    [data],
  )

  // What the rows are actually marked by. `dirty` is add-only on purpose — it
  // decides what gets written, and its comment above says why — but a key
  // typed back to what it already was is not a change, and a badge saying
  // otherwise is the page lying about a diff the operator can read.
  const changed = useMemo(() => changedKeys(values, original), [values, original])

  /** The rail, and the sections under it, in the order the daemon lists the
   *  keys — which is the order they were grouped in, not alphabetical. */
  const groups = useMemo(() => {
    const order: string[] = []
    const byGroup = new Map<string, KnownProperty[]>()
    for (const prop of known) {
      const name = prop.group || '未分类'
      if (!byGroup.has(name)) {
        byGroup.set(name, [])
        order.push(name)
      }
      byGroup.get(name)?.push(prop)
    }
    return order.map((name) => ({ id: name, label: name, props: byGroup.get(name) ?? [] }))
  }, [known])


  const set = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }))
    setDirty((prev) => new Set(prev).add(key))
  }

  /** Falls back to Minecraft's own default so an absent key displays honestly. */
  const valueOf = (prop: KnownProperty) => values[prop.key] ?? prop.default

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const entries = Object.entries(values)
        .filter(([key]) => dirty.has(key) || present.has(key))
        .map(([key, value]) => ({ key, value }))

      if (entries.length === 0) {
        toast('没有修改', { key: 'properties-editor.save' })
        return
      }

      const saved = await api.saveProperties(instance.id, entries)
      setData(saved)
      setValues(Object.fromEntries(saved.entries.map((e) => [e.key, e.value])))
      setPresent(new Set(saved.entries.map((e) => e.key)))
      setDirty(new Set())
      toast('已保存，重启服务器后生效', { key: 'properties-editor.save' })
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  /** Back to what the file says, for every key at once. */
  const discard = () => {
    if (!data) return
    setValues(Object.fromEntries(data.entries.map((e) => [e.key, e.value])))
    setDirty(new Set())
    setError(null)
  }

  const acceptEula = async () => {
    try {
      setEula(await api.acceptEula(instance.id))
    } catch (err) {
      setError(err instanceof Error ? err.message : '写入 eula.txt 失败')
    }
  }

  if (!data) {
    if (error) return <div className="alert">{error}</div>
    return (
      <SkeletonScreen label="正在读取 server.properties…">
        <SkeletonPanel title={false}>
          <Skeleton w="30%" h={15} />
          <Skeleton w="46%" h={12} />
          {/* A form is label-then-control down the page, and each pair is
              taller than a line of prose — a stack of even bars would be the
              wrong height and put the first field back where it started. */}
          {Array.from({ length: 6 }, (_, index) => (
            <div className="field" key={index}>
              <Skeleton w={`${22 + ((index * 29) % 18)}%`} h={12} />
              <Skeleton w="100%" h={32} />
            </div>
          ))}
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  return (
    <form className="stack" onSubmit={save}>
      {eula && !eula.accepted && (
        <Section tone="warn" title="还没有同意 EULA">
          <p>
            Minecraft 服务端在 <code>eula.txt</code> 里写入{' '}
            <code>eula=true</code> 之前不会启动。请先阅读{' '}
            <a
              href="https://aka.ms/MinecraftEULA"
              target="_blank"
              rel="noreferrer"
            >
              Minecraft 最终用户许可协议
            </a>
            ，同意后点击下面的按钮。
          </p>
          <Button variant="primary" type="button" onClick={acceptEula}>
            我已阅读并同意 EULA
          </Button>
        </Section>
      )}

      {!data.exists && (
        <Note>
          <code>server.properties</code> 还不存在。
          服务端首次启动会生成它；你现在填的值会在保存时直接写入文件。
        </Note>
      )}

      <ConfigLayout
        groups={[
          ...groups.map((group) => ({ id: group.id, label: group.label })),
          ...(extras.length > 0 ? [{ id: 'cfg-extras', label: '未分类' }] : []),
        ]}
        counts={{
          ...Object.fromEntries(groups.map((group) => [group.id, group.props.length])),
          'cfg-extras': extras.length,
        }}
        changed={changed.size}
        onlyChanged={onlyChanged}
        onToggleOnlyChanged={() => setOnlyChanged((on) => !on)}
        note="面板只写入你改动过的键，其余保持文件原样（含注释与顺序）。"
        path={data.path}
      >
        {groups.map((group) => {
          const rows = group.props.filter((prop) => !onlyChanged || changed.has(prop.key))
          if (rows.length === 0) return null
          return (
            <Section className="cfg__group" data-group={group.id} key={group.id} title={group.label}>
              {rows.map((prop) => (
                <ConfigRow
                  key={prop.key}
                  setting={prop}
                  value={valueOf(prop)}
                  unset={!present.has(prop.key) && !dirty.has(prop.key)}
                  changed={changed.has(prop.key)}
                  original={original[prop.key]}
                  onChange={(v) => set(prop.key, v)}
                />
              ))}
            </Section>
          )
        })}

        {extras.length > 0 && (!onlyChanged || extras.some((key) => changed.has(key))) && (
          <Section
            className="cfg__group"
            data-group="cfg-extras"
            title="未分类"
            count={extras.length}
            note="面板不认识的键 —— 模组、插件或者更新的服务端加的。原样可编辑，保存时不会丢。"
          >
            <div className="props-grid">
              {extras
                .filter((key) => !onlyChanged || changed.has(key))
                .map((key) => (
                  <label
                    className={`field field--inline${changed.has(key) ? ' field--changed' : ''}`}
                    key={key}
                  >
                    <span title={key}>{key}</span>
                    <input
                      value={values[key] ?? ''}
                      onChange={(e) => set(key, e.target.value)}
                      spellCheck={false}
                    />
                  </label>
                ))}
            </div>
          </Section>
        )}

        {error && <div className="alert">{error}</div>}

        <div className="actions">
          <Button type="button" onClick={() => void load()}>
            重新读取
          </Button>
        </div>
      </ConfigLayout>

      <ConfigSaveBar changed={changed.size} busy={busy} onDiscard={discard} />
    </form>
  )
}
