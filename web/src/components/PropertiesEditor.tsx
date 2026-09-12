import { useEffect, useMemo, useRef, useState } from 'react'

import { api } from '../api'
import { reducedMotion } from '../motion'
import type {
  EulaStatus,
  InstanceStatus,
  KnownProperty,
  PropertiesResponse,
} from '../types'
import { Select } from './Select'
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
  const [status, setStatus] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Hides every row that still reads the way it did on disk. Off by default:
  // the page is also how you find a setting you have never touched.
  const [onlyChanged, setOnlyChanged] = useState(false)
  const formRef = useRef<HTMLFormElement>(null)

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
  const changed = useMemo(() => {
    const set = new Set<string>()
    for (const [key, value] of Object.entries(values)) {
      if (value !== original[key]) set.add(key)
    }
    return set
  }, [values, original])

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
    return order.map((name, index) => ({
      id: `cfg-g${index}`,
      name,
      props: byGroup.get(name) ?? [],
    }))
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
    setStatus(null)
    try {
      const entries = Object.entries(values)
        .filter(([key]) => dirty.has(key) || present.has(key))
        .map(([key, value]) => ({ key, value }))

      if (entries.length === 0) {
        setStatus('没有修改')
        return
      }

      const saved = await api.saveProperties(instance.id, entries)
      setData(saved)
      setValues(Object.fromEntries(saved.entries.map((e) => [e.key, e.value])))
      setPresent(new Set(saved.entries.map((e) => e.key)))
      setDirty(new Set())
      setStatus('已保存，重启服务器后生效')
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
    setStatus(null)
    setError(null)
  }

  const jumpTo = (id: string) => {
    const target = formRef.current?.querySelector(`#${id}`)
    target?.scrollIntoView({
      block: 'start',
      behavior: reducedMotion() ? 'auto' : 'smooth',
    })
  }

  const acceptEula = async () => {
    try {
      setEula(await api.acceptEula(instance.id))
    } catch (err) {
      setError(err instanceof Error ? err.message : '写入 eula.txt 失败')
    }
  }

  if (!data) {
    if (error) return <div className="alert alert--error">{error}</div>
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
    <form className="stack" onSubmit={save} ref={formRef}>
      {eula && !eula.accepted && (
        <section className="panel panel--warn">
          <h3 className="panel__title">还没有同意 EULA</h3>
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
          <button className="btn btn--primary" type="button" onClick={acceptEula}>
            我已阅读并同意 EULA
          </button>
        </section>
      )}

      {!data.exists && (
        <div className="alert">
          <code>server.properties</code> 还不存在。
          服务端首次启动会生成它；你现在填的值会在保存时直接写入文件。
        </div>
      )}

      <div className="cfg">
        {/* The rail. Anchors rather than tabs: the sections are one document
            and scrolling between them is how you notice the setting next to
            the one you came for. */}
        <aside className="cfg__rail">
          <button
            type="button"
            className={`cfg__filter${onlyChanged ? ' cfg__filter--on' : ''}`}
            onClick={() => setOnlyChanged((on) => !on)}
            disabled={changed.size === 0}
            aria-pressed={onlyChanged}
          >
            仅看已修改
            <b>{changed.size}</b>
          </button>

          <nav className="cfg__anchors" aria-label="配置分组">
            {groups.map((group) => (
              <button
                type="button"
                className="cfg__anchor"
                key={group.id}
                onClick={() => jumpTo(group.id)}
              >
                <span>{group.name}</span>
                <b>{group.props.length}</b>
              </button>
            ))}
            {extras.length > 0 && (
              <button type="button" className="cfg__anchor" onClick={() => jumpTo('cfg-extras')}>
                <span>未分类</span>
                <b>{extras.length}</b>
              </button>
            )}
          </nav>

          <p className="cfg__note">
            面板只写入你改动过的键，其余保持文件原样（含注释与顺序）。
          </p>
          <p className="cfg__path" title={data.path}>
            {data.path}
          </p>
        </aside>

        <div className="cfg__body">
          {groups.map((group) => {
            const rows = group.props.filter((prop) => !onlyChanged || changed.has(prop.key))
            if (rows.length === 0) return null
            return (
              <section className="panel cfg__group" id={group.id} key={group.id}>
                <h3 className="panel__title">{group.name}</h3>
                {rows.map((prop) => (
                  <PropertyField
                    key={prop.key}
                    prop={prop}
                    value={valueOf(prop)}
                    unset={!present.has(prop.key) && !dirty.has(prop.key)}
                    changed={changed.has(prop.key)}
                    original={original[prop.key]}
                    onChange={(v) => set(prop.key, v)}
                  />
                ))}
              </section>
            )
          })}

          {extras.length > 0 && (!onlyChanged || extras.some((key) => changed.has(key))) && (
            <section className="panel cfg__group" id="cfg-extras">
              <h3 className="panel__title">未分类 ({extras.length})</h3>
              <p className="muted">
                面板不认识的键 —— 模组、插件或者更新的服务端加的。原样可编辑，保存时不会丢。
              </p>
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
            </section>
          )}

          {error && <div className="alert alert--error">{error}</div>}
          {status && <div className="alert alert--ok">{status}</div>}

          <div className="actions">
            <button className="btn" type="button" onClick={() => void load()}>
              重新读取
            </button>
          </div>
        </div>
      </div>

      {/* Rises from the foot of the page while anything is unsaved. The restart
          is stated once, here, rather than as a badge on every row: the server
          reads this file at startup, so it is true of every line in it, and a
          badge that is always on stops being read. */}
      {changed.size > 0 && (
        <div className="cfg__savebar" role="status">
          <span className="cfg__savecount">
            <b>{changed.size}</b> 项更改待保存
          </span>
          <span className="cfg__savenote">重启服务器后生效</span>
          <div className="cfg__saveactions">
            <button className="btn" type="button" onClick={discard} disabled={busy}>
              放弃更改
            </button>
            <button className="btn btn--primary" type="submit" disabled={busy}>
              保存
            </button>
          </div>
        </div>
      )}
    </form>
  )
}

function PropertyField({
  prop,
  value,
  unset,
  changed,
  original,
  onChange,
}: {
  prop: KnownProperty
  value: string
  unset: boolean
  /** True when this row no longer reads the way the file does. */
  changed: boolean
  /** What the file says, or undefined when the file has no such line. */
  original: string | undefined
  onChange: (value: string) => void
}) {
  const hint = [prop.hint, unset ? '当前使用默认值，未写入文件' : null]
    .filter(Boolean)
    .join(' · ')

  // Under the control rather than beside it: the original is read after the
  // new value, as the answer to "what was it before", and putting it in the
  // label turns every changed row into two columns of small text.
  const footnotes = (
    <>
      {changed && (
        <small className="cfg__was">
          原值 <s>{original ?? `（未写入，默认 ${prop.default || '空'}）`}</s>
        </small>
      )}
      {prop.risk && (
        <small className="cfg__risk">
          <span aria-hidden="true">⚠</span> {prop.risk}
        </small>
      )}
      {changed && prop.live && (
        <small className="cfg__live">
          也可以直接在控制台敲 <code>{prop.live}</code> 立即生效，不用等重启。
        </small>
      )}
    </>
  )

  const label = (
    <span className="cfg__name">
      {prop.label}
      <code className="cfg__key">{prop.key}</code>
      {changed && <span className="badge badge--changed">已修改</span>}
    </span>
  )

  if (prop.type === 'boolean') {
    return (
      <div className={`cfg__row${changed ? ' cfg__row--changed' : ''}`}>
        <label className="checkbox">
          <input
            type="checkbox"
            checked={value === 'true'}
            onChange={(e) => onChange(e.target.checked ? 'true' : 'false')}
          />
          {label}
        </label>
        {hint && <small>{hint}</small>}
        {footnotes}
      </div>
    )
  }

  return (
    <div className={`cfg__row${changed ? ' cfg__row--changed' : ''}`}>
      <label className="field">
        {label}
        {prop.type === 'select' ? (
          <Select
            ariaLabel={prop.label}
            value={value}
            options={[
              // An unset key must not silently become the first option.
              ...(prop.options?.includes(value)
                ? []
                : [{ value, label: value || '(未设置)' }]),
              ...(prop.options ?? []).map((option) => ({ value: option, label: option })),
            ]}
            onChange={onChange}
          />
        ) : (
          <input
            type={prop.type === 'number' ? 'number' : 'text'}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            spellCheck={false}
          />
        )}
        {hint && <small>{hint}</small>}
      </label>
      {footnotes}
    </div>
  )
}
