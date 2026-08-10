import { useEffect, useRef, useState } from 'react'

import { api } from '../api'
import { formatDate } from '../format'
import type {
  InstanceStatus,
  LibraryPlugin,
  PluginArtifact,
  PluginCompat,
  PluginInstallTargets,
} from '../types'
import { artifactKey, isLive, pluginArtifacts } from '../types'
import { Modal } from './Modal'
import { CompatBadge } from './PluginCompat'
import { loaderLabel } from './PluginBrowse'
import { Select } from './Select'

/** How a version's supported loaders read on one line: "Paper / Velocity". */
export function loaderNote(loaders?: string[]): string | undefined {
  if (!loaders?.length) return undefined
  return loaders.map(loaderLabel).join(' / ')
}

/**
 * Handing a library plugin to one or more servers.
 *
 * The only place a jar moves from the shared library into a server directory,
 * reached from either end — the plugin list, where the operator is thinking
 * about one plugin across the fleet, and a server's own page, where they are
 * thinking about one server. Both arrive here because the decision is the same
 * one and getting it wrong costs the same thing.
 *
 * A version has to be picked explicitly. "Whichever is newest" is exactly the
 * ambiguity a pinned plugin version exists to remove, and the newest is only
 * preselected because it is right nine times out of ten — not because the
 * choice is a formality.
 *
 * And "newest" is not even one jar. A plugin that supports several server
 * platforms ships one build per platform under the same release number —
 * LuckPerms publishes bukkit, velocity, fabric, forge and more — so the
 * library holds v5.5.71-bukkit *and* v5.5.71-velocity, and picking between
 * them by eye means reading a suffix and knowing what it implies. The panel
 * already recorded what each jar declares at download time; this dialog is
 * where that finally gets used. Every version says which loaders it is for,
 * and every server says whether the chosen jar fits it.
 *
 * Incompatible servers are marked, not blocked. The metadata comes from the
 * registry and the registry is sometimes wrong or silent, so the panel says
 * what it knows and loudly — and then lets the operator, who can read their
 * own server's logs, decide.
 */
export function PluginInstallDialog({
  item,
  instances,
  /** Preselected server, when opened from one. */
  preselect,
  onCancel,
  onInstalled,
}: {
  item: LibraryPlugin
  instances: InstanceStatus[]
  preselect?: string
  onCancel: () => void
  onInstalled: (summary: string) => void
}) {
  const [tag, setTag] = useState(item.versions[0]?.tag ?? '')
  const [chosen, setChosen] = useState<string[]>(preselect ? [preselect] : [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [matrix, setMatrix] = useState<PluginInstallTargets | null>(null)

  // Every held version against every server, in one request. Best effort: a
  // failure here costs the badges, not the dialog — installing without a
  // verdict is exactly what this screen did before there were any.
  useEffect(() => {
    let live = true
    api
      .pluginInstallTargets(item.id)
      .then((next) => live && setMatrix(next))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [item.id])

  const version = item.versions.find((entry) => entry.tag === tag)
  const targets = instances.filter((instance) => chosen.includes(instance.id))
  const live = targets.filter((instance) => isLive(instance.state))

  const verdictFor = (versionTag: string, instanceId: string) =>
    matrix?.verdicts?.[versionTag]?.[instanceId] ?? null

  // Which jar of the chosen release each server gets.
  //
  // A release is not a file. LuckPerms publishes a bukkit build and a velocity
  // build under one version number, and "install 5.5.71 on these three
  // servers" means the paper jar on two of them and the velocity jar on the
  // proxy. The operator picked a version, which is the decision they can
  // actually make; which file that is, per server, is arithmetic the panel has
  // the metadata for — so it does it rather than asking.
  const jars = version ? pluginArtifacts(version) : []
  const jarFor = (instanceId: string): PluginArtifact | null => {
    if (jars.length === 0) return null
    let best = jars[0]
    let rank = compatRank(jarVerdict(matrix, best, instanceId))
    for (const jar of jars.slice(1)) {
      const next = compatRank(jarVerdict(matrix, jar, instanceId))
      if (next < rank) {
        best = jar
        rank = next
      }
    }
    return best
  }
  const verdictOn = (instanceId: string): PluginCompat | null => {
    const jar = jarFor(instanceId)
    return jar ? jarVerdict(matrix, jar, instanceId) : verdictFor(tag, instanceId)
  }
  const clashes = targets.filter((instance) => verdictOn(instance.id)?.state === 'bad')

  // Which jar to open on.
  //
  // "The newest" is not an answer when a release ships one build per platform:
  // the library holds v5.5.71-bukkit and v5.5.71-velocity, they were published
  // in the same minute, and whichever sorted first is what the dialog used to
  // land on — with the only sign being a suffix. So: the newest that fits the
  // server this was opened from, or failing that the newest that fits any
  // server at all. Runs once, because after that the choice is the operator's.
  const seeded = useRef(false)
  useEffect(() => {
    if (seeded.current || !matrix) return
    seeded.current = true
    const fits = (entry: LibraryPlugin['versions'][number], instanceId: string) =>
      matrix.verdicts?.[entry.tag]?.[instanceId]?.state === 'ok'
    const fit =
      (preselect && item.versions.find((entry) => fits(entry, preselect))) ||
      item.versions.find((entry) => instances.some((inst) => fits(entry, inst.id)))
    if (fit) setTag(fit.tag)
  }, [matrix, preselect, item.versions, instances])

  const install = async () => {
    setBusy(true)
    setError(null)

    // Carries on past a failure rather than stopping at the first: the servers
    // it already reached have the new jar either way, and "one of these
    // failed" without saying which means checking all of them by hand.
    const failures: string[] = []
    let applied = 0
    for (const instance of targets) {
      try {
        await api.installInstancePlugin(instance.id, item.id, tag, jarFor(instance.id)?.sha256)
        applied++
      } catch (err) {
        failures.push(`${instance.name}：${err instanceof Error ? err.message : '安装失败'}`)
      }
    }
    setBusy(false)

    if (failures.length > 0) {
      setError(failures.join('；'))
      return
    }
    onInstalled(
      `已把 ${item.name} ${version?.version ?? ''} 装到 ${targets.map((t) => t.name).join('、')}` +
        (live.length > 0 ? '，重启后生效' : ''),
    )
  }

  return (
    <Modal onClose={onCancel} busy={busy} label={`安装 ${item.name}`}>
      <div className="modal__card">
        <h2 className="modal__title">把 {item.name} 装到实例</h2>

        {error && <div className="alert alert--error">{error}</div>}

        {item.versions.length === 0 ? (
          <div className="alert alert--warn">
            插件库里还没有这个插件的 jar —— 先去「插件市场」下载一个版本。
          </div>
        ) : (
          <label className="field">
            <span>版本</span>
            <Select
              ariaLabel="版本"
              value={tag}
              className="select--block"
              options={item.versions.map((entry) => ({
                value: entry.tag,
                label: `${entry.version}${entry.prerelease ? '（预发布）' : ''}`,
                // Which server software this particular jar is built for —
                // the whole reason there are several of them under one
                // release number.
                note: loaderNote(entry.loaders) ?? formatDate(entry.publishedAt),
              }))}
              onChange={setTag}
            />
            {version && (
              <small>
                {jars.length > 1
                  ? `这次发布有 ${jars.length} 个 jar（${jarNote(jars)}）—— 每台服各取合适的那个`
                  : loaderNote(version.loaders)
                    ? `这个 jar 是给 ${loaderNote(version.loaders)} 用的`
                    : '来源没说明这个 jar 是给哪种服务端核心用的，装之前请自行确认'}
                {' · '}
                {formatDate(version.publishedAt)}
              </small>
            )}
          </label>
        )}

        <div className="field">
          <span>装到哪几台</span>
          {instances.length === 0 ? (
            <p className="muted">面板里还没有实例。</p>
          ) : (
            <div className="pick-list">
              {instances.map((instance) => (
                <label className="pick-list__row" key={instance.id}>
                  <input
                    type="checkbox"
                    checked={chosen.includes(instance.id)}
                    onChange={() =>
                      setChosen((current) =>
                        current.includes(instance.id)
                          ? current.filter((id) => id !== instance.id)
                          : [...current, instance.id],
                      )
                    }
                  />
                  <span className={`status__dot status__dot--${instance.state}`} />
                  <span className="pick-list__name">{instance.name}</span>
                  {item.usedBy.includes(instance.name) && (
                    <span className="badge">已装，会换成这个版本</span>
                  )}
                  {/* The verdict for the jar *this* server would get, which
                      on a multi-jar release is not the same jar its neighbour
                      gets. Absent when the source published nothing to judge
                      by, which is not a green light — see CompatBadge. */}
                  {jars.length > 1 && jarFor(instance.id)?.platform && (
                    <span className="badge badge--muted">
                      {loaderLabel(jarFor(instance.id)!.platform!)} 构建
                    </span>
                  )}
                  <CompatBadge compat={verdictOn(instance.id) ?? undefined} />
                </label>
              ))}
            </div>
          )}
        </div>

        {clashes.length > 0 && (
          <div className="alert alert--warn">
            {clashes.map((instance) => instance.name).join('、')} 跟这个版本里的 jar 都对不上（
            {/* Deduped: five servers failing for the same reason should say
                the reason once. */}
            {Array.from(
              new Set(
                clashes
                  .map((instance) => verdictOn(instance.id)?.label)
                  .filter((label): label is string => Boolean(label)),
              ),
            ).join('、')}
            ），<strong>装上去多半不会被加载</strong>。
            {jars.length > 1
              ? '这次发布库里有几个平台的构建，但没有这几台服要的那个 —— 去插件详情的「版本」里把它下载到库。'
              : '这个插件如果分平台发版，去插件详情的「版本」里看看有没有对应平台的 jar 可以下载。'}
          </div>
        )}

        {live.length > 0 && (
          <div className="alert alert--warn">
            {live.map((instance) => instance.name).join('、')} 正在运行 ——
            jar 会立刻写进去，但要重启才会加载。面板不会自动重启。
          </div>
        )}

        <div className="modal__actions">
          <button className="btn" disabled={busy} onClick={onCancel}>
            取消
          </button>
          <button
            className="btn btn--primary"
            disabled={busy || !tag || targets.length === 0}
            onClick={() => void install()}
          >
            {busy ? '安装中…' : targets.length > 1 ? `装到 ${targets.length} 台` : '安装'}
          </button>
        </div>
      </div>
    </Modal>
  )
}

/** The verdict on one jar for one server, out of the matrix. */
function jarVerdict(
  matrix: PluginInstallTargets | null,
  artifact: PluginArtifact,
  instanceId: string,
): PluginCompat | null {
  return matrix?.jars?.[artifactKey(artifact)]?.[instanceId] ?? null
}

/** Fits, might fit, does not fit. Mirrors the ranking the server uses to fold
 *  a release's jars into one verdict for the release. */
function compatRank(verdict: PluginCompat | null): number {
  switch (verdict?.state) {
    case 'ok':
      return 0
    case 'bad':
      return 2
    default:
      return 1
  }
}

/** The platforms a release's jars are for, as one line. */
function jarNote(jars: PluginArtifact[]): string {
  const seen = new Set<string>()
  for (const jar of jars) {
    const platform = jar.platform ?? jar.loaders?.[0]
    if (platform) seen.add(loaderLabel(platform))
  }
  return seen.size > 0 ? Array.from(seen).join(' / ') : `${jars.length} 个构建`
}
