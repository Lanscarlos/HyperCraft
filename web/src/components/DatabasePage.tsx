import { useEffect, useState } from 'react'

import { ask, askWithToggle } from '../confirm'
import { formatBytes, formatDate } from '../format'
import { toast } from '../toast'
import type {
  DatabaseEngine,
  DatabaseInstall,
  DatabaseInstallJob,
  DatabaseService,
} from '../types'
import type { DatabaseController } from '../useDatabases'
import { Badge } from './Badge'
import { Button } from './Button'
import { Page } from './Page'
import { Select } from './Select'
import { Shelf } from './Shelf'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'

/** Named because the page renders it before its data arrives as well as after,
 *  and the two have to be the same string or the page moves when it loads. */
const DB_LEAD =
  '不少插件要数据库：LuckPerms 存权限、CoreProtect 存日志、Plan 存统计。这里装的数据库归面板所有，不动系统里的服务，建好之后把连接串复制到插件配置里就行。'

const STATE_LABELS: Record<DatabaseService['state'], string> = {
  stopped: '已停止',
  starting: '启动中',
  running: '运行中',
  stopping: '停止中',
  failed: '启动失败',
}

/** The dot an instance's status uses, borrowed so a database that will not
 *  start looks like a server that will not start. Its vocabulary calls that
 *  state 崩溃 rather than 失败, which is the only word that differs. */
const STATE_DOTS: Record<DatabaseService['state'], string> = {
  stopped: 'stopped',
  starting: 'starting',
  running: 'running',
  stopping: 'stopping',
  failed: 'crashed',
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
export function DatabasePage({ databases }: { databases: DatabaseController }) {
  const { overview, job, installing, busy } = databases

  if (!overview) {
    // The heading and the lead are constants, not data — showing them straight
    // away means the page opens with its own name on it, and only what was
    // actually being fetched arrives later.
    return (
      <Page wide title="数据库环境" lead={DB_LEAD}>
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
      title="数据库环境"
      lead={DB_LEAD}
      aside={
        <p className="meta-chips">
          {/* The count and how many of them are up, in one chip. The other
              three — os/arch, engine total — are facts about the machine and
              the disk, not about the thing you came to look at. */}
          <span>
            {services.length > 0
              ? `${services.length} 个数据库 · ${live} 个运行中`
              : '还没建数据库'}
          </span>
          {installs.length > 0 && <span>引擎共 {formatBytes(totalSize)}</span>}
          {platform.os && (
            <span>
              {platform.os}/{platform.arch}
            </span>
          )}
        </p>
      }
    >
      {platform.warning && <div className="alert alert--error">{platform.warning}</div>}
      {databases.error && <div className="alert alert--error">{databases.error}</div>}

      {/* An install keeps running after you navigate away, so it is reported at
          the top of the page rather than inside the card that started it. */}
      {job && (job.state === 'downloading' || job.state === 'extracting') && (
        <InstallStatus job={job} engines={engines} />
      )}

      {/* Three cards, in the order of the three questions: what databases do I
          have, what engines are they built on, and how do I get another engine.
          They were three pages, and the engine list is why the split never paid
          — after the first install it is a card you glance at, not a page you
          navigate to. */}
      <ServiceList
        databases={databases}
        services={services}
        installs={installs}
        engines={engines}
      />

      <EngineList databases={databases} installs={installs} engines={engines} />

      <InstallEngine databases={databases} engines={engines} busy={busy || installing} />
    </Page>
  )
}

// ------------------------------------------------------------------ services

function ServiceList({
  databases,
  services,
  installs,
  engines,
}: {
  databases: DatabaseController
  services: DatabaseService[]
  installs: DatabaseInstall[]
  engines: DatabaseEngine[]
}) {
  const [creating, setCreating] = useState(false)
  // Which database the pane beside the table is describing. Null until
  // something is clicked, and then resolved against the live list rather than
  // stored as an object — a database that is deleted or renamed under this
  // must not leave a stale copy of itself on screen.
  const [picked, setPicked] = useState<string | null>(null)
  const usable = installs.filter((install) => !install.problem)
  const current = services.find((service) => service.id === picked) ?? services[0]

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
    /* The table says which databases exist; the pane beside it says everything
       about the one being looked at. They were one thing — 每行自带连接串、
       JDBC 和密码 — which made four databases four screens long and moved the
       one string somebody has to read character by character to a different
       place on the page each time.

       No split until there is something to put in the pane: a two-track grid
       with an empty second track is 356px of nothing beside the 还没有建过
       empty state. */
    <div className={current ? 'dbsplit' : undefined}>
      <div className="dbsplit__main">
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
                  {/* No link: 安装引擎 is the last card on this page now, so
                      pointing at it is pointing down. */}
                  <p className="muted">
                    先在下面装一个引擎，MySQL 的精简包只有 60 MB 左右，装完就能建库。
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
            <Shelf head={['数据库', '监听', '库名', '建于', '状态', '']}>
              {services.map((service) => (
                <ServiceRow
                  key={service.id}
                  service={service}
                  busy={databases.busy}
                  picked={service.id === current?.id}
                  onPick={() => setPicked(service.id)}
                  onStart={() => void databases.start(service.id)}
                  onStop={() => void databases.stop(service.id)}
                />
              ))}
            </Shelf>
          )}

          {usable.length > 0 && (
            <div className="actions">
              {/* Hands the emphasis over the moment the form opens: 创建 is
                  then the thing being asked for, and this one is a disabled
                  copy of a decision already made. */}
              <Button
                variant={creating ? 'default' : 'primary'}
                type="button"
                disabled={databases.busy || creating}
                onClick={() => setCreating(true)}
              >
                新建数据库
              </Button>
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
      </div>

      {current && (
        <ServiceDetail
          service={current}
          busy={databases.busy}
          onRemove={() => void remove(current)}
          onStart={() => void databases.start(current.id)}
          onStop={() => void databases.stop(current.id)}
          onToggleAutoStart={() =>
            void databases.update(current.id, { autoStart: !current.autoStart })
          }
        />
      )}
    </div>
  )
}

function ServiceRow({
  service,
  busy,
  picked,
  onPick,
  onStart,
  onStop,
}: {
  service: DatabaseService
  busy: boolean
  picked: boolean
  onPick: () => void
  onStart: () => void
  onStop: () => void
}) {
  const running = service.state === 'running'
  const moving = service.state === 'starting' || service.state === 'stopping'

  return (
    <article className={`asset${picked ? ' asset--picked' : ''}`}>
      {/* The name is the row's hit target rather than the whole row: two of
          the six cells are already buttons, and a click handler on the article
          would swallow the one that matters most — 停止. */}
      <button className="asset__pick" type="button" aria-pressed={picked} onClick={onPick}>
        <span className={`asset__tile${running ? ' asset__tile--accent' : ''}`}>
          {service.engine.slice(0, 2).toUpperCase()}
        </span>
        <span className="asset__title">
          <span className="asset__label">
            <strong>{service.name}</strong>
            <Badge>{service.version}</Badge>
            {service.missing && <Badge tone="update">引擎已删除</Badge>}
          </span>
          <span className="asset__sub">
            <span>{service.autoStart ? '跟随面板启动' : '手动启动'}</span>
            <code title={service.dir}>{service.dir}</code>
          </span>
        </span>
      </button>

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

      <span className="asset__users">
        <span
          className={`status__dot status__dot--${STATE_DOTS[service.state]}`}
          aria-hidden="true"
        />
        {STATE_LABELS[service.state]}
      </span>

      <span className="asset__actions asset__actions--split">
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
      </span>
    </article>
  )
}

/**
 * Everything about the one database that is selected: how to connect to it,
 * what to paste into a plugin, and the two decisions that are not reversible.
 *
 * It is a pane rather than an expanded row because the connection string is
 * the thing this page exists for — it should be in the same place on the
 * screen every time, not wherever the fourth row happens to be.
 */
function ServiceDetail({
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
    <aside className="dbdetail" aria-label={`${service.name} 的连接信息`}>
      <div className="dbdetail__head">
        <span className={`asset__tile${running ? ' asset__tile--accent' : ''}`}>
          {service.engine.slice(0, 2).toUpperCase()}
        </span>
        <strong>{service.name}</strong>
        <Badge>{STATE_LABELS[service.state]}</Badge>
      </div>

      {service.error && (
        <div className="dbdetail__sec">
          <div className="alert alert--error">{service.error}</div>
        </div>
      )}

      <div className="dbdetail__sec">
        <span className="dbdetail__label">连接信息</span>
        <div className="dbdetail__row">
          <span>主机</span>
          <code>
            {service.bind}:{service.port}
          </code>
        </div>
        <div className="dbdetail__row">
          <span>库名</span>
          <code>{service.database}</code>
        </div>
        {service.user && (
          <>
            <div className="dbdetail__row">
              <span>用户</span>
              <code>{service.user}</code>
            </div>
            {/* Shown rather than masked. It is not a secret the panel is
                keeping from the operator — every plugin that uses it stores
                the same string in its own config — and a field they have to
                reveal before every copy would be theatre with a cost. */}
            <div className="dbdetail__row">
              <span>密码</span>
              <code>{service.password}</code>
            </div>
          </>
        )}
        <div className="dbdetail__row">
          <span>目录</span>
          <code title={service.dir}>{service.dir}</code>
        </div>
      </div>

      <Connection service={service} />

      <div className="dbdetail__sec">
        <label className="check">
          <input
            type="checkbox"
            checked={service.autoStart}
            disabled={busy}
            onChange={onToggleAutoStart}
          />
          <span>面板启动时自动开</span>
        </label>
      </div>

      <div className="dbdetail__sec">
        <span className="dbdetail__label">操作</span>
        <div className="dbdetail__actions">
          {running || moving ? (
            <Button size="small" type="button" disabled={busy || moving} onClick={onStop}>
              停止
            </Button>
          ) : (
            <Button
              size="small"
              type="button"
              disabled={busy || service.missing}
              onClick={onStart}
            >
              启动
            </Button>
          )}
          <Button
            size="small"
            variant="danger"
            type="button"
            disabled={busy || running || moving}
            title={running || moving ? '先停下来再删' : undefined}
            onClick={onRemove}
          >
            删除
          </Button>
        </div>
      </div>
    </aside>
  )
}

/**
 * The two strings that get pasted into a plugin's config, which are the reason
 * this whole page exists.
 *
 * A section of the detail pane rather than part of every row: a URI has no
 * spaces to wrap at, so it needs a line of its own, and four of those lines
 * turned a list of four databases into four screens.
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
    <div className="dbdetail__sec">
      <span className="dbdetail__label">
        <span>连接串</span>
        <button className="link" type="button" onClick={() => void copy('连接串', service.uri)}>
          复制
        </button>
      </span>
      <code className="asset__conn">{service.uri}</code>

      {service.jdbc && (
        <>
          <span className="dbdetail__label">
            <span>JDBC —— 插件配置里常写成这个形式</span>
            <button
              className="link"
              type="button"
              onClick={() => void copy('JDBC 地址', service.jdbc ?? '')}
            >
              复制
            </button>
          </span>
          <code className="asset__conn">{service.jdbc}</code>
        </>
      )}

      {!service.user && (
        <small>这个引擎没有账号密码，只监听本机，别的机器连不上。</small>
      )}
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
    <section className="panel panel--form">
      <div className="panel__aside">
        <h3 className="panel__title">新建数据库</h3>
        <p className="panel__note">建一个库和它自己的账号，留空的都会用默认值。</p>
      </div>

      <div className="panel__body">

      <div className="field field--md">
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

      <div className="field field--md">
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

      <div className="field field--md">
        <span>显示名（可选）</span>
        <input
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder={install ? `${engine?.name ?? ''} ${install.version}` : ''}
        />
      </div>

      {/* One account, so one line: a username without its password is half a
          credential, and reading them down a column puts the pair on two
          separate rows of a form that is mostly optional fields. */}
      {needsAccount && (
        <div className="field-row">
          <div className="field field--md">
            <span>用户名</span>
            <input
              value={user}
              onChange={(event) => setUser(event.target.value)}
              spellCheck={false}
            />
          </div>
          <div className="field field--md">
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
        </div>
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
        <Button
          variant="primary"
          type="button"
          disabled={databases.busy || installId === '' || database.trim() === ''}
          onClick={() => void submit()}
        >
          {databases.busy ? '正在初始化…' : '创建'}
        </Button>
        <Button type="button" disabled={databases.busy} onClick={onDone}>
          取消
        </Button>
        <span className="muted">初始化要几秒到几十秒，建好后不会自动启动。</span>
      </div>
      </div>
    </section>
  )
}

// ------------------------------------------------------------------- engines

function EngineList({
  databases,
  installs,
  engines,
}: {
  databases: DatabaseController
  installs: DatabaseInstall[]
  engines: DatabaseEngine[]
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
        <div className="chart-head">
          <h2 className="panel__title">已装引擎</h2>
          <p className="chart-head__meta">引擎是数据库程序本身，一个可以给多个数据库共用</p>
        </div>
        <p className="muted">
          还没有装过。下面挑一个装上 —— 全程不动系统里的服务，也不需要 root 之外的额外配置。
        </p>
      </section>
    )
  }

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">已装引擎</h2>
        <p className="chart-head__meta">
          共 {formatBytes(installs.reduce((sum, entry) => sum + entry.size, 0))} · 删掉引擎不会动数据，
          但跑在上面的数据库会起不来
        </p>
      </div>

      <Shelf head={['引擎', '', '体积', '安装于', '使用中的数据库', '']}>
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
                  {install.live && <Badge tone="live">运行中</Badge>}
                  {install.problem && <Badge tone="update">跑不起来</Badge>}
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

            {/* The hole goes first, not last. An engine row has two facts for
                three tracks, and the third track is the wide one the shelves
                size for a date — leaving the gap at the end pushed 安装于 into
                a 96px track and clipped it. Skipping the narrow track instead
                lands the date where it fits and still lines the rows up with
                each other, which is all the alignment is for. */}
            <dl className="asset__facts asset__facts--split">
              <div className="asset__hole" aria-hidden="true" />
              <div>
                <dt>体积</dt>
                <dd>{formatBytes(install.size)}</dd>
              </div>
              <div>
                <dt>安装于</dt>
                <dd>{formatDate(install.installedAt)}</dd>
              </div>
            </dl>

            <footer className="asset__actions asset__actions--split">
              {install.usedBy.length > 0 ? (
                <span className="asset__users">
                  使用中：
                  {install.usedBy.map((name) => (
                    <Badge key={name}>
                      {name}
                    </Badge>
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
      </Shelf>
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

  const engineInfo = engines.find((entry) => entry.id === selected)

  return (
    <section className="panel">
      {/* Which of the three, in the card's head. It is one choice out of three
          and it scopes everything below it, which is the shape of a tab strip
          rather than of a grid of tiles the size of the versions underneath. */}
      <div className="chart-head">
        <h2 className="panel__title">安装引擎</h2>
        <p className="chart-head__meta">
          {engineInfo ? `二进制来自 ${engineInfo.vendor}` : '从官方渠道下载'} ·
          下载走服务器自己的网络，关掉网页也会继续
        </p>
        <div className="chart-head__tools">
          <div className="segmented segmented--inline" role="group" aria-label="选择数据库">
            {engines.map((entry) => (
              <button
                key={entry.id}
                type="button"
                className={`segmented__option${
                  entry.id === engine ? ' segmented__option--active' : ''
                }`}
                aria-pressed={entry.id === engine}
                title={entry.note}
                disabled={installing}
                onClick={() => {
                  setEngine(entry.id)
                  setVersion(null)
                  setCustom('')
                }}
              >
                <strong>{entry.name}</strong>
              </button>
            ))}
          </div>
        </div>
      </div>

      {list === undefined ? (
        <p className="muted">正在读取可安装的版本…</p>
      ) : list.length === 0 ? (
        <p className="muted">
          没能取到可安装的版本列表 —— 通常是这台机器连不上外网。已装的引擎不受影响，
          已有的数据库照常启动。也可以在下面直接填版本号试试。
        </p>
      ) : (
        <>
          {/* One line per build, with the button on the line. Picking a point
              release and then hunting for an 安装 button under the form was a
              second step for a decision the line had already made. */}
          <div className="pick-grid">
            {list.map((entry) => {
              const running = installing && job?.version === entry.version
              return (
                <div
                  key={entry.version}
                  className={`pick${entry.version === version ? ' pick--on' : ''}`}
                >
                  <span className="pick__tile">{entry.series}</span>
                  <div className="pick__body">
                    <span className="pick__name">
                      <strong>{entry.version}</strong>
                      {entry.lts && <Badge>长期支持</Badge>}
                      {entry.installed && <Badge tone="ok">已安装</Badge>}
                    </span>
                    <span className="pick__meta">{entry.note}</span>
                  </div>
                  {running ? (
                    <Button
                      size="small"
                      variant="danger"
                      type="button"
                      disabled={busy}
                      onClick={() => void databases.cancelInstall()}
                    >
                      取消
                    </Button>
                  ) : (
                    <Button
                      size="small"
                      type="button"
                      disabled={busy || installing}
                      onClick={() => {
                        setVersion(entry.version)
                        setCustom('')
                        void databases.install(engine, entry.version)
                      }}
                    >
                      {entry.installed ? '重装' : '安装'}
                    </Button>
                  )}
                </div>
              )
            })}
          </div>
          <small>拿不准就选标了「长期支持」的那个。</small>
        </>
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
          <Button variant="danger" onClick={() => void databases.cancelInstall()}>
            取消安装
          </Button>
        ) : (
          // The escape hatch, not the main path — that is the 安装 on each
          // row of the list above, and those are plain.
          <Button
            disabled={busy || custom.trim() === ''}
            onClick={() => void databases.install(engine, custom.trim())}
          >
            安装填写的版本
          </Button>
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
