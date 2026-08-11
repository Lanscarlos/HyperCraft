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
  /**
   * Run the server on a pseudo-terminal rather than pipes. Default true: it is
   * what gives the console the server's own tab completion, output that appears
   * before its newline does, and colour without forcing it. Off falls back to
   * the pipe console, which is the only one that keeps stderr separate.
   */
  tty: boolean
  /** Make the server emit ANSI colour even though its stdout is a pipe. Has no
   *  effect in TTY mode, where the server can see a terminal. */
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
  /** Whether the running process actually got a terminal. Differs from `tty`
   *  when the platform has none, or when opening one failed and the start fell
   *  back to pipes. */
  ttyActive?: boolean
  lastSeq: number
  /** Whether this host can back a console with a pseudo-terminal at all. */
  ttySupported: boolean
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
  ttyActive?: boolean
}

/**
 * Text messages pushed over the console websocket.
 *
 * A terminal console's output does not appear here: raw bytes arrive in binary
 * frames instead, because splicing them into JSON would break the multi-byte
 * characters and escape sequences that are the point of having a terminal. The
 * opening `history` frame says which protocol the connection speaks, and that
 * does not change while it is open.
 */
export type ConsoleMessage =
  | { type: 'history'; tty: boolean; lines: ConsoleLine[]; state: StateInfo }
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

// ------------------------------------------------------------ 配置历史
//
// A timeline of one server's configuration — not a backup. Worlds, player data
// and databases are outside what it records, and the page has to keep saying
// so; see ConfigHistory.tsx.

/** What caused a snapshot. Drives the badge on each timeline row. */
export type SnapshotTrigger = 'lifecycle' | 'transaction' | 'user' | 'restore'

export interface CommitStats {
  files: number
  insertions: number
  deletions: number
}

export interface ConfigSnapshot {
  ref: string
  short: string
  at: string
  message: string
  trigger: SnapshotTrigger
  author: string
  running?: boolean
  stats: CommitStats
  /** What was installed when this was taken; a restore compares against now. */
  core?: string
  plugins?: string[]
}

export type ChangeStatus = 'added' | 'modified' | 'deleted'

export interface ConfigFileChange {
  path: string
  status: ChangeStatus
  insertions: number
  deletions: number
  binary?: boolean
}

export type DiffLineKind = 'context' | 'add' | 'delete'

export interface DiffLine {
  kind: DiffLineKind
  oldLine: number
  newLine: number
  text: string
  /** A credential. Rendered as `masked` until the operator clicks it. */
  sensitive?: boolean
  masked?: string
}

export interface DiffHunk {
  oldStart: number
  oldCount: number
  newStart: number
  newCount: number
  lines: DiffLine[]
}

export interface ConfigFileDiff {
  path: string
  status: ChangeStatus
  hunks: DiffHunk[]
  binary: boolean
  /** The differ gave up and reported the file as replaced whole. */
  truncated: boolean
  insertions: number
  deletions: number
}

export interface OversizedFile {
  path: string
  size: number
}

export interface ConfigHistoryLimits {
  fileBytes: number
  fileCount: number
  repoBytes: number
}

export interface ConfigHistorySettings {
  disabled?: boolean
  limits: ConfigHistoryLimits
  allow?: string[]
  exclude?: string[]
  compactedAt?: string
  sincePrune?: number
}

export interface ConfigHistoryStats {
  commits: number
  repoBytes: number
  files: number
  compactedAt?: string
}

/** What the rules would record right now, before any snapshot exists. */
export interface ConfigCoverage {
  files: number
  bytes: number
  /** Directories recognised as worlds by their contents, so the page can say
   *  what was skipped and why. */
  worlds?: string[]
  oversized?: OversizedFile[]
  truncated?: boolean
}

export interface ConfigHistoryOverview {
  available: boolean
  enabled: boolean
  reason?: string
  running: boolean
  timeline: ConfigSnapshot[] | null
  pending: ConfigFileChange[] | null
  stats: ConfigHistoryStats
  coverage: ConfigCoverage
  settings: ConfigHistorySettings
  repoPath: string
}

export interface ConfigMismatch {
  coreThen?: string
  coreNow?: string
  plugins?: string[]
}

export interface RestorePlan {
  ref: string
  short: string
  at: string
  message: string
  whole: boolean
  path?: string
  changes: ConfigFileChange[] | null
  removals?: string[]
  mismatch?: ConfigMismatch
  blockedBy?: string
  warning?: string
}

export interface RestoreResult {
  result: { ref?: string; skipped: boolean; reason?: string; stats: CommitStats }
  plan: RestorePlan
}

export interface CompactResult {
  before: number
  after: number
  bytesBefore: number
  bytesAfter: number
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

// ------------------------------------------------------------- databases

/** A database engine the panel knows how to set up. */
export interface DatabaseEngine {
  id: 'mysql' | 'postgresql' | 'mongodb'
  name: string
  note: string
  /** Who publishes the binaries the panel downloads. */
  vendor: string
  defaultPort: number
  adminUser: string
  /** False for MongoDB, whose tarball ships no client to create a user with. */
  password: boolean
  scheme: string
  jdbc?: string
}

/** One engine build unpacked on disk, shared by every database built on it. */
export interface DatabaseInstall {
  id: string
  engine: string
  version: string
  path: string
  serverPath: string
  size: number
  installedAt: string
  /** What the server binary said when it could not load — a missing libaio and
   *  friends — with the command that fixes it. */
  problem?: string
  hint?: string
  /** Databases running on this engine. */
  usedBy: string[]
  live: boolean
}

export type DatabaseState = 'stopped' | 'starting' | 'running' | 'stopping' | 'failed'

/** One database the panel set up: a data directory, a port and a process. */
export interface DatabaseService {
  id: string
  name: string
  engine: string
  version: string
  installId: string
  dir: string
  port: number
  bind: string
  database: string
  user: string
  password: string
  runAs?: string
  autoStart: boolean
  createdAt: string
  state: DatabaseState
  pid?: number
  since: string
  error?: string
  /** True when the engine this database runs on has been deleted. */
  missing: boolean
  /** What to paste into a plugin config; built by the panel so every client
   *  agrees on it. */
  uri: string
  jdbc?: string
}

export interface DatabasePlatform {
  os: string
  arch: string
  distro?: string
  distroVersion?: string
  musl?: boolean
  warning?: string
}

export interface DatabaseVersion {
  version: string
  /** The product line — MySQL 8.0 and 8.4 are different lines, not two patches. */
  series: string
  lts: boolean
  note: string
  installed: boolean
}

export type DatabaseInstallState = JavaInstallState

export interface DatabaseInstallJob {
  engine: string
  version: string
  fileName: string
  total: number
  downloaded: number
  state: DatabaseInstallState
  error?: string
  installId?: string
  startedAt: string
  finishedAt?: string
}

export interface DatabaseOverview {
  root: string
  platform: DatabasePlatform
  engines: DatabaseEngine[]
  installs: DatabaseInstall[]
  services: DatabaseService[]
  job: DatabaseInstallJob | null
}

/** What creating a database needs. Everything but the engine has a default the
 *  panel fills in, so the form can be as short as one click. */
export interface NewDatabase {
  name?: string
  installId: string
  database: string
  user?: string
  password?: string
  port?: number
  bind?: string
  autoStart?: boolean
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

/** Which catalogue a plugin comes from. Mirrors internal/plugin. */
export type PluginSourceKind = 'github' | 'modrinth' | 'hangar' | 'spigot' | 'local'

/** Where a plugin's releases come from. */
export interface PluginSource {
  kind: PluginSourceKind
  /** "owner/name" for GitHub; whatever the registry calls it otherwise. */
  repo: string
  /** Glob picking the jar when a release publishes several. */
  assetPattern?: string
  /** Include GitHub prereleases in the version list. */
  prerelease?: boolean
  /**
   * A repository only an authenticated account can see. Its releases are read
   * and downloaded through the API instead of the public download host, and
   * never through the mirror.
   */
  private?: boolean
  /**
   * Which of the panel's GitHub tokens this source is read with. Empty means
   * the default one — what every source added before there could be more than
   * one says, and what a public repository wants, where a token buys rate limit
   * rather than access.
   */
  tokenId?: string
}

/** One release upstream offers, whether or not it has been downloaded. */
export interface PluginRelease {
  tag: string
  name: string
  version: string
  notes: string
  prerelease: boolean
  publishedAt: string
  asset: PluginAsset
  assets: PluginAsset[]
  /** What a registry published and a GitHub release does not. Absent means
   *  unknown, which is never treated as compatible. */
  gameVersions?: string[]
  loaders?: string[]
  dependencies?: PluginDependency[]
  downloads?: number
  /** Compatibility metadata that describes the plugin rather than this exact
   *  version — all SpigotMC offers for anything but its newest release. */
  unverified?: boolean
}

/**
 * One file published with a release.
 *
 * A release is very often not one file: Hangar publishes a paper build and a
 * velocity build under one version, Modrinth files each platform's jar under
 * the same version number. Those are one release packaged twice, and what
 * tells them apart is `platform` — never the file name.
 */
export interface PluginAsset {
  name: string
  size: number
  url: string
  /** paper / velocity / fabric …, lowercased, as the source names it. Absent
   *  for a GitHub release, which says nothing about its assets. */
  platform?: string
  /** What this jar in particular supports, which on a multi-platform release
   *  is not what the release as a whole supports. */
  loaders?: string[]
  gameVersions?: string[]
  sha256?: string
}

export interface PluginDependency {
  name: string
  required: boolean
  url?: string
}

/**
 * One jar held under a release.
 *
 * The primary key is `sha256` and nothing else. `fileName` is whatever the
 * author happened to call it — the same jar arrives as LuckPerms-Bukkit-5.5.71.jar,
 * luckperms-bukkit.jar and LuckPerms.jar depending on where it came from, and
 * operators rename it again — so it is shown and never decided from.
 *
 * `pluginName` / `pluginVer` come out of the jar's own descriptor, which is the
 * identity the server itself uses: a Bukkit server refuses to load two jars
 * declaring the same name whatever the files are called.
 */
export interface PluginArtifact {
  sha256: string
  fileName: string
  size: number
  pluginName?: string
  pluginVer?: string
  /** bukkit / paper / velocity / bungeecord / fabric, from the descriptor read. */
  platform?: string
  apiVersion?: string
  depend?: string[]
  softDepend?: string[]
  gameVersions?: string[]
  loaders?: string[]
  addedAt?: string
}

/** One release the panel has downloaded into the library, and the jars under
 *  it. A release with several jars is one version, not several — see §1. */
export interface PluginVersion {
  tag: string
  version: string
  artifacts?: PluginArtifact[]
  /** Mirrors of the primary artifact, kept for records written before the
   *  artifact list existed. Read `artifacts` in new code. */
  fileName: string
  size: number
  sha256: string
  prerelease: boolean
  notes?: string
  publishedAt: string
  addedAt: string
  gameVersions?: string[]
  loaders?: string[]
}

/** What the panel does with a plugin without being asked. */
export type PluginUpdateMode = '' | 'notify' | 'fetch' | 'push'

export interface PluginPolicy {
  update?: PluginUpdateMode
  /** Locked to one release tag. A pinned plugin reports no updates and is
   *  skipped by bulk upgrades. */
  pin?: string
  /** Releases retained in the library; 0 means all of them. */
  keep?: number
  /** Silences the hash-mismatch alarm for plugins that rewrite their own jar. */
  allowSelfUpdate?: boolean
}

export interface LibraryPlugin {
  id: string
  name: string
  note?: string
  source: PluginSource
  /** The registry's own artwork, kept from when the plugin was tracked. */
  iconUrl?: string
  /** Directory inside the instance a copy lands in — "plugins", or "mods". */
  targetDir: string
  addedAt: string
  versions: PluginVersion[]
  policy?: PluginPolicy
  /** What the last update check found upstream; cached, see checkedAt. */
  latest?: PluginRelease
  checkedAt?: string
  checkError?: string
  /** Instances that have this plugin installed. */
  usedBy: string[]
}

/** Every jar of a plugin, flattened with its release, for the 版本 tab. */
export function pluginArtifacts(version: PluginVersion): PluginArtifact[] {
  if (version.artifacts && version.artifacts.length > 0) return version.artifacts
  return [{ sha256: version.sha256, fileName: version.fileName, size: version.size }]
}

/** What one release costs on disk, across every jar under it. */
export function versionSize(version: PluginVersion): number {
  return pluginArtifacts(version).reduce((sum, artifact) => sum + artifact.size, 0)
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

/**
 * One GitHub credential the panel holds, as much of it as the panel is willing
 * to describe. The secret itself has no route out of the panel.
 */
export interface PluginTokenInfo {
  id: string
  name: string
  /** Last four characters, enough to recognise which token this is. */
  hint?: string
  /** The token every source that names none is read with. */
  default?: boolean
  /** How many tracked plugins this token answers for. */
  usedBy: number
  /** This token's own quota — each credential has its own ceiling. */
  budget: GitHubBudget
}

export interface PluginLibrary {
  root: string
  plugins: LibraryPlugin[]
  job: PluginDownloadJob | null
  /** The GitHub credentials the panel holds, default first. */
  tokens: PluginTokenInfo[]
  /** Whether the panel holds any token at all, and the default one's tail.
   *  Both are what `tokens` says, kept for older clients. */
  tokenConfigured: boolean
  tokenHint?: string
  /** Download proxies to choose between, automatic first. */
  mirrors: PluginMirror[]
  /** The chosen mirror's id, or a custom URL prefix. */
  mirror: string
  /** The GitHub quota, riding along with the listing rather than being asked
   *  for — asking would spend the thing it reports on. */
  budget: GitHubBudget
}

/** What a plugin jar says about itself, read from its own descriptor file. */
export interface PluginJarInfo {
  name?: string
  version?: string
  /** What the author wrote about the plugin. Prose — it belongs in the drawer,
   *  not in a table cell. */
  description?: string
  authors?: string[]
  /** Which server the descriptor was written for: bukkit, paper, velocity… */
  platform?: string
  /** The game version the plugin declares support for, when it declares one. */
  apiVersion?: string
  /** The plugins the server will refuse to load this one without. A different
   *  list from the registry's — that one is what the author wrote on the
   *  listing page, this one is what the server enforces. */
  depend?: string[]
  /** Wanted if present, and not a reason to refuse. */
  softDepend?: string[]
}

/** What one uploaded jar turned out to be. Per file rather than per request:
 *  a five-jar upload where the third one is a zip lands the other four. */
export interface ImportedPlugin {
  fileName: string
  imported?: {
    plugin: LibraryPlugin
    version: PluginVersion
    /** The jar's own description of itself, so the dialog can show what was
     *  read out rather than only what was stored. */
    info: PluginJarInfo
    /** True when this exact jar was already in the library. */
    replaced: boolean
  }
  error?: string
}

/** A library version an unmanaged jar was recognised as, by SHA-256. */
export interface AdoptablePlugin {
  pluginId: string
  name: string
  tag: string
  version: string
  /** What the library calls this jar — often not what the file on the server
   *  is called, and that mismatch is what a rename looks like. */
  fileName?: string
}

// -------------------------------------------------------- reconciliation

/**
 * How one file compares with the panel's ledger.
 *
 * `foreign` — a jar is there that no record claims. Somebody uploaded it.
 * `drift`   — the record's file is there and its bytes have changed. Tampered
 *             with, or the plugin updated itself, which some genuinely do.
 * `missing` — the record names a file that is not there. Deleted by hand.
 *
 * These outrank every version state on a row: 有更新 computed from a ledger
 * that does not describe the directory is a sentence about a file that is not
 * there.
 */
export type ReconState = 'ok' | 'drift' | 'missing' | 'foreign'

export interface ReconFinding {
  state: ReconState
  dir: string
  fileName: string
  pluginId?: string
  name: string
  /** The digest the ledger holds, and the one on disk. A missing file has no
   *  `found`; a foreign jar has no `expected`. */
  expected?: string
  found?: string
  jar?: PluginJarInfo
  adoptable?: AdoptablePlugin
  /** True for drift on a plugin with 允许自更新 on: recorded, shown, and
   *  deliberately not counted as a problem. */
  allowed?: boolean
}

export interface ReconReport {
  instanceId: string
  at: string
  checked: number
  ok: number
  drift: number
  missing: number
  foreign: number
  findings: ReconFinding[]
}

/** One end of an upgrade: which release, which jar, which bytes. */
export interface SnapshotSide {
  tag?: string
  version?: string
  sha256?: string
  fileName?: string
}

/** What an upgrade left behind, and everything a rollback reads. */
export interface PluginSnapshot {
  id: string
  pluginId: string
  pluginName?: string
  dir: string
  at: string
  by?: string
  action: string
  /** Absent for a first install, which is what makes "there is nothing to roll
   *  back to" a fact rather than a guess. */
  from?: SnapshotSide
  to: SnapshotSide
  /** Jars swept out of the directory before the new one went in. */
  removed?: string[]
  configDir?: string
  configSaved: boolean
  /** What the snapshot could not do, in the operator's own terms. It changes
   *  what a rollback would restore, so it goes on the rollback button. */
  note?: string
}

export interface InstallResult {
  entry: InstancePlugin
  snapshot: PluginSnapshot
}

/** What the panel can see of a GitHub repository before anybody agrees to
 *  track it. One API call answers all four of the ways adding a source fails. */
export interface SourcePreview {
  repo: string
  reachable: boolean
  private: boolean
  error?: string
  /** The failure a token would fix — the one worth offering a way out of. */
  needsToken?: boolean
  release?: string
  version?: string
  publishedAt?: string
  assets: { name: string; size: number }[]
  /** The jar the panel would take today, given `pattern`. */
  picked?: string
  pattern?: string
  /** A pattern derived from that choice, offered and never applied. */
  suggest?: string
  releases: number
}

/** GitHub's remaining API quota, as of the last call the panel made. Read off
 *  headers rather than asked for — the point of showing it is that it is
 *  scarce, so spending a call to learn how many are left would be a joke. */
export interface GitHubBudget {
  limit: number
  remaining: number
  resetAt?: string
  /** Which ceiling this is measured against: 60/hour anonymous, 5000 with a
   *  token. Without it the number is unreadable. */
  authenticated: boolean
  seenAt?: string
}

// ------------------------------------------------------ compatibility

/**
 * Whether a plugin runs on a given server.
 *
 * Three states, not two. A source that publishes no compatibility metadata is
 * common, and the only honest reading of silence is that nobody knows —
 * showing it green is how someone restarts into a server that will not boot.
 */
export type CompatState = 'ok' | 'bad' | 'unknown'

export interface PluginCompat {
  state: CompatState
  /** What the badge reads: 兼容 1.20.4 / 最高支持 1.16.5 / 不支持 Paper. */
  label: string
  /** The whole supported range, for the tooltip. */
  detail?: string
}

/** What server a plugin would be installed into. Either field may be empty,
 *  and empty is honest rather than defaulted. */
export interface PluginTarget {
  mcVersion?: string
  loader?: string
  /** Where the panel learned it: version-history, core-library or jar-name. */
  source?: string
}

/** Why a plugin is not running, read out of the server's own startup output. */
export interface PluginFailure {
  plugin: string
  file?: string
  kind: 'dependency' | 'incompatible' | 'java' | 'error'
  reason: string
  /** Dependency names to install, for the dependency kind. */
  missing?: string[]
  line: string
}

/** A plugin change the running server has not seen. Every plugin change is
 *  one: the directory is read once, at startup. */
export interface PendingPluginChange {
  key: string
  name: string
  action: 'install' | 'upgrade' | 'remove' | 'enable' | 'disable'
  at: string
  label: string
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
  /** What the jar declares about itself, read out of its plugin.yml — for
   *  every row, not only the ones the panel did not install. */
  jar?: PluginJarInfo
  /** The other jars here declaring the same plugin name. The server loads one
   *  of them and silently refuses the rest, so this is a failure waiting to
   *  happen rather than a tidiness problem. Empty for the normal case. */
  conflicts?: string[]
  /** Set when this jar is byte-for-byte a version the library holds. */
  adoptable?: AdoptablePlugin
  /** How this row's file compares with the ledger. Outranks every version
   *  badge on the row — see ReconState. */
  recon?: ReconState
  /** What is on disk now, and what the ledger expected. Both shown for a
   *  drift, because the useful question is which one you recognise. */
  sha256?: string
  recordSha?: string
  checkedAt?: string
  /** This plugin's 允许自更新 setting, so the row can say why a drift is
   *  reported quietly rather than as a problem. */
  selfUpdate?: boolean
  /** Whether this jar suits the server's version and loader. */
  compat?: PluginCompat
  /** The newest version in the library *this server* could move to, and which
   *  jar of it. Decided by the panel rather than by taking the top of the
   *  version list: a cross-platform plugin publishes its proxy and its Fabric
   *  builds under numbers newer than the one a Paper server is running, and
   *  those are not newer versions here. Absent when there is nothing to
   *  offer. */
  update?: PluginUpdateOffer
  /** What the server said when it could not load this plugin. */
  failure?: PluginFailure
  /** Set when this row has a change the running server has not seen. */
  pendingAction?: PendingPluginChange['action']
  /** The plugin's own directory inside the instance, for the file-manager
   *  shortcut. Offered whether or not it exists yet. */
  configDir?: string
  gameVersions?: string[]
  loaders?: string[]
}

/** A version one server could move up to, and the jar of it that fits. */
export interface PluginUpdateOffer {
  tag: string
  version: string
  sha256?: string
  fileName?: string
  platform?: string
}

export interface InstancePluginList {
  entries: InstancePlugin[]
  library: LibraryPlugin[]
  root: string
  /** What this server turned out to be — the basis of every badge on the page. */
  target: PluginTarget
  pending: PendingPluginChange[]
  /** Whether there is a process to restart. Decides between 待重启生效 and
   *  下次启动时生效. */
  live: boolean
  failures: PluginFailure[]
  /** False when the server has not run since the panel started, in which case
   *  an empty failure list means "nothing to read", not "nothing wrong". */
  logAvailable: boolean
}

// ------------------------------------------------------------- discovery

export interface RegistrySource {
  id: PluginSourceKind
  name: string
  note: string
  installable: boolean
}

export interface PluginCategory {
  id: string
  name: string
}

/** One server the 安装到 block offers. */
export interface InstallTarget {
  id: string
  name: string
  state: InstanceState
  target: PluginTarget
  /** Where a plugin would land here — "mods" on a Fabric server. */
  pluginDir: string
}

/** One search result, from whichever registry produced it. */
export interface PluginListing {
  source: PluginSourceKind
  id: string
  name: string
  author?: string
  summary?: string
  iconUrl?: string
  downloads: number
  updated?: string
  loaders?: string[]
  gameVersions?: string[]
  categories?: string[]
  pageUrl?: string
  /** False for a resource the panel can only link to — an externally hosted
   *  or paid SpigotMC entry. */
  downloadable: boolean
  compat?: PluginCompat
}

/** One section of the curated shelf the empty query shows. */
export interface PluginPickGroup {
  id: string
  name: string
  note?: string
  listings: PluginListing[]
}

export interface PluginBrowseResult {
  sources: RegistrySource[]
  categories: PluginCategory[]
  targets: InstallTarget[]
  listings: PluginListing[]
  /** Sent instead of `listings` when nothing has been typed and no category
   *  chosen — see api.browsePlugins. Absent for a real search. */
  picks?: PluginPickGroup[]
  /** Per source, why it contributed nothing. A source that worked is absent. */
  notes?: Record<string, string>
  truncated: boolean
  /** How many results do not fit the target, for the count line. */
  incompatible: number
}

/**
 * Which of my servers can take which of the versions I already hold.
 *
 * Keyed by version tag, then by instance id; a missing or null verdict means
 * the source published nothing to judge by, which is a real answer and not a
 * green light. Asked as a matrix rather than per chosen version because a
 * plugin that ships one jar per platform — LuckPerms publishes bukkit,
 * velocity, fabric and forge under the same release number — has a different
 * right answer per server, and the picker needs that before the choice.
 */
export interface PluginInstallTargets {
  targets: InstallTarget[]
  /** Keyed by version tag, then instance id: can this release go on that
   *  server. A release's verdict is the best of its jars'. */
  verdicts: Record<string, Record<string, PluginCompat | null>>
  /** Keyed by artifact digest, then instance id: can *this jar* go on that
   *  server. Picking the release does not pick the file — see artifactKey. */
  jars: Record<string, Record<string, PluginCompat | null>>
}

/** How one jar is addressed in the verdict matrix: its digest, which is its
 *  identity, falling back to the file name for a record written before there
 *  were digests. Mirrors plugin.ArtifactKey. */
export function artifactKey(artifact: PluginArtifact): string {
  return artifact.sha256 || artifact.fileName
}

export interface BrowseVersion extends PluginRelease {
  /** Absent when no server was chosen to judge against. */
  compat?: PluginCompat
  /** True when the library already holds a jar of this release, so installing
   *  skips the transfer. */
  held: boolean
  /** Which jars of it, by file name. A release that ships one build per
   *  platform can be half held — the paper jar downloaded, the velocity one
   *  not — and installing onto a proxy then has to download after all. */
  heldJars?: string[]
  /** One verdict per platform build, keyed by file name, and only for a
   *  release that ships several labelled ones.
   *
   *  `compat` above is the release's, folded so that one build fitting makes
   *  the release fit — right for the version list, wrong for the jar that
   *  actually comes down. LuckPerms is compatible with a Velocity proxy and
   *  its Bukkit build is not. */
  builds?: Record<string, PluginCompat | undefined>
}

export interface PluginBrowseDetail {
  listing: PluginListing
  /** The plugin's own long description, as its source publishes it. */
  body: string
  versions: BrowseVersion[]
  target: PluginTarget
  tracked?: { id: string; name: string; usedBy: string[] }
}

// -------------------------------------------------- cross-instance view

/** One instance's copy of a plugin, in the global library's 部署 column. */
export interface PluginUse {
  instanceId: string
  name: string
  state: InstanceState
  version: string
  tag: string
  /** Behind the newest version the library holds *that this server can take*
   *  — the field the page is for. */
  outdated: boolean
  /** Which release that is, and which jar of it. Absent when there is nothing
   *  to move up to: a release whose only build is for another platform is not
   *  an update for this server. */
  update?: PluginUpdateOffer
  /** What the last reconciliation said about this copy. Empty when the two
   *  agree, or when nothing has looked yet. */
  recon?: ReconState
  fileName?: string
  checkedAt?: string
  /** A plugin switched off by renaming its jar is installed and not running —
   *  a third state the version column cannot express. */
  enabled?: boolean
  present?: boolean
  /** The version this instance's last snapshot would restore, absent when
   *  there is nothing to go back to. */
  rollbackTo?: string
  rollbackNote?: string
  configSaved?: boolean
}

/**
 * The seven states a library row can be in, in the order they resolve.
 *
 * The first three are reconciliation: the ledger does not describe the disk.
 * They come first because every state after them is a conclusion drawn from
 * that ledger, and a conclusion drawn from a wrong ledger is not information.
 */
export type PluginStatus =
  | 'missing'
  | 'drift'
  | 'foreign'
  | 'unused'
  | 'update'
  | 'behind'
  | 'ok'

/** A jar on a server that no record claims. Not a library row: it has no
 *  source, no history and no update path, and the one offer it gets — 收编进库
 *  — is the act that would make it one. */
export interface ForeignJar {
  name: string
  version?: string
  instanceId: string
  instance: string
  dir: string
  fileName: string
  adoptable?: AdoptablePlugin
}

export interface PluginOverviewRow {
  id: string
  name: string
  note?: string
  kind: PluginSourceKind
  repo: string
  /** Registry artwork, or the repository owner's avatar for a GitHub source. */
  iconUrl?: string
  used: PluginUse[]
  newest?: string
  newestTag?: string
  upstream?: string
  status: PluginStatus
  size: number
  /** Releases and jars. They differ for a plugin shipping one build per
   *  platform, and that difference is what used to be misreported as
   *  「2 个版本 · 版本不一致」about a plugin that had published one release. */
  versions: number
  artifacts: number
  /** Platforms the newest release ships separate builds for. An explanation,
   *  never a warning: same version, different jars is what correct looks like. */
  variants?: string[]
  pinned?: string
  selfUpdate?: boolean
}

export interface PluginOverview {
  rows: PluginOverviewRow[]
  root: string
  unused: number
  unusedSize: number
  totalSize: number
  foreign: ForeignJar[]
  /** The oldest reconciliation across instances — a fleet is as stale as its
   *  stalest server. Absent when nothing has ever been reconciled. */
  reconciledAt?: string
  /** Instances that have never been reconciled at all. */
  unchecked: number
}

/** The chips across the top of the list, which are both the summary and the
 *  navigation. `all` is not a status, so it is spelled out separately. */
export type PluginFilter = 'all' | PluginStatus

export function statusLabel(status: PluginStatus): string {
  switch (status) {
    case 'missing':
      return '文件缺失'
    case 'drift':
      return '哈希不匹配'
    case 'foreign':
      return '库外来源'
    case 'unused':
      return '未部署'
    case 'update':
      return '库有更新'
    case 'behind':
      return '实例落后'
    default:
      return '已同步'
  }
}

/** Whether a status is the ledger disagreeing with the disk, as opposed to a
 *  version being older than another version. The two are different kinds of
 *  problem and the page colours them differently. */
export function isReconStatus(status: PluginStatus): boolean {
  return status === 'missing' || status === 'drift' || status === 'foreign'
}

/** What a bulk upgrade would touch, for the confirmation. */
export interface BulkImpact {
  plugins: { id: string; name: string; to: string; from: string[] }[]
  instances: { id: string; name: string; state: InstanceState; plugins: string[] }[]
  /** Affected instances that are running — the number the confirmation leads
   *  with. A stopped server takes the change on its next start. */
  restarts: number
}

export interface BulkUpgradeResult {
  impact: BulkImpact
  failures: { instance: string; plugin: string; error: string }[]
  applied: number
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

/** What a directory on the host looks like as a candidate for import. */
export interface HostInspection {
  path: string
  exists: boolean
  /** Set when the directory exists but could not be read, usually permissions. */
  error?: string
  /** The directory's own name, offered as the instance name. */
  name: string
  jars: JarInfo[]
  /** The jar most likely to start this server; empty when there is none. */
  jar?: string
  properties?: {
    motd?: string
    port?: string
    levelName?: string
    maxPlayers?: string
  }
  eula: 'accepted' | 'declined' | 'missing'
  /** Level directories found here — what makes this an existing server. */
  worlds?: string[]
  plugins: number
  mods: number
  /** The panel's verdict: something here says a server has run, or is meant to. */
  server: boolean
  /** Name of the instance already pointing at this directory, if any. */
  takenBy?: string
}

export interface User {
  username: string
  version: string
  /** Name of the paired client, set only when a device token authenticated. */
  device?: string
  /**
   * The two addresses this request arrived on: who the panel believes you are,
   * and the peer it actually spoke to. Identical unless trustedProxies is
   * configured, and the difference is the whole point behind "what address does
   * my panel actually see?".
   */
  client: string
  remote: string
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

/** What happened to the panel's credentials. Mirrors internal/api/authlog.go. */
export type AuthEventKind =
  | 'signin'
  | 'signin-failed'
  | 'throttled'
  | 'paired'
  | 'pair-failed'
  | 'unpaired'
  | 'password-changed'
  | 'token-rejected'

/**
 * One credential event, as kept in the panel's memory. The list is cleared by a
 * restart on purpose — the panel's own log is the durable record.
 */
export interface AuthEvent {
  at: string
  kind: AuthEventKind
  /** Unverified on a failure: it is whatever the caller typed. */
  username?: string
  client: string
  remote: string
  /** Names the device, for pairing events. */
  detail?: string
  /** How many times this exact event repeated in a row. */
  count: number
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
 * Listing order, everywhere a list of servers appears.
 *
 * A crashed server is the one entry that is asking for something, so it goes
 * first even though it is not running; after it the live ones, and the
 * deliberately-stopped ones last. Alphabetical would bury the fire in the
 * middle of the list, which is the one thing the list must not do.
 */
export const STATE_RANK: Record<InstanceState, number> = {
  crashed: 0,
  running: 1,
  starting: 2,
  stopping: 3,
  stopped: 4,
}

/** Sorts by urgency, keeping the API's order — creation order — within a group
 *  so a row never moves under the pointer aiming at it. */
export function byUrgency(instances: InstanceStatus[]): InstanceStatus[] {
  return instances
    .map((item, index) => ({ item, index }))
    .sort((a, b) => STATE_RANK[a.item.state] - STATE_RANK[b.item.state] || a.index - b.index)
    .map((entry) => entry.item)
}

/**
 * Folds an update into what we already have about an instance.
 *
 * Config always takes the newest value, but live state only ever moves
 * forward. Two sources race: the console websocket pushes changes the instant
 * they happen, while an HTTP response carries a snapshot taken when the
 * request arrived. Without this guard a slow `POST /start` reply lands after
 * the socket's "running" event and drags the UI back to "starting".
 *
 * Returns `current` itself when the merge changed nothing, and that identity is
 * load-bearing rather than a micro-optimisation. The instance list is polled
 * every five seconds whether or not anything is happening, and a fresh object
 * per instance per poll is a new array, which is new props for every page under
 * it — so an idle panel re-rendered the dashboard's charts, the file listing
 * and the console's surroundings twelve times a minute, forever. Handing back
 * the same object lets React stop at the top: `setInstances` with an unchanged
 * array bails out of the render entirely.
 */
export function mergeState(
  current: InstanceStatus,
  incoming: Partial<InstanceStatus> & { rev?: number },
): InstanceStatus {
  const merged = { ...current, ...incoming }
  if ((incoming.rev ?? 0) < current.rev) {
    return same(current, {
      ...merged,
      rev: current.rev,
      state: current.state,
      pid: current.pid,
      startedAt: current.startedAt,
      exitCode: current.exitCode,
      message: current.message,
    })
  }
  return same(current, merged)
}

/**
 * `next` unless it is field-for-field what `current` already was.
 *
 * One level deep, which is what InstanceStatus is apart from its three string
 * arrays, compared element-wise below. Anything nested added later needs a case
 * here or it will compare by reference and always look changed; the cost of
 * getting that wrong is a re-render, not a stale screen, since a value that
 * differs is always kept.
 *
 * Compares the union of both key sets, and an absent key reads the same as one
 * explicitly set to undefined — which is not pedantry: the guard above always
 * writes exitCode and message, so a stopped server that never had either ends
 * up with the keys present and undefined. Counting keys instead would call that
 * a change and make the stale-response path — the one case that by definition
 * changes nothing — the one that always re-renders.
 */
function same(current: InstanceStatus, next: InstanceStatus): InstanceStatus {
  const keys = new Set([...Object.keys(current), ...Object.keys(next)])
  for (const key of keys as Set<keyof InstanceStatus>) {
    const a = current[key]
    const b = next[key]
    if (Array.isArray(a) && Array.isArray(b)) {
      if (a.length !== b.length || a.some((item, index) => item !== b[index])) return next
      continue
    }
    if (a !== b) return next
  }
  return current
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
  /** Bytes per second over the interval ending at `time`, every non-loopback
   *  interface summed. Zero on the first sample, which has no interval. */
  netRecvPerSec: number
  netSentPerSec: number
}

/** What the network has carried, counted from when the panel started rather
 *  than from when the machine booted. */
export interface NetUsage {
  /** The interfaces being counted; loopback is not one of them. */
  interfaces: string[] | null
  recvBytes: number
  sentBytes: number
  recvPerSec: number
  sentPerSec: number
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
  net: NetUsage
  samples: HostSample[]
  panel: { heapBytes: number; goroutines: number }
  instances: { total: number; running: number }
}

// --------------------------------------------------------------- updates

export type UpdatePhase =
  | 'idle'
  | 'checking'
  /** Step one: the release is downloading *and* the servers are stopping. */
  | 'downloading'
  /** Step one with the download already finished — waiting on the last world
   *  to save. */
  | 'stopping'
  | 'installing'
  | 'restarting'

/** How far the "stop every server" half of an update has got. */
export interface UpdateShutdown {
  total: number
  stopped: number
  /** Names of the servers still saving. */
  pending?: string[]
}

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
  /** Present while an update is running: the servers it is stopping. */
  shutdown?: UpdateShutdown
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
