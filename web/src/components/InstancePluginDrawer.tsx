import { useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'

import { formatBytes, formatDate } from '../format'
import type { InstancePlugin } from '../types'
import { useDismiss } from '../useDismiss'
import { loaderLabel } from './PluginBrowse'
import { CompatBadge } from './PluginCompat'

/**
 * Everything one jar says about itself.
 *
 * The table is a list of problems and actions and has no room for prose. But
 * the questions that make an operator open a plugins page at all — what is this
 * thing, who wrote it, what does it need loaded before it — are answered inside
 * the jar, in the same plugin.yml the server reads at startup, and until now the
 * panel read three fields of it and threw the rest away.
 *
 * So it is here, off to the side, where a description can be a paragraph and a
 * dependency list can be a list. The one thing this does that a plain dump of
 * the descriptor would not: it resolves each dependency against the other jars
 * in this directory, so 前置依赖 reads 已装 or 没装 rather than leaving the
 * operator to scroll back and check five names by eye. A missing hard dependency
 * is the reason the plugin will not load, and that sentence is the whole point
 * of showing the list.
 *
 * Read-only on purpose. Every action for this row is a click away in the table
 * behind it, and duplicating 移除 into a panel whose job is "tell me what this
 * is" is how you get someone deleting a plugin they opened to identify.
 */
export function InstancePluginDrawer({
  entry,
  siblings,
  onClose,
  onOpenConfig,
  onOpenConsole,
}: {
  entry: InstancePlugin
  /** Every row of this server's list, so dependencies can be resolved. */
  siblings: InstancePlugin[]
  onClose: () => void
  onOpenConfig: () => void
  onOpenConsole: () => void
}) {
  const { leaving, close } = useDismiss(onClose)
  const closer = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      event.preventDefault()
      close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [close])

  // Focus enters the drawer when it opens; the table behind it hands focus back
  // on close, because it is what unmounts this.
  useEffect(() => {
    closer.current?.focus()
  }, [])

  const jar = entry.jar
  const declared = jar?.name ?? ''
  // Which plugin names this server has, by declared name — that is the name a
  // dependency is written against, not the file name and not the library's.
  const present = new Set(
    siblings
      .filter((row) => !row.missing && row.enabled)
      .map((row) => (row.jar?.name ?? row.name).toLowerCase()),
  )

  return createPortal(
    <div className={`drawer${leaving ? ' drawer--leaving' : ''}`}>
      <div className="drawer__scrim" onClick={close} aria-hidden="true" />
      <aside className="drawer__panel" role="dialog" aria-label={entry.name}>
        <header className="drawer__head">
          <div className="drawer__title">
            <div>
              <h2>{entry.name}</h2>
              <p className="drawer__sub">
                {jar?.version && <span>{jar.version}</span>}
                {jar?.platform && <span>{loaderLabel(jar.platform)}</span>}
                <span>{entry.managed ? '面板安装' : '自行放入'}</span>
                {entry.size > 0 && <span>{formatBytes(entry.size)}</span>}
              </p>
            </div>
          </div>
          <button
            ref={closer}
            className="btn btn--icon"
            onClick={close}
            aria-label="关闭（Esc）"
          >
            ✕
          </button>
        </header>

        <div className="drawer__body">
          {/* Wrapped, because .alert is a wrapping flex row: without a box of
              their own the heading and its explanation lay out side by side
              and break wherever the width happens to run out. */}
          {entry.failure && (
            <div className="alert alert--error">
              <div>
                <strong>加载失败</strong>
                <p className="jar-facts__note">{entry.failure.reason}</p>
                <button className="link" onClick={onOpenConsole}>
                  去控制台看这一段日志
                </button>
              </div>
            </div>
          )}

          {/* Above the descriptor, because a name clash makes every fact below
              it ambiguous: two jars answer to this name and the server picked
              one of them without saying which. */}
          {entry.conflicts && entry.conflicts.length > 0 && (
            <div className="alert alert--warn">
              <div>
                <strong>和别的 jar 重名</strong>
                <p className="jar-facts__note">
                  这些文件都声明自己叫「{declared || entry.name}」，服务端只会加载其中一个，其余的会被拒绝：
                </p>
                <ul className="jar-facts__list">
                  {entry.conflicts.map((path) => (
                    <li key={path}>
                      <code>{path}</code>
                    </li>
                  ))}
                </ul>
                <p className="jar-facts__note">
                  删掉或停用多余的那一个，页面上的版本号才对得上跑着的那份。
                </p>
              </div>
            </div>
          )}

          <section className="drawer__section">
            <h3>简介</h3>
            {jar?.description ? (
              <p className="jar-facts__prose">{jar.description}</p>
            ) : (
              <p className="muted">
                {jar
                  ? '这个 jar 的 plugin.yml 里没写 description。'
                  : '读不到这个 jar 的插件描述文件 —— 它可能不是插件，或者是面板还不认识的格式（比如 Forge 的 TOML）。'}
              </p>
            )}
          </section>

          {jar && (
            <section className="drawer__section">
              <h3>
                声明信息 <span className="muted">插件包里写的</span>
              </h3>
              <dl className="jar-facts">
                <Fact label="插件名" value={jar.name} />
                <Fact label="版本" value={jar.version} />
                <Fact label="作者" value={jar.authors?.join('、')} />
                <Fact label="API 版本" value={jar.apiVersion} />
                <Fact label="平台" value={jar.platform ? loaderLabel(jar.platform) : undefined} />
              </dl>
              {declared && declared !== entry.name && (
                <p className="chart-note">
                  服务端按 <code>{declared}</code> 称呼它，配置目录也叫这个名字 —— 文件名是什么并不影响。
                </p>
              )}
            </section>
          )}

          <DependencySection
            title="前置依赖"
            names={jar?.depend ?? []}
            present={present}
            note="这些没装，服务端启动时这个插件会直接加载失败。"
            required
          />
          <DependencySection
            title="软依赖"
            names={jar?.softDepend ?? []}
            present={present}
            note="装了就用，没装也能启动 —— 但功能可能少一块。"
          />

          <section className="drawer__section">
            <h3>面板知道的</h3>
            <dl className="jar-facts">
              <Fact label="文件" value={`${entry.dir}/${entry.fileName}`} mono />
              <Fact label="大小" value={entry.size > 0 ? formatBytes(entry.size) : undefined} />
              <Fact label="改动时间" value={when(entry.modified)} />
              <Fact label="记录版本" value={entry.managed ? entry.version : undefined} />
              <Fact label="装入时间" value={when(entry.installedAt)} />
              <Fact label="配置目录" value={entry.configDir} mono />
              <Fact label="SHA-256" value={entry.sha256} mono />
            </dl>
            {/* CompatBadge draws nothing for an unknown verdict — see the note
                on it — so the box has to be asked for on the same terms, or it
                is an empty row of padding under every plugin on a server whose
                core the panel has not recognised. */}
            {entry.compat && entry.compat.state !== 'unknown' && (
              <p className="jar-facts__badges">
                <CompatBadge compat={entry.compat} />
              </p>
            )}
          </section>
        </div>

        <footer className="drawer__foot">
          <div className="drawer__foot-info">
            <span className="muted">
              {entry.managed
                ? '这一行的操作在后面的表格里 —— 换版本、回滚、移除。'
                : '面板没有装过它，所以没有版本记录可以切换或回滚。'}
            </span>
          </div>
          <button className="btn" onClick={onOpenConfig} title={entry.configDir}>
            打开配置目录
          </button>
          <button className="btn btn--primary" onClick={close}>
            关闭
          </button>
        </footer>
      </aside>
    </div>,
    document.body,
  )
}

/**
 * A timestamp, or nothing.
 *
 * A Go time.Time that was never set does not disappear from the JSON — it
 * serialises as year 1 — so a jar the panel never installed reports having been
 * installed on 0001-01-01, which is the sort of date that makes an operator
 * wonder what else on the page is made up.
 */
function when(iso?: string): string | undefined {
  if (!iso) return undefined
  const at = Date.parse(iso)
  return Number.isNaN(at) || at <= 0 ? undefined : formatDate(iso)
}

/** One row of a definition list, dropped entirely when there is no value —
 *  a label with a dash beside it is a row that costs a line and says nothing. */
function Fact({ label, value, mono }: { label: string; value?: string; mono?: boolean }) {
  if (!value) return null
  return (
    <>
      <dt>{label}</dt>
      <dd className={mono ? 'jar-facts__mono' : undefined}>{value}</dd>
    </>
  )
}

/**
 * A dependency list with each name answered.
 *
 * Matched against the declared names of the other jars in this directory, which
 * is what the server matches on too. Case-insensitively: authors write `Vault`
 * and `vault` interchangeably and the server does not care either.
 */
function DependencySection({
  title,
  names,
  present,
  note,
  required,
}: {
  title: string
  names: string[]
  present: Set<string>
  note: string
  required?: boolean
}) {
  if (names.length === 0) return null
  const missing = names.filter((name) => !present.has(name.toLowerCase()))

  return (
    <section className="drawer__section">
      <h3>
        {title} <span className="muted">{names.length}</span>
      </h3>
      <ul className="drawer__deps">
        {names.map((name) => {
          const here = present.has(name.toLowerCase())
          return (
            <li key={name}>
              <span>{name}</span>
              <span className={`badge ${here ? 'badge--live' : required ? 'badge--danger' : 'badge--muted'}`}>
                {here ? '已装' : '没装'}
              </span>
            </li>
          )
        })}
      </ul>
      {missing.length > 0 && <p className="chart-note">{note}</p>}
    </section>
  )
}
