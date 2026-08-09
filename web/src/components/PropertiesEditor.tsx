import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import type {
  EulaStatus,
  InstanceStatus,
  KnownProperty,
  PropertiesResponse,
} from '../types'
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
    <form className="stack" onSubmit={save}>
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

      <section className="panel">
        <h3 className="panel__title">常用设置</h3>
        <p className="panel__path">{data.path}</p>

        {known.map((prop) => (
          <PropertyField
            key={prop.key}
            prop={prop}
            value={valueOf(prop)}
            unset={!present.has(prop.key) && !dirty.has(prop.key)}
            onChange={(v) => set(prop.key, v)}
          />
        ))}
      </section>

      {extras.length > 0 && (
        <section className="panel">
          <h3 className="panel__title">其他设置 ({extras.length})</h3>
          <div className="props-grid">
            {extras.map((key) => (
              <label className="field field--inline" key={key}>
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
        <button className="btn btn--primary" type="submit" disabled={busy}>
          保存配置
        </button>
        <button className="btn" type="button" onClick={() => void load()}>
          重新读取
        </button>
      </div>
    </form>
  )
}

function PropertyField({
  prop,
  value,
  unset,
  onChange,
}: {
  prop: KnownProperty
  value: string
  unset: boolean
  onChange: (value: string) => void
}) {
  const hint = [prop.hint, unset ? '当前使用默认值，未写入文件' : null]
    .filter(Boolean)
    .join(' · ')

  if (prop.type === 'boolean') {
    return (
      <label className="checkbox">
        <input
          type="checkbox"
          checked={value === 'true'}
          onChange={(e) => onChange(e.target.checked ? 'true' : 'false')}
        />
        <span>
          {prop.label}
          {hint && <small> — {hint}</small>}
        </span>
      </label>
    )
  }

  return (
    <label className="field">
      <span>{prop.label}</span>
      {prop.type === 'select' ? (
        <select value={value} onChange={(e) => onChange(e.target.value)}>
          {/* An unset key must not silently become the first option. */}
          {!prop.options?.includes(value) && (
            <option value={value}>{value || '(未设置)'}</option>
          )}
          {prop.options?.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
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
  )
}
