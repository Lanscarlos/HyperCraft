// Mirrors the Go types in internal/instance and internal/api.

export type InstanceState =
  | 'stopped'
  | 'starting'
  | 'running'
  | 'stopping'
  | 'crashed'

export interface InstanceConfig {
  id: string
  name: string
  directory: string
  createdAt: string
  java: string
  jar: string
  minMemoryMB: number
  maxMemoryMB: number
  jvmArgs: string[]
  serverArgs: string[]
  command: string[] | null
  /** Console charset: 'auto', 'utf-8', 'gbk', … See ENCODING_OPTIONS. */
  encoding: string
  /** Make the server emit ANSI colour even though its stdout is a pipe. */
  forceColor: boolean
  autoStart: boolean
  autoRestart: boolean
  stopCommand: string
  stopTimeoutSec: number
}

export interface InstanceStatus extends InstanceConfig {
  /** Monotonic state revision; see mergeState. */
  rev: number
  state: InstanceState
  pid?: number
  startedAt?: string
  exitCode?: number
  message?: string
  lastSeq: number
}

/** The editable subset the API accepts on create/update. */
export type InstanceInput = Omit<
  InstanceConfig,
  'id' | 'createdAt' | 'command'
> & { command: string[] }

/** Console encodings the daemon accepts, in the order the dropdown shows them. */
export const ENCODING_OPTIONS: { value: string; label: string }[] = [
  { value: 'auto', label: '自动（推荐）' },
  { value: 'utf-8', label: 'UTF-8' },
  { value: 'gbk', label: 'GBK（简体中文）' },
  { value: 'gb18030', label: 'GB18030（简体中文）' },
  { value: 'big5', label: 'Big5（繁体中文）' },
  { value: 'shift_jis', label: 'Shift_JIS（日文）' },
  { value: 'euc-jp', label: 'EUC-JP（日文）' },
  { value: 'euc-kr', label: 'EUC-KR（韩文）' },
  { value: 'windows-1252', label: 'Windows-1252（西欧）' },
  { value: 'iso-8859-1', label: 'ISO-8859-1（西欧）' },
]

export type StreamKind = 'stdout' | 'stderr' | 'system'

export interface ConsoleLine {
  seq: number
  time: string
  stream: StreamKind
  text: string
}

export interface StateInfo {
  rev: number
  state: InstanceState
  pid?: number
  startedAt?: string
  exitCode?: number
  message?: string
}

/** Messages pushed over the console websocket. */
export type ConsoleMessage =
  | { type: 'history'; lines: ConsoleLine[]; state: StateInfo }
  | { type: 'line'; line: ConsoleLine }
  | { type: 'state'; state: StateInfo }
  | { type: 'error'; message: string }
  | { type: 'resync'; message: string }
  | { type: 'pong' }

export interface PropertyEntry {
  key: string
  value: string
}

export interface KnownProperty {
  key: string
  label: string
  type: 'text' | 'number' | 'boolean' | 'select'
  options?: string[]
  hint?: string
  /** What Minecraft uses when the key is absent from the file. */
  default: string
}

export interface PropertiesResponse {
  exists: boolean
  path: string
  entries: PropertyEntry[]
  known: KnownProperty[]
}

export interface EulaStatus {
  exists: boolean
  accepted: boolean
  path: string
}

export interface JarInfo {
  name: string
  size: number
}

// -------------------------------------------------------- core downloads

/** A downloadable server core. "proxy" cores have no world and no EULA. */
export interface CoreProject {
  id: string
  name: string
  kind: 'server' | 'proxy'
  description: string
}

export interface CoreVersion {
  id: string
  /** Upstream support status, e.g. SUPPORTED or UNSUPPORTED. */
  support: string
  /** Lowest Java major version this build runs on, 0 when unknown. */
  javaMinimum: number
  /** False for pre-releases, release candidates and snapshots. */
  stable: boolean
  builds: number
}

export interface CoreBuild {
  build: number
  channel: string
  time: string
  fileName: string
  url: string
  sha256: string
  size: number
}

// --------------------------------------------------------- java runtimes

/** A Java installation the panel can launch servers with. */
export interface JavaRuntime {
  id: string
  path: string
  /** The launcher an instance's config points at; empty if the directory is broken. */
  javaPath: string
  vendor: string
  version: string
  major: number
  imageType: string
  size: number
  installedAt: string
  /** Instances whose launch config points into this runtime. */
  usedBy: string[]
  /** True while one of those instances is running on it. */
  live: boolean
}

export interface SystemJava {
  path: string
  version: string
  major: number
  vendor: string
  source: string
}

export interface JavaPlatform {
  os: string
  arch: string
  /** Non-fatal note, e.g. musl systems where Temurin will not run. */
  warning?: string
}

export type JavaInstallState =
  | 'downloading'
  | 'extracting'
  | 'done'
  | 'failed'
  | 'cancelled'

/** Somewhere the panel can download a Java archive from. */
export interface JavaSource {
  id: string
  name: string
  note: string
  /** The one an install gets when it names no source. */
  default?: boolean
}

export interface JavaInstallJob {
  major: number
  imageType: string
  /** The source serving this download — the fallback's, if one kicked in. */
  source?: string
  version: string
  fileName: string
  total: number
  downloaded: number
  state: JavaInstallState
  error?: string
  runtimeId?: string
  startedAt: string
  finishedAt?: string
}

export interface JavaOverview {
  root: string
  platform: JavaPlatform
  runtimes: JavaRuntime[]
  system: SystemJava | null
  job: JavaInstallJob | null
  /** Where an install can download from, automatic first. */
  sources: JavaSource[]
  /** The source the last install used; what the picker starts on. */
  source: string
}

export interface JavaMajor {
  major: number
  lts: boolean
  installed: boolean
}

export type CoreDownloadState = 'downloading' | 'done' | 'failed' | 'cancelled'

/** The panel-wide download slot; cores land in the library, not in an instance. */
export interface CoreDownloadJob {
  project: string
  projectName: string
  version: string
  build: number
  channel: string
  fileName: string
  total: number
  downloaded: number
  state: CoreDownloadState
  error?: string
  /** The library entry a finished download produced. */
  coreId?: string
  startedAt: string
  finishedAt?: string
}

/** One server jar kept in the panel-wide library, ready to copy into instances. */
export interface ServerCore {
  id: string
  fileName: string
  project: string
  projectName: string
  kind: 'server' | 'proxy' | ''
  version: string
  build: number
  channel: string
  sha256: string
  size: number
  addedAt: string
  /** True for a jar dropped into the library by hand, which has no build info. */
  imported: boolean
  /** Instances whose launch jar has this file name. */
  usedBy: string[]
}

export interface CoreLibrary {
  root: string
  cores: ServerCore[]
  job: CoreDownloadJob | null
}

// ------------------------------------------------------------------ plugins

/** Where a plugin's releases come from. GitHub is the only kind so far. */
export interface PluginSource {
  kind: 'github'
  repo: string
  /** Glob picking the jar when a release publishes several. */
  assetPattern?: string
  /** Include GitHub prereleases in the version list. */
  prerelease?: boolean
  /**
   * A repository only the panel's GitHub token can see. Its releases are read
   * and downloaded through the API instead of the public download host, and
   * never through the mirror.
   */
  private?: boolean
}

/** One release upstream offers, whether or not it has been downloaded. */
export interface PluginRelease {
  tag: string
  name: string
  version: string
  notes: string
  prerelease: boolean
  publishedAt: string
  asset: { name: string; size: number; url: string }
  assets: { name: string; size: number; url: string }[]
}

/** One release the panel has downloaded into the library. */
export interface PluginVersion {
  tag: string
  version: string
  fileName: string
  size: number
  sha256: string
  prerelease: boolean
  notes?: string
  publishedAt: string
  addedAt: string
}

export interface LibraryPlugin {
  id: string
  name: string
  note?: string
  source: PluginSource
  /** Directory inside the instance a copy lands in — "plugins", or "mods". */
  targetDir: string
  addedAt: string
  versions: PluginVersion[]
  /** What the last update check found upstream; cached, see checkedAt. */
  latest?: PluginRelease
  checkedAt?: string
  checkError?: string
  /** Instances that have this plugin installed. */
  usedBy: string[]
}

export type PluginDownloadState = 'downloading' | 'done' | 'failed' | 'cancelled'

export interface PluginDownloadJob {
  pluginId: string
  pluginName: string
  tag: string
  version: string
  fileName: string
  /** Which mirror actually served the bytes — with 自动 on, not obvious. */
  mirror?: string
  total: number
  downloaded: number
  state: PluginDownloadState
  error?: string
  startedAt: string
  finishedAt?: string
}

/** A proxy plugin jars can be downloaded through. Mirrors internal/plugin. */
export interface PluginMirror {
  id: string
  name: string
  note: string
  /** The URL a GitHub link is appended to; absent for a direct download. */
  prefix?: string
  default?: boolean
}

export interface PluginLibrary {
  root: string
  plugins: LibraryPlugin[]
  job: PluginDownloadJob | null
  /** Whether the panel holds a GitHub access token. The token never travels. */
  tokenConfigured: boolean
  /** Last four characters of that token, enough to recognise which one it is. */
  tokenHint?: string
  /** Download proxies to choose between, automatic first. */
  mirrors: PluginMirror[]
  /** The chosen mirror's id, or a custom URL prefix. */
  mirror: string
}

/** What a plugin jar says about itself, read from its own descriptor file. */
export interface PluginJarInfo {
  name?: string
  version?: string
  authors?: string[]
  /** Which server the descriptor was written for: bukkit, paper, velocity… */
  platform?: string
  /** The game version the plugin declares support for, when it declares one. */
  apiVersion?: string
}

/** A library version an unmanaged jar was recognised as, by SHA-256. */
export interface AdoptablePlugin {
  pluginId: string
  name: string
  tag: string
  version: string
}

/** One row of an instance's plugin list: the panel's record joined with disk. */
export interface InstancePlugin {
  /** Addresses this row in the toggle and remove calls. */
  key: string
  pluginId?: string
  name: string
  fileName: string
  dir: string
  enabled: boolean
  /** False for a jar the panel found rather than installed. */
  managed: boolean
  /** True for a plugin the panel installed whose file has since gone. */
  missing: boolean
  size: number
  modified?: string
  tag?: string
  version?: string
  installedAt?: string
  /** Read from the jar for rows the panel did not install. */
  jar?: PluginJarInfo
  /** Set when this jar is byte-for-byte a version the library holds. */
  adoptable?: AdoptablePlugin
}

export interface InstancePluginList {
  entries: InstancePlugin[]
  library: LibraryPlugin[]
  root: string
}

/** True when upstream's newest release is not one the library holds. */
export function hasPluginUpdate(item: LibraryPlugin): boolean {
  if (!item.latest) return false
  return !item.versions.some((version) => version.tag === item.latest?.tag)
}

// ------------------------------------------------------- host directories

export interface HostEntry {
  name: string
  path: string
  isDir: boolean
  size: number
}

export interface HostShortcut {
  label: string
  path: string
}

/** One directory on the machine the panel runs on, for the path picker. */
export interface HostListing {
  path: string
  /** Empty at a filesystem root, which is where "go up" stops. */
  parent: string
  /** False for a path that does not exist yet, which is fine when creating. */
  exists: boolean
  separator: string
  entries: HostEntry[]
  /** The .jar files directly in this directory. */
  jars: JarInfo[]
  truncated: boolean
  /** Set when the directory exists but could not be read, usually permissions. */
  error?: string
  shortcuts: HostShortcut[]
}

export interface User {
  username: string
  version: string
  /** Name of the paired client, set only when a device token authenticated. */
  device?: string
}

/**
 * A paired native client. The panel only ever returns the token itself once,
 * from the pairing call, so it is not part of this type.
 */
export interface Device {
  id: string
  name: string
  createdAt: string
  /** Absent until the device has made its first authenticated request. */
  lastUsed?: string
  /** True for the device making the request; always false in the browser. */
  current: boolean
}

export const STATE_LABELS: Record<InstanceState, string> = {
  stopped: '已停止',
  starting: '启动中',
  running: '运行中',
  stopping: '停止中',
  crashed: '已崩溃',
}

export function isLive(state: InstanceState): boolean {
  return state === 'starting' || state === 'running' || state === 'stopping'
}

/**
 * Folds an update into what we already have about an instance.
 *
 * Config always takes the newest value, but live state only ever moves
 * forward. Two sources race: the console websocket pushes changes the instant
 * they happen, while an HTTP response carries a snapshot taken when the
 * request arrived. Without this guard a slow `POST /start` reply lands after
 * the socket's "running" event and drags the UI back to "starting".
 */
export function mergeState(
  current: InstanceStatus,
  incoming: Partial<InstanceStatus> & { rev?: number },
): InstanceStatus {
  const merged = { ...current, ...incoming }
  if ((incoming.rev ?? 0) < current.rev) {
    return {
      ...merged,
      rev: current.rev,
      state: current.state,
      pid: current.pid,
      startedAt: current.startedAt,
      exitCode: current.exitCode,
      message: current.message,
    }
  }
  return merged
}

// ---------------------------------------------------------------- metrics

export interface MetricSample {
  time: string
  /** top-style: 100 means one core fully busy. */
  cpuPercent: number
  memoryBytes: number
  processes: number
}

export interface InstanceMetrics {
  intervalSeconds: number
  cpuCores: number
  memoryTotal: number
  maxMemoryMB: number
  samples: MetricSample[]
}

export interface HostSample {
  time: string
  cpuPercent: number
  memoryUsed: number
  memoryPercent: number
}

export interface SystemInfo {
  version: string
  goVersion: string
  intervalSeconds: number
  host: {
    hostname: string
    platform: string
    cpuCores: number
    memoryTotal: number
  }
  disk: { path: string; total: number; free: number; percent: number }
  samples: HostSample[]
  panel: { heapBytes: number; goroutines: number }
  instances: { total: number; running: number }
}

// --------------------------------------------------------------- updates

export type UpdatePhase =
  | 'idle'
  | 'checking'
  | 'downloading'
  | 'installing'
  | 'restarting'

export interface UpdateStatus {
  currentVersion: string
  latestVersion?: string
  updateAvailable: boolean
  releaseUrl?: string
  releaseNotes?: string
  publishedAt?: string
  checkedAt?: string
  /** Set when the last check could not reach GitHub; the cached result stands. */
  checkError?: string
  phase: UpdatePhase
  progress: number
  /** False for dev builds and platforms the release has no binary for. */
  eligible: boolean
  ineligibleWhy?: string
  /** Set when the last update attempt failed. */
  error?: string
  /** Download proxy prefix; empty means downloads go straight to GitHub. */
  mirror: string
  /** Which releases this panel is offered. */
  channel: UpdateChannel
  /** True when the running binary is a snapshot or rc rather than a release. */
  currentIsSnapshot: boolean
  /** True when the offered version is a snapshot or rc. */
  latestIsPrerelease: boolean
  /** True when installing the offered version moves backwards — the way back
   *  from a snapshot to the stable track. */
  downgrade: boolean
}

export type UpdateChannel = 'stable' | 'snapshot'

/** The two release channels, as offered on the update page. */
export const UPDATE_CHANNELS: {
  label: string
  value: UpdateChannel
  note: string
}[] = [
  {
    label: '正式版',
    value: 'stable',
    note: '只更新到正式发布的版本，生产环境用这个',
  },
  {
    label: '快照',
    value: 'snapshot',
    note: 'main 分支每次通过 CI 的提交都会出一版，尝鲜用，可能有未完成的功能',
  },
]

/** Known GitHub download proxies. The panel accepts any prefix, these are just
 *  the ones offered without typing. */
export const UPDATE_MIRRORS: { label: string; value: string; note: string }[] = [
  {
    label: 'ghfast.top（默认）',
    value: 'https://ghfast.top/',
    note: '国内访问 GitHub 下载较快',
  },
  {
    label: 'gh-proxy.com',
    value: 'https://gh-proxy.com/',
    note: '备选，用法相同',
  },
  {
    label: '直连 GitHub',
    value: '',
    note: '不经过任何第三方，海外服务器选这个',
  },
]

// ----------------------------------------------------------------- files

export interface FileEntry {
  name: string
  path: string
  isDir: boolean
  size: number
  modified: string
  editable: boolean
  symlink: boolean
}

export interface FileListing {
  path: string
  root: string
  entries: FileEntry[]
  maxEditableBytes: number
  maxUploadBytes: number
}

// -------------------------------------------------------------- terminal

/** Mirrors terminalStatus in internal/api/handlers_terminal.go. */
export interface TerminalStatus {
  /** The operator's switch. */
  enabled: boolean
  /** Whether this platform can run a shell at all; false on Windows. */
  supported: boolean
  /** The program a session would run, e.g. /bin/bash. */
  shell: string
  /** The account the panel — and so the shell — runs as. */
  user: string
  /** Where a new shell starts. */
  cwd: string
  /** Why the terminal is unavailable; empty when it is merely switched off. */
  reason?: string
  /** Shells open right now. */
  live: number
}
