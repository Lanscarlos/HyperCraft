import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { JavaInstallJob, JavaMajor, JavaOverview, JavaRuntime } from '../types'

/** Which Java a Minecraft version needs, for the hint under the picker. */
const VERSION_HINTS = [
  { major: 8, servers: '1.8 – 1.16.5' },
  { major: 17, servers: '1.17 – 1.20.4' },
  { major: 21, servers: '1.20.5 及以上' },
  { major: 25, servers: 'Paper 26 及以上' },
]

/**
 * Panel-wide Java management: what is installed, what the system has, and a
 * one-click install of a Temurin build.
 *
 * It lives on the overview page rather than inside an instance because a
 * runtime is shared — one download serves every server that needs that
 * version, and deleting one is a decision about all of them.
 */
export function JavaRuntimes() {
  const [overview, setOverview] = useState<JavaOverview | null>(null)
  const [majors, setMajors] = useState<JavaMajor[]>([])
  const [major, setMajor] = useState<number | null>(null)
  const [imageType, setImageType] = useState<'jre' | 'jdk'>('jre')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const job = overview?.job ?? null
  const installing = job?.state === 'downloading' || job?.state === 'extracting'

  const load = useCallback(async () => {
    try {
      setOverview(await api.javaOverview())
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取 Java 列表失败')
    }
  }, [])

  useEffect(() => {
    void load()
    api
      .javaMajors()
      .then((list) => {
        setMajors(list)
        // Default to the newest LTS: it is what current Minecraft wants and
        // what upstream supports longest.
        setMajor((current) => current ?? (list.find((m) => m.lts) ?? list[0])?.major ?? null)
      })
      .catch(() => setMajors([]))
  }, [load])

  useEffect(() => {
    if (!installing) return
    const timer = window.setInterval(() => void load(), 800)
    return () => window.clearInterval(timer)
  }, [installing, load])

  // A finished install changes the list and the "已安装" flags.
  useEffect(() => {
    if (job?.state !== 'done') return
    api.javaMajors().then(setMajors).catch(() => undefined)
  }, [job?.state, job?.runtimeId])

  const install = async () => {
    if (major == null) return
    setBusy(true)
    setError(null)
    try {
      const started = await api.installJava(major, imageType)
      setOverview((prev) => (prev ? { ...prev, job: started } : prev))
    } catch (err) {
      setError(err instanceof Error ? err.message : '安装失败')
    } finally {
      setBusy(false)
    }
  }

  const cancel = async () => {
    setBusy(true)
    try {
      await api.cancelJavaInstall()
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '取消失败')
    } finally {
      setBusy(false)
    }
  }

  const remove = async (runtime: JavaRuntime) => {
    const warning =
      runtime.usedBy.length > 0
        ? `实例「${runtime.usedBy.join('、')}」还在用 ${runtime.version}，删掉后它们下次启动会失败。确定删除吗？`
        : `确定要删除 Java ${runtime.version}（${formatBytes(runtime.size)}）吗？`
    if (!window.confirm(warning)) return

    setBusy(true)
    setError(null)
    try {
      await api.deleteJavaRuntime(runtime.id)
      await load()
      setMajors(await api.javaMajors())
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    } finally {
      setBusy(false)
    }
  }

  if (!overview) {
    return null
  }

  const selected = majors.find((entry) => entry.major === major)
  const hint = VERSION_HINTS.find((entry) => entry.major === major)

  return (
    <section className="panel">
      <div className="chart-head">
        <h3 className="panel__title">Java 运行时</h3>
        <p className="chart-head__meta">
          {overview.platform.os && `${overview.platform.os}/${overview.platform.arch} · `}
          由 Eclipse Temurin 提供
        </p>
      </div>

      <p className="chart-note">
        不同版本的服务端要不同的 Java：1.16 要 8，1.17 要 17，1.20.5 起要 21，Paper 26 要 25。
        装好之后在实例的「启动设置」里选一个即可，不用动系统环境。
      </p>

      {overview.platform.warning && (
        <div className="alert alert--error">{overview.platform.warning}</div>
      )}

      <div className="java-list">
        {overview.system && (
          <div className="java-row">
            <div className="java-row__main">
              <strong>系统 Java {overview.system.major || '?'}</strong>
              <span className="badge">来自 {overview.system.source}</span>
            </div>
            <div className="java-row__meta">
              {overview.system.version} · <code>{overview.system.path}</code>
            </div>
          </div>
        )}

        {overview.runtimes.map((runtime) => (
          <div className="java-row" key={runtime.id}>
            <div className="java-row__main">
              <strong>Java {runtime.major}</strong>
              <span className="badge">{runtime.imageType.toUpperCase()}</span>
              {runtime.live && <span className="badge">运行中</span>}
              <span className="java-row__spacer" />
              <button
                className="link link--danger"
                disabled={busy || runtime.live}
                title={runtime.live ? '有实例正在用它运行，先停服' : undefined}
                onClick={() => void remove(runtime)}
              >
                删除
              </button>
            </div>
            <div className="java-row__meta">
              {runtime.version} · {formatBytes(runtime.size)} ·{' '}
              {runtime.vendor || '未知发行方'}
              {runtime.usedBy.length > 0 && <> · 使用中：{runtime.usedBy.join('、')}</>}
            </div>
            <div className="java-row__meta">
              <code>{runtime.javaPath}</code>
            </div>
          </div>
        ))}

        {overview.runtimes.length === 0 && !overview.system && (
          <p className="muted">这台机器上还没有 Java，下面装一个。</p>
        )}
      </div>

      {job && <InstallStatus job={job} />}
      {error && <div className="alert alert--error">{error}</div>}

      <div className="field-row">
        <label className="field">
          <span>安装版本</span>
          <select
            value={major ?? ''}
            onChange={(e) => setMajor(Number(e.target.value))}
            disabled={installing || majors.length === 0}
          >
            {majors.map((entry) => (
              <option key={entry.major} value={entry.major}>
                Java {entry.major}
                {entry.lts ? ' · LTS' : ''}
                {entry.installed ? ' · 已安装' : ''}
              </option>
            ))}
          </select>
          <small>{hint ? `对应服务端版本：${hint.servers}` : ' '}</small>
        </label>

        <label className="field">
          <span>类型</span>
          <select
            value={imageType}
            onChange={(e) => setImageType(e.target.value as 'jre' | 'jdk')}
            disabled={installing}
          >
            <option value="jre">JRE（跑服够用，体积更小）</option>
            <option value="jdk">JDK（带编译器和调试工具）</option>
          </select>
          <small>拿不准就选 JRE。</small>
        </label>
      </div>

      <div className="settings__actions">
        {installing ? (
          <button className="btn btn--danger" onClick={() => void cancel()} disabled={busy}>
            取消安装
          </button>
        ) : (
          <button
            className="btn btn--primary"
            onClick={() => void install()}
            disabled={busy || major == null}
          >
            {selected?.installed ? '重新安装' : '安装'}
          </button>
        )}
        <span className="file-toolbar__hint">
          装到 <code>{overview.root}</code>，不会碰系统里的 Java。
        </span>
      </div>
    </section>
  )
}

function InstallStatus({ job }: { job: JavaInstallJob }) {
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
          正在下载 Java {job.major} {job.imageType.toUpperCase()}（{job.version}）
        </p>
      </div>
    )
  }

  if (job.state === 'extracting') {
    return <div className="alert alert--ok">正在解压 Java {job.major}（{job.version}）…</div>
  }

  if (job.state === 'done') {
    return (
      <div className="alert alert--ok">
        Java {job.major}（{job.version}）已安装，去实例的「启动设置」里选它。
      </div>
    )
  }

  if (job.state === 'cancelled') {
    return <div className="alert alert--ok">已取消安装 Java {job.major}，没有留下任何文件。</div>
  }

  return <div className="alert alert--error">安装失败：{job.error ?? '未知错误'}</div>
}
