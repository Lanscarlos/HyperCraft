import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { toast, toastWarn } from '../toast'
import type {
  InstanceStatus,
  VelocityResponse,
  VelocitySettingGroup,
  VelocityServer,
  VelocitySetting,
} from '../types'
import { Button } from './Button'
import { EmptyState } from './EmptyState'
import { Note } from './Note'
import { PageHead } from './Page'
import { Section } from './Section'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'
import { Toolbar } from './Toolbar'

/** A row of the sub-server table. `inTry` is this server's membership of the
 *  try list, which is not a table of its own in velocity.toml — it is an array
 *  beside the servers naming which of them a player falls back to. */
interface ServerRow extends VelocityServer {
  /** Stable across renames: a React key that is the name would remount the
   *  input on every keystroke and drop focus after one character. */
  id: number
  inTry: boolean
}

/** A row of the 域名映射 table. Same story as ServerRow: the key has to be
 *  stable across edits or the input remounts and loses focus. */
interface HostRow {
  id: number
  host: string
  servers: string[]
}

let nextRowID = 1

/**
 * Edits velocity.toml — the proxy's answer to 服务器配置.
 *
 * Not a variant of PropertiesEditor: a proxy's configuration is a different
 * file with different keys, and the part of it that matters most has no
 * equivalent on a server at all. 子服务器 is that part, so it is the first
 * panel rather than one setting among forty — everything else here has a
 * working default, and the list of where players actually go does not.
 */
export function VelocityConfig({ instance }: { instance: InstanceStatus }) {
  const [data, setData] = useState<VelocityResponse | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  // Keys the operator actually touched. Only these are sent, so opening the
  // page and saving cannot write forty defaults into a file the proxy has been
  // running happily without.
  const [dirty, setDirty] = useState<Set<string>>(new Set())
  const [present, setPresent] = useState<Set<string>>(new Set())
  const [rows, setRows] = useState<ServerRow[]>([])
  const [hosts, setHosts] = useState<HostRow[]>([])
  const [secret, setSecret] = useState('')
  const [secretDirty, setSecretDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const adopt = (loaded: VelocityResponse) => {
    setData(loaded)
    setValues(Object.fromEntries(loaded.entries.map((entry) => [entry.key, entry.value])))
    setPresent(new Set(loaded.entries.map((entry) => entry.key)))
    setDirty(new Set())
    setRows(
      loaded.servers.map((server) => ({
        ...server,
        id: nextRowID++,
        inTry: loaded.try.includes(server.name),
      })),
    )
    setHosts(
      loaded.forcedHosts.map((entry) => ({
        id: nextRowID++,
        host: entry.host,
        servers: entry.servers,
      })),
    )
    setSecret(loaded.secret.value)
    setSecretDirty(false)
  }

  const load = async () => {
    try {
      adopt(await api.getVelocity(instance.id))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取代理配置失败')
    }
  }

  useEffect(() => {
    void load()
  }, [instance.id])

  const grouped = useMemo(() => {
    const groups: Record<VelocitySettingGroup, VelocitySetting[]> = {
      basic: [],
      forwarding: [],
      advanced: [],
      query: [],
    }
    for (const setting of data?.known ?? []) groups[setting.group].push(setting)
    return groups
  }, [data])

  const set = (key: string, value: string) => {
    setValues((prev) => ({ ...prev, [key]: value }))
    setDirty((prev) => new Set(prev).add(key))
  }

  const valueOf = (setting: VelocitySetting) => values[setting.key] ?? setting.default

  const patchRow = (id: number, patch: Partial<ServerRow>) =>
    setRows((prev) => prev.map((row) => (row.id === id ? { ...row, ...patch } : row)))

  const addRow = (server?: VelocityServer) =>
    setRows((prev) => [
      ...prev,
      {
        id: nextRowID++,
        name: server?.name ?? '',
        address: server?.address ?? '',
        // A proxy with one server and nothing to fall back to is a proxy that
        // kicks everyone with "无法连接到任何服务器", so the first one added is
        // in the try list and the rest follow the operator's decision.
        inTry: prev.length === 0,
      },
    ])

  const moveRow = (index: number, by: number) =>
    setRows((prev) => {
      const target = index + by
      if (target < 0 || target >= prev.length) return prev
      const next = [...prev]
      ;[next[index], next[target]] = [next[target], next[index]]
      return next
    })

  const tryNames = rows.filter((row) => row.inTry && row.name.trim() !== '').map((row) => row.name)

  /** The sub-server names a forced host can point at: whatever is in the table
   *  above right now, since adding a server and routing a domain at it has to
   *  be one save. */
  const serverNames = rows.map((row) => row.name.trim()).filter((name) => name !== '')

  const patchHost = (id: number, patch: Partial<HostRow>) =>
    setHosts((prev) => prev.map((row) => (row.id === id ? { ...row, ...patch } : row)))

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const saved = await api.saveVelocity(instance.id, {
        entries: Object.entries(values)
          .filter(([key]) => dirty.has(key))
          .map(([key, value]) => ({ key, value })),
        servers: rows.map((row) => ({ name: row.name.trim(), address: row.address.trim() })),
        try: tryNames,
        forcedHosts: hosts.map((row) => ({ host: row.host.trim(), servers: row.servers })),
        forwardingSecret: secretDirty ? secret.trim() : '',
      })
      adopt(saved)
      toast('已保存，重启代理端后生效', { key: 'velocity-config.save' })
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const copySecret = async () => {
    try {
      await navigator.clipboard.writeText(secret)
      toast('已复制转发密钥')
    } catch {
      toastWarn('复制失败，手动选中复制吧')
    }
  }

  const head = (
    <PageHead title="代理配置" lead="velocity.toml：子服务器、转发方式和玩家验证。" />
  )

  if (!data) {
    return (
      <div className="stack">
        {head}
        {error ? (
          <div className="alert">{error}</div>
        ) : (
          <SkeletonScreen inPage label="正在读取 velocity.toml…">
            <SkeletonPanel title={false}>
              <Skeleton w="30%" h={15} />
              <Skeleton w="46%" h={12} />
              {Array.from({ length: 6 }, (_, index) => (
                <div className="field" key={index}>
                  <Skeleton w={`${22 + ((index * 29) % 18)}%`} h={12} />
                  <Skeleton w="100%" h={32} />
                </div>
              ))}
            </SkeletonPanel>
          </SkeletonScreen>
        )}
      </div>
    )
  }

  const forwarding = values['player-info-forwarding-mode'] ?? 'none'
  const needsSecret = forwarding === 'modern' || forwarding === 'bungeeguard'
  const taken = new Set(rows.map((row) => row.address.trim()))

  return (
    <form className="stack stack--narrow" onSubmit={save}>
      {head}

      {!data.exists && (
        <Note>
          <code>velocity.toml</code> 还不存在。代理端首次启动会生成它；
          下面填的是 Velocity 的默认值，保存时会直接写成完整的配置文件。
        </Note>
      )}

      <Section
        title="子服务器"
        note={
          <>
            玩家用 <code>/server &lt;名称&gt;</code> 切换过去。地址要写成
            <code> 主机:端口</code>，端口是子服 <code>server.properties</code> 里的{' '}
            <code>server-port</code>。勾上「登录顺序」的会按下面的排列依次尝试。
          </>
        }
      >
        {rows.length === 0 ? (
          <EmptyState inline title="还没有子服。">
            代理端后面一个服务器都没有的话，玩家连上来会立刻被踢。
          </EmptyState>
        ) : (
          <ul className="subservers">
            {rows.map((row, index) => (
              <li className="subserver" key={row.id}>
                <label className="field">
                  <span>名称</span>
                  <input
                    value={row.name}
                    onChange={(e) => patchRow(row.id, { name: e.target.value })}
                    placeholder="lobby"
                    spellCheck={false}
                  />
                </label>
                <label className="field">
                  <span>地址</span>
                  <input
                    value={row.address}
                    onChange={(e) => patchRow(row.id, { address: e.target.value })}
                    placeholder="127.0.0.1:25566"
                    spellCheck={false}
                  />
                </label>
                <label className="checkbox subserver__try">
                  <input
                    type="checkbox"
                    checked={row.inTry}
                    onChange={(e) => patchRow(row.id, { inTry: e.target.checked })}
                  />
                  <span>登录顺序</span>
                </label>
                <div className="subserver__actions">
                  <Button
                    icon
                    size="row"
                    type="button"
                    aria-label={`把 ${row.name || '这一行'} 上移`}
                    disabled={index === 0}
                    onClick={() => moveRow(index, -1)}
                  >
                    ↑
                  </Button>
                  <Button
                    icon
                    size="row"
                    type="button"
                    aria-label={`把 ${row.name || '这一行'} 下移`}
                    disabled={index === rows.length - 1}
                    onClick={() => moveRow(index, 1)}
                  >
                    ↓
                  </Button>
                  <Button
                    icon
                    size="row"
                    type="button"
                    aria-label={`删除 ${row.name || '这一行'}`}
                    onClick={() => setRows((prev) => prev.filter((entry) => entry.id !== row.id))}
                  >
                    ✕
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}

        <div className="actions">
          <Button type="button" onClick={() => addRow()}>
            + 添加子服
          </Button>
        </div>

        {/* The addresses are already on this panel — every other instance wrote
            its own port into its own server.properties. Making the operator go
            and read them off five other pages is the errand this saves. */}
        {data.suggests.length > 0 && (
          <Toolbar className="subserver-picks">
            <span className="toolbar__label">从本机实例添加</span>
            <div className="toolbar__chips">
              {data.suggests.map((suggest) => {
                const already = taken.has(suggest.address)
                return (
                  <button
                    className="chip"
                    type="button"
                    key={suggest.instanceId}
                    disabled={already}
                    title={suggest.address}
                    onClick={() => addRow({ name: suggest.name, address: suggest.address })}
                  >
                    {suggest.instance}
                    <small> · {suggest.address}</small>
                  </button>
                )
              })}
            </div>
          </Toolbar>
        )}
      </Section>

      {/* Right under the sub-servers, because it is about them: a forced host
          is a name for a route into that list, and it is unreadable — and
          unsavable — without the list above it. */}
      <Section
        title="域名映射"
        note={
          <>
            玩家从哪个域名连进来，就直接落到哪个子服。
            <code> creative.example.com</code> 进创造服，主域名进大厅 ——
            对玩家来说像两个服务器，其实是同一个代理端。域名的 DNS 要先指到这台机器。
          </>
        }
      >
        {hosts.length === 0 ? (
          <EmptyState inline title="还没有映射。">
            不填的话所有域名都走上面的登录顺序。
          </EmptyState>
        ) : (
          <ul className="subservers">
            {hosts.map((row) => (
              <li className="subserver" key={row.id}>
                <label className="field">
                  <span>域名</span>
                  <input
                    value={row.host}
                    onChange={(e) => patchHost(row.id, { host: e.target.value })}
                    placeholder="mc.example.com"
                    spellCheck={false}
                  />
                </label>
                <label className="field">
                  <span>落到哪个子服</span>
                  <Select
                    ariaLabel={`${row.host || '这个域名'} 落到哪个子服`}
                    value={row.servers[0] ?? ''}
                    options={[
                      // A name the table no longer has must not silently
                      // become the first server in the list.
                      ...(row.servers[0] && !serverNames.includes(row.servers[0])
                        ? [{ value: row.servers[0], label: `${row.servers[0]}（已不存在）` }]
                        : []),
                      ...(row.servers[0] ? [] : [{ value: '', label: '(未选择)' }]),
                      ...serverNames.map((name) => ({ value: name, label: name })),
                    ]}
                    // Velocity takes a list here — the ones after the first are
                    // fallbacks — and the page offers only the first. The tail
                    // is carried through untouched rather than dropped: a
                    // config written by hand must survive a save it was not
                    // about.
                    onChange={(value) =>
                      patchHost(row.id, { servers: [value, ...row.servers.slice(1)] })
                    }
                  />
                  {row.servers.length > 1 && (
                    <small>连不上时依次尝试：{row.servers.slice(1).join('、')}</small>
                  )}
                </label>
                <div className="subserver__actions">
                  <Button
                    icon
                    size="row"
                    type="button"
                    aria-label={`删除 ${row.host || '这一行'}`}
                    onClick={() => setHosts((prev) => prev.filter((entry) => entry.id !== row.id))}
                  >
                    ✕
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}

        <div className="actions">
          <Button
            type="button"
            disabled={serverNames.length === 0}
            onClick={() =>
              setHosts((prev) => [
                ...prev,
                { id: nextRowID++, host: '', servers: [serverNames[0] ?? ''] },
              ])
            }
          >
            + 添加域名
          </Button>
        </div>
      </Section>

      <Section form title="基本设置" note="代理端监听在哪、对外显示什么。">
        <p className="panel__path">{data.path}</p>
        {grouped.basic.map((setting) => (
          <SettingField
            key={setting.key}
            setting={setting}
            value={valueOf(setting)}
            unset={!present.has(setting.key) && !dirty.has(setting.key)}
            onChange={(value) => set(setting.key, value)}
          />
        ))}
      </Section>

      <Section
        form
        title="玩家信息转发"
        note={
          <>
            子服看到的 IP 和 UUID 从哪来。用 <code>modern</code> 的话，每个子服的{' '}
            <code>paper-global.yml</code> 里要打开 <code>velocity.enabled</code>{' '}
            并填上同一个密钥，同时关掉子服自己的正版验证。
          </>
        }
      >
        {grouped.forwarding.map((setting) => (
          <SettingField
            key={setting.key}
            setting={setting}
            value={valueOf(setting)}
            unset={!present.has(setting.key) && !dirty.has(setting.key)}
            onChange={(value) => set(setting.key, value)}
          />
        ))}

        <label className="field">
          <span>转发密钥</span>
          <input
            value={secret}
            onChange={(e) => {
              setSecret(e.target.value)
              setSecretDirty(true)
            }}
            placeholder={needsSecret ? '点右边生成一个' : '当前转发模式用不到'}
            spellCheck={false}
          />
          <small>
            写在 <code>{data.secret.file}</code> 里，代理端和每个子服必须一致。
          </small>
        </label>

        {/* Outside the label above on purpose: a button inside a label is also
            a click on the label's input, which steals the focus ring away from
            the button that was actually pressed. */}
        <div className="actions">
          <Button
            size="row"
            type="button"
            onClick={() => {
              setSecret(randomSecret())
              setSecretDirty(true)
            }}
          >
            生成随机密钥
          </Button>
          <Button
            size="row"
            type="button"
            disabled={secret === ''}
            onClick={() => void copySecret()}
          >
            复制
          </Button>
        </div>

        {needsSecret && secret.trim() === '' && (
          <Note tone="warn">
            转发模式是 <code>{forwarding}</code>，但还没有密钥。
            密钥为空时 Velocity 会拒绝启动。
          </Note>
        )}
      </Section>

      <Section form title="高级设置" note="默认值适用于绝大多数服；不清楚作用的就别动。">
        {grouped.advanced.map((setting) => (
          <SettingField
            key={setting.key}
            setting={setting}
            value={valueOf(setting)}
            unset={!present.has(setting.key) && !dirty.has(setting.key)}
            onChange={(value) => set(setting.key, value)}
          />
        ))}
      </Section>

      <Section form title="Query" note="给服务器列表查询用的那个端口。">
        {grouped.query.map((setting) => (
          <SettingField
            key={setting.key}
            setting={setting}
            value={valueOf(setting)}
            unset={!present.has(setting.key) && !dirty.has(setting.key)}
            onChange={(value) => set(setting.key, value)}
          />
        ))}
      </Section>

      {error && <div className="alert">{error}</div>}

      <div className="actions">
        <Button variant="primary" type="submit" disabled={busy}>
          保存配置
        </Button>
        <Button type="button" onClick={() => void load()}>
          重新读取
        </Button>
      </div>
    </form>
  )
}

/** A secret Velocity accepts and nobody has to remember: 24 characters out of
 *  the browser's own CSPRNG, no lookalikes to mistype into a sub-server.
 *
 *  Exported because the creation wizard generates one too — a proxy created
 *  with modern forwarding and no secret is a proxy that refuses to start. */
export function randomSecret(): string {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (byte) => alphabet[byte % alphabet.length]).join('')
}

function SettingField({
  setting,
  value,
  unset,
  onChange,
}: {
  setting: VelocitySetting
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
