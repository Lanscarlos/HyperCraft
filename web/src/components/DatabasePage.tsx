import { useEffect, useState } from 'react'

import { ask, askWithToggle } from '../confirm'
import { formatBytes, formatDate } from '../format'
import type { LibraryView } from '../routes'
import { toast } from '../toast'
import type {
  DatabaseEngine,
  DatabaseInstall,
  DatabaseInstallJob,
  DatabaseService,
} from '../types'
import type { DatabaseController } from '../useDatabases'
import { Page } from './Page'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'

/** Named because the page renders it before its data arrives as well as after,
 *  and the two have to be the same string or the page moves when it loads. */
const DB_LEAD =
  '不少插件要数据库：LuckPerms 存权限、CoreProtect 存日志、Plan 存统计。这里装的数据库归面板所有，不动系统里的服务，建好之后把连接串复制到插件配置里就行。'

const TITLES: Partial<Record<LibraryView, string>> = {
  databases: '数据库环境',
  engines: '已装引擎',
  install: '安装引擎',
}

const LEADS: Partial<Record<LibraryView, string>> = {
  databases: DB_LEAD,
  engines: '引擎是数据库程序本身，装一次可以给多个数据库共用。删掉引擎不会动数据，但跑在上面的数据库会起不来。',
  install: '从官方渠道下载数据库程序。下载走服务器自己的网络，关掉网页也会继续。',
}

const STATE_LABELS: Record<DatabaseService['state'], string> = {
  stopped: '已停止',
  starting: '启动中',
  running: '运行中',
  stopping: '停止中',
  failed: '启动失败',
}

/**
 * Panel-wide database management: install an engine, set a database up on it,
 * start it, and copy the connection string into a plugin's config.
 *
 * Its own section rather than part of an instance for the same reason Java
 * runtimes are their own: an engine is shared, and a database usually is too —
 * one MySQL serving four servers is the normal shape, not four MySQLs.
 *
 * Two things live here that the Java page has no equivalent of, and both are
 * the reason this is 「搭建」 rather than 「下载」: a database is a *process* the
 * panel starts and stops, and it has *credentials* the operator has to be able
 * to read back.
 */
export function DatabasePage({
  databases,
  view,
  onOpenView,
}: {
  databases: DatabaseController
  view: LibraryView
  onOpenView: (view: LibraryView) => void
}) {
  const { overview, job, installing, busy } = databases

  if (!overview) {
    // The heading and the lead are constants, not data — showing them straight
    // away means the page opens with its own name on it, and only what was
    // actually being fetched arrives later.
    return (
      <Page wide title={TITLES[view] ?? '数据库环境'} lead={DB_LEAD}>
        <SkeletonScreen inPage label="正在读取数据库…">
          <SkeletonPanel title={false}>
            <div className="chart-head">
              <Skeleton w="72px" h={15} />
              <Skeleton w="180px" h={12} />
            </div>
            <SkeletonRows rows={2} />
          </SkeletonPanel>
        </SkeletonScreen>
      </Page>
    )
  }

  const { installs, services, engines, platform } = overview
  const totalSize = installs.reduce((sum, install) => sum + install.size, 0)
  const live = services.filter((service) => service.state === 'running').length

  return (
    <Page
      wide
      title={TITLES[view] ?? '数据库环境'}
      lead={LEADS[view] ?? DB_LEAD}
      aside={
        <p className="meta-chips">
          {platform.os && (
            <span>
              {platform.os}/{platform.arch}
            </span>
          )}
          <span>{services.length} 个数据库</span>
          {live > 0 && <span>{live} 个运行中</span>}
          {installs.length > 0 && <span>引擎共 {formatBytes(totalSize)}</span>}
        </p>
      }
    >
      {platform.warning && <div className="alert alert--error">{platform.warning}</div>}
      {databases.error && <div className="alert alert--error">{databases.error}</div>}

      {/* An install keeps running after you navigate away, so it is reported on
          whichever of these pages you happen to be looking at. */}
      {view !== 'install' && job && (job.state === 'downloading' || job.state === 'extracting') && (
        <InstallStatus job={job} engines={engines} />
      )}

      {view === 'databases' && (
        <ServiceList
          databases={databases}
          services={services}
          installs={installs}
          engines={engines}
          onOpenView={onOpenView}
        />
      )}

      {view === 'engines' && (
        <EngineList
          databases={databases}
          installs={installs}
          engines={engines}
          onOpenView={onOpenView}
        />
      )}

      {view === 'install' && (
        <InstallEngine databases={databases} engines={engines} busy={busy || installing} />
      )}
    </Page>
  )
}

// ------------------------------------------------------------------ services

function ServiceList({
  databases,
  services,
  installs,
  engines,
  onOpenView,
}: {
  databases: DatabaseController
  services: DatabaseService[]
  installs: DatabaseInstall[]
  engines: DatabaseEngine[]
  onOpenView: (view: LibraryView) => void
}) {
  const [creating, setCreating] = useState(false)
  const usable = installs.filter((install) => !install.problem)

  const remove = async (service: DatabaseService) => {
    // One card with a checkbox rather than two questions: whether the data goes
    // is part of this decision, not a follow-up to it.
    const answer = await askWithToggle({
      title: `删除数据库「${service.name}」？`,
      lead: '会从面板里移除这个数据库。',
      detail: `数据目录是 ${service.dir}。不勾下面那项就只是从列表里去掉，目录留在磁盘上，以后还能靠它恢复。`,
      confirmLabel: '删除',
      danger: true,
      toggle: {
        label: '连数据一起删掉',
        note: `${service.database} 里的所有数据都会没有 —— 插件存的权限、日志、统计一并消失，删了就找不回来。`,
        initial: false,
      },
    })
    if (!answer.ok) return
    await databases.remove(service.id, answer.toggled)
  }

  return (
    <>
      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">我的数据库</h2>
          <p className="chart-head__meta">
            {services.length > 0 ? `面板管理 ${services.length} 个` : '还没有建过数据库'}
          </p>
        </div>

        {services.length === 0 ? (
          <div className="welcome__empty">
            {usable.length === 0 ? (
              <>
                <p>还没有装数据库引擎，建不了数据库。</p>
                <p className="muted">
                  <button className="link" type="button" onClick={() => onOpenView('install')}>
                    先装一个引擎
                  </button>
                  ，MySQL 的精简包只有 60 MB 左右，装完就能建库。
                </p>
              </>
            ) : (
              <>
                <p>引擎装好了，还没有建过数据库。</p>
                <p className="muted">
                  <button className="link" type="button" onClick={() => setCreating(true)}>
                    建一个
                  </button>
                  ，端口、账号、密码面板都会给默认值，建完直接复制连接串。
                </p>
              </>
            )}
          </div>
        ) : (
          <div className="asset-list">
            {services.map((service) => (
              <ServiceRow
                key={service.id}
                service={service}
                busy={databases.busy}
                onStart={() => void databases.start(service.id)}
                onStop={() => void databases.stop(service.id)}
                onRemove={() => void remove(service)}
                onToggleAutoStart={() =>
                  void databases.update(service.id, { autoStart: !service.autoStart })
                }
              />
            ))}
          </div>
        )}

        {usable.length > 0 && (
          <div className="actions">
            <button
              className="btn btn--primary"
              type="button"
              disabled={databases.busy || creating}
              onClick={() => setCreating(true)}
            >
              新建数据库
            </button>
            <span className="muted">
              一个引擎可以建多个数据库，端口面板会自动错开。
            </span>
          </div>
        )}
      </section>

      {creating && (
        <CreateForm
          databases={databases}
          installs={usable}
          engines={engines}
          onDone={() => setCreating(false)}
        />
      )}
    </>
  )
}

function ServiceRow({
  service,
  busy,
  onStart,
  onStop,
  onRemove,
  onToggleAutoStart,
}: {
  service: DatabaseService
  busy: boolean
  onStart: () => void
  onStop: () => void
  onRemove: () => void
  onToggleAutoStart: () => void
}) {
  const running = service.state === 'running'
  const moving = service.state === 'starting' || service.state === 'stopping'

  return (
    <article className="asset">
      <div className="asset__head">
        <span className={`asset__tile${running ? ' asset__tile--accent' : ''}`}>
          {service.engine.slice(0, 2).toUpperCase()}
        </span>
        <div className="asset__title">
          <span className="asset__label">
            <strong>{service.name}</strong>
            <span className="badge">{service.version}</span>
            {running && <span className="badge badge--live">运行中</span>}
            {service.state === 'failed' && <span className="badge badge--update">启动失败</span>}
            {service.missing && <span className="badge badge--update">引擎已删除</span>}
          </span>
          <span className="asset__sub">
            <span>
              {STATE_LABELS[service.state]}
              {service.autoStart && ' · 跟随面板启动'}
            </span>
            <code title={service.dir}>{service.dir}</code>
          </span>
        </div>
      </div>

      {service.error && <div className="alert alert--error">{service.error}</div>}

      <dl className="asset__facts asset__facts--split">
        <div>
          <dt>监听</dt>
          <dd>
            {service.bind}:{service.port}
          </dd>
        </div>
        <div>
          <dt>库名</dt>
          <dd>{service.database}</dd>
        </div>
        <div>
          <dt>建于</dt>
          <dd>{formatDate(service.createdAt)}</dd>
        </div>
      </dl>

      <Connection service={service} />

      <footer className="asset__actions asset__actions--split">
        <label className="check">
          <input
            type="checkbox"
            checked={service.autoStart}
            disabled={busy}
            onChange={onToggleAutoStart}
          />
          <span>面板启动时自动开</span>
        </label>
        <span className="asset__users">
          {running || moving ? (
            <button className="link" disabled={busy || moving} onClick={onStop}>
              {service.state === 'stopping' ? '停止中…' : '停止'}
            </button>
          ) : (
            <button
              className="link"
              disabled={busy || service.missing}
              title={service.missing ? '这个数据库的引擎已经被删掉了，重新装一个同版本的即可' : undefined}
              onClick={onStart}
            >
              启动
            </button>
          )}
          <button className="link link--danger" disabled={busy || running || moving} onClick={onRemove}>
            删除
          </button>
        </span>
      </footer>
    </article>
  )
}

/**
 * The connection details, which are the reason this whole page exists.
 *
 * The password is shown rather than masked. It is not a secret the panel is
 * keeping from the operator — every plugin that uses it will store the same
 * string in its own config file — and a masked field they have to reveal before
 * every copy would be theatre with a cost.
 */
function Connection({ service }: { service: DatabaseService }) {
  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast(`已复制${label}`)
    } catch {
      // Clipboard access needs a secure context, and a panel reached over plain
      // HTTP on a LAN address is not one. The value is on screen either way.
      toast('复制失败，手动选中复制吧')
    }
  }

  return (
    <div className="field">
      <div className="field__head">
        <span>连接信息</span>
        <button className="link" type="button" onClick={() => void copy('连接串', service.uri)}>
          复制连接串
        </button>
      </div>
      <code className="asset__conn">{service.uri}</code>
      {service.jdbc && (
        <div className="field__head">
          <small>插件配置里常写成 JDBC 形式</small>
          <button className="link" type="button" onClick={() => void copy('JDBC 地址', service.jdbc ?? '')}>
            复制 JDBC
          </button>
        </div>
      )}
      {service.jdbc && <code className="asset__conn">{service.jdbc}</code>}
      <small>
        {service.user
          ? `用户名 ${service.user}，密码 ${service.password}`
          : '这个引擎没有账号密码，只监听本机，别的机器连不上。'}
      </small>
    </div>
  )
}

/** Creating a database. Every field has a default, so the fast path is picking
 *  an engine and pressing the button. */
function CreateForm({
  databases,
  installs,
  engines,
  onDone,
}: {
  databases: DatabaseController
  installs: DatabaseInstall[]
  engines: DatabaseEngine[]
  onDone: () => void
}) {
  const [installId, setInstallId] = useState(installs[0]?.id ?? '')
  const [name, setName] = useState('')
  const [database, setDatabase] = useState('minecraft')
  const [user, setUser] = useState('hypercraft')
  const [password, setPassword] = useState('')
  const [remote, setRemote] = useState(false)
  const [autoStart, setAutoStart] = useState(true)

  const install = installs.find((entry) => entry.id === installId)
  const engine = engines.find((entry) => entry.id === install?.engine)
  const needsAccount = engine?.password ?? true

  const submit = async () => {
    const created = await databases.create({
      installId,
      name: name.trim() || undefined,
      database: database.trim(),
      user: needsAccount ? user.trim() : undefined,
      password: needsAccount && password.trim() !== '' ? password.trim() : undefined,
      bind: remote ? '0.0.0.0' : '127.0.0.1',
      autoStart,
    })
    if (created) {
      toast(`数据库「${created.name}」已建好`)
      onDone()
    }
  }

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">新建数据库</h2>
        <p className="chart-head__meta">留空的都会用默认值</p>
      </div>

      <div className="field">
        <span>用哪个引擎</span>
        <Select
          value={installId}
          onChange={setInstallId}
          ariaLabel="用哪个引擎"
          className="select--block"
          options={installs.map((entry) => ({
            value: entry.id,
            label: `${engines.find((candidate) => candidate.id === entry.engine)?.name ?? entry.engine} ${entry.version}`,
          }))}
        />
        {engine && <small>{engine.note}</small>}
      </div>

      <div className="field">
        <span>库名</span>
        <input
          value={database}
          onChange={(event) => setDatabase(event.target.value)}
          placeholder="minecraft"
          spellCheck={false}
        />
        <small>
          字母开头，只能用字母、数字和下划线。给每个服务器一个库比共用一个更好排查问题。
        </small>
      </div>

      <div className="field">
        <span>显示名（可选）</span>
        <input
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder={install ? `${engine?.name ?? ''} ${install.version}` : ''}
        />
      </div>

      {needsAccount && (
        <>
          <div className="field">
            <span>用户名</span>
            <input
              value={user}
              onChange={(event) => setUser(event.target.value)}
              spellCheck={false}
            />
          </div>
          <div className="field">
            <span>密码（可选）</span>
            <input
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              placeholder="留空则自动生成一个"
              spellCheck={false}
            />
            <small>
              至少 8 位，不能有引号、反斜杠和空格 —— 这些字符会破坏插件配置文件和面板生成的初始化语句。
            </small>
          </div>
        </>
      )}

      <div className="field">
        <label className="check">
          <input
            type="checkbox"
            checked={autoStart}
            onChange={(event) => setAutoStart(event.target.checked)}
          />
          <span>面板启动时自动开</span>
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={remote}
            disabled={!needsAccount}
            onChange={(event) => setRemote(event.target.checked)}
          />
          <span>允许别的机器连接</span>
        </label>
        <small>
          {needsAccount
            ? '不勾选就只监听本机，同一台机器上的服务端照样能连 —— 绝大多数情况这样就够，也最安全。勾选之后请自行确认防火墙规则。'
            : '这个引擎没法由面板设置账号密码，只能监听本机。'}
        </small>
      </div>

      <div className="actions">
        <button
          className="btn btn--primary"
          type="button"
          disabled={databases.busy || installId === '' || database.trim() === ''}
          onClick={() => void submit()}
        >
          {databases.busy ? '正在初始化…' : '创建'}
        </button>
        <button className="btn" type="button" disabled={databases.busy} onClick={onDone}>
          取消
        </button>
        <span className="muted">初始化要几秒到几十秒，建好后不会自动启动。</span>
      </div>
    </section>
  )
}

// ------------------------------------------------------------------- engines

function EngineList({
  databases,
  installs,
  engines,
  onOpenView,
}: {
  databases: DatabaseController
  installs: DatabaseInstall[]
  engines: DatabaseEngine[]
  onOpenView: (view: LibraryView) => void
}) {
  const remove = async (install: DatabaseInstall) => {
    const ok = await ask({
      title: `删除 ${install.engine} ${install.version}？`,
      lead: `会从面板的引擎目录里删掉它，释放 ${formatBytes(install.size)}。`,
      detail:
        install.usedBy.length > 0
          ? `数据库「${install.usedBy.join('、')}」跑在它上面，得先删掉那些数据库。`
          : '没有数据库在用它，已有的数据目录不受影响。',
      confirmLabel: '删除',
      danger: true,
    })
    if (!ok) return
    await databases.removeEngine(install.id)
  }

  if (installs.length === 0) {
    return (
      <section className="panel">
        <div className="welcome__empty">
          <p>还没有装过数据库引擎。</p>
          <p className="muted">
            <button className="link" type="button" onClick={() => onOpenView('install')}>
              挑一个装上
            </button>
            ，全程不动系统里的服务，也不需要 root 之外的额外配置。
          </p>
        </div>
      </section>
    )
  }

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">已安装</h2>
        <p className="chart-head__meta">
          共 {formatBytes(installs.reduce((sum, entry) => sum + entry.size, 0))}
        </p>
      </div>

      <div className="asset-list">
        {installs.map((install) => (
          <article className="asset" key={install.id}>
            <div className="asset__head">
              <span className="asset__tile asset__tile--accent">
                {install.engine.slice(0, 2).toUpperCase()}
              </span>
              <div className="asset__title">
                <span className="asset__label">
                  <strong>
                    {engines.find((entry) => entry.id === install.engine)?.name ?? install.engine}{' '}
                    {install.version}
                  </strong>
                  {install.live && <span className="badge badge--live">运行中</span>}
                  {install.problem && <span className="badge badge--update">跑不起来</span>}
                </span>
                <span className="asset__sub">
                  <span>
                    {engines.find((entry) => entry.id === install.engine)?.vendor ?? '未知来源'}
                  </span>
                  <code title={install.serverPath}>{install.serverPath}</code>
                </span>
              </div>
            </div>

            {/* The one failure an operator can act on, and the reason this
                check exists: these tarballs link against system libraries they
                do not ship, and a missing one only shows up at exec time. */}
            {install.problem && (
              <div className="alert alert--error">
                {install.problem}
                {install.hint && (
                  <>
                    <br />
                    {install.hint}
                  </>
                )}
              </div>
            )}

            <dl className="asset__facts asset__facts--split">
              <div>
                <dt>体积</dt>
                <dd>{formatBytes(install.size)}</dd>
              </div>
              <div>
                <dt>安装于</dt>
                <dd>{formatDate(install.installedAt)}</dd>
              </div>
              <div className="asset__hole" aria-hidden="true" />
            </dl>

            <footer className="asset__actions asset__actions--split">
              {install.usedBy.length > 0 ? (
                <span className="asset__users">
                  使用中：
                  {install.usedBy.map((name) => (
                    <span className="badge" key={name}>
                      {name}
                    </span>
                  ))}
                </span>
              ) : (
                <span className="muted">暂时没有数据库用它</span>
              )}
              <button
                className="link link--danger"
                disabled={databases.busy || install.usedBy.length > 0}
                title={install.usedBy.length > 0 ? '还有数据库跑在上面，先删掉它们' : undefined}
                onClick={() => void remove(install)}
              >
                删除
              </button>
            </footer>
          </article>
        ))}
      </div>
    </section>
  )
}

// ------------------------------------------------------------------- install

function InstallEngine({
  databases,
  engines,
  busy,
}: {
  databases: DatabaseController
  engines: DatabaseEngine[]
  busy: boolean
}) {
  const [engine, setEngine] = useState(engines[0]?.id ?? 'mysql')
  const [version, setVersion] = useState<string | null>(null)
  const [custom, setCustom] = useState('')
  const { job, installing } = databases

  // Versions come from three different upstreams, so they are fetched for the
  // engine being looked at rather than all at once.
  const { loadVersions } = databases
  useEffect(() => {
    if (engine) void loadVersions(engine)
  }, [engine, loadVersions])

  const list = databases.versions[engine]
  const selected = engine
  // Default to the newest long-term line, which is the answer for anyone who
  // does not have an opinion. Only until the operator picks something.
  useEffect(() => {
    if (!list || list.length === 0) return
    setVersion((current) => {
      if (current && list.some((entry) => entry.version === current)) return current
      return (list.find((entry) => entry.lts) ?? list[0]).version
    })
  }, [list])

  const chosen = custom.trim() !== '' ? custom.trim() : version
  const engineInfo = engines.find((entry) => entry.id === selected)

  return (
    <section className="panel">
      {job && <InstallStatus job={job} engines={engines} />}

      <div className="field">
        <span>选择数据库</span>
        <div className="choice-grid choice-grid--wide">
          {engines.map((entry) => (
            <button
              key={entry.id}
              type="button"
              className={`choice${entry.id === engine ? ' choice--active' : ''}`}
              aria-pressed={entry.id === engine}
              disabled={installing}
              onClick={() => {
                setEngine(entry.id)
                setVersion(null)
                setCustom('')
              }}
            >
              <span className="choice__label">{entry.name}</span>
              <span className="choice__note">{entry.note}</span>
            </button>
          ))}
        </div>
        {engineInfo && <small>二进制来自：{engineInfo.vendor}</small>}
      </div>

      {list === undefined ? (
        <p className="muted">正在读取可安装的版本…</p>
      ) : list.length === 0 ? (
        <p className="muted">
          没能取到可安装的版本列表 —— 通常是这台机器连不上外网。已装的引擎不受影响，
          已有的数据库照常启动。也可以在下面直接填版本号试试。
        </p>
      ) : (
        <div className="field">
          <span>选择版本</span>
          <div className="choice-grid">
            {list.map((entry) => (
              <button
                key={entry.version}
                type="button"
                className={`choice${entry.version === version && custom === '' ? ' choice--active' : ''}`}
                aria-pressed={entry.version === version && custom === ''}
                disabled={installing}
                onClick={() => {
                  setVersion(entry.version)
                  setCustom('')
                }}
              >
                <span className="choice__value">{entry.series}</span>
                <span className="choice__label">
                  {entry.version}
                  {entry.lts && <span className="badge">长期支持</span>}
                  {entry.installed && <span className="badge badge--ok">已安装</span>}
                </span>
                <span className="choice__note">{entry.note}</span>
              </button>
            ))}
          </div>
          <small>拿不准就选标了「长期支持」的那个。</small>
        </div>
      )}

      <div className="field">
        <span>或者直接填版本号（可选）</span>
        <input
          value={custom}
          onChange={(event) => setCustom(event.target.value)}
          placeholder="例如 8.0.45"
          spellCheck={false}
          disabled={installing}
        />
        <small>
          上面的列表可能比上游慢一两个版本。填了就按填的装，装不到会直接报错，不会装错东西。
        </small>
      </div>

      <DownloadNote engine={engine} />

      <div className="actions">
        {installing ? (
          <button className="btn btn--danger" onClick={() => void databases.cancelInstall()}>
            取消安装
          </button>
        ) : (
          <button
            className="btn btn--primary"
            disabled={busy || !chosen}
            onClick={() => chosen && void databases.install(engine, chosen)}
          >
            安装 {engineInfo?.name ?? ''} {chosen ?? ''}
          </button>
        )}
        <span className="muted">装完还要建一个数据库才能用。</span>
      </div>
    </section>
  )
}

/** What the operator should know before starting a download this large. */
function DownloadNote({ engine }: { engine: string }) {
  if (engine === 'mysql') {
    return (
      <p className="chart-note">
        Linux 上装的是官方精简包（60 MB 左右），ARM 机器没有精简包，要下完整包（近 1 GB）。
        另外 MySQL 的官方二进制依赖系统的 libaio，装完如果引擎那一页报「跑不起来」，按提示装一下就行。
      </p>
    )
  }
  if (engine === 'postgresql') {
    return (
      <p className="chart-note">
        PostgreSQL 官方不提供 Linux 的免安装包，这里用的是 zonky.io 的便携构建（Maven
        中央仓库，带校验和，embedded-postgres 用的就是它）。包很小，十几兆。
      </p>
    )
  }
  return (
    <p className="chart-note">
      MongoDB 会按这台机器的发行版挑对应的官方社区版构建，校验和来自官方发布清单。
      面板没法给它建账号密码（官方包里不带 mongosh），所以只能监听本机。
    </p>
  )
}

function InstallStatus({ job, engines }: { job: DatabaseInstallJob; engines: DatabaseEngine[] }) {
  const name = engines.find((entry) => entry.id === job.engine)?.name ?? job.engine

  if (job.state === 'downloading') {
    const fraction = job.total > 0 ? job.downloaded / job.total : 0
    return (
      <div className="download-status">
        <div className="progress">
          <div className="progress__bar" style={{ width: `${Math.round(fraction * 100)}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${Math.round(fraction * 100)}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : formatBytes(job.downloaded)}
          </span>
        </div>
        <p className="chart-note">
          正在下载 {name} {job.version}
        </p>
      </div>
    )
  }

  if (job.state === 'extracting') {
    return (
      <div className="alert alert--ok">
        正在解压 {name} {job.version}…（大的包解压比下载还慢，别关面板）
      </div>
    )
  }

  if (job.state === 'done') {
    return (
      <div className="alert alert--ok">
        {name} {job.version} 已安装，去「我的数据库」建一个库就能用了。
      </div>
    )
  }

  if (job.state === 'cancelled') {
    return <div className="alert alert--ok">已取消安装 {name}，没有留下任何文件。</div>
  }

  return <div className="alert alert--error">安装失败：{job.error ?? '未知错误'}</div>
}
