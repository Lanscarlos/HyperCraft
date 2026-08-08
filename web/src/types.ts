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

export interface User {
  username: string
  version: string
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
