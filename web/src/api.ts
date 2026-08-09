import type {
  ConsoleLine,
  CoreBuild,
  CoreDownloadJob,
  CoreLibrary,
  CoreProject,
  CoreVersion,
  Device,
  EulaStatus,
  HostListing,
  JavaMajor,
  JavaOverview,
  JavaInstallJob,
  InstanceInput,
  FileListing,
  InstanceMetrics,
  InstancePluginList,
  InstanceStatus,
  LibraryPlugin,
  PluginDownloadJob,
  PluginLibrary,
  PluginRelease,
  PropertiesResponse,
  PropertyEntry,
  SystemInfo,
  TerminalStatus,
  UpdateChannel,
  UpdateStatus,
  User,
} from './types'

/**
 * Sent on every mutating request. The server rejects state changes without it,
 * which is what stops another site from acting through a logged-in browser.
 */
const CSRF_HEADER = 'X-HyperCraft'

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }

  /** True when the session is gone and the UI should return to the login screen. */
  get isUnauthorized(): boolean {
    return this.status === 401
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = { [CSRF_HEADER]: '1' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const response = await fetch(path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (response.status === 204) return undefined as T
  const text = await response.text()
  const parsed = text ? JSON.parse(text) : undefined

  if (!response.ok) {
    const message =
      (parsed && typeof parsed.error === 'string' && parsed.error) ||
      `请求失败 (HTTP ${response.status})`
    throw new ApiError(response.status, message)
  }
  return parsed as T
}

export const api = {
  login: (username: string, password: string) =>
    request<User>('POST', '/api/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/auth/logout'),
  me: () => request<User>('GET', '/api/auth/me'),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>('POST', '/api/auth/password', {
      currentPassword,
      newPassword,
    }),

  listDevices: () => request<Device[]>('GET', '/api/auth/devices'),
  // Pairing is deliberately not here: it takes the password rather than a
  // session, and the browser has no use for a token it cannot store safely.
  // The UI can only list and unpair.
  deleteDevice: (id: string) =>
    request<void>('DELETE', `/api/auth/devices/${id}`),

  listInstances: () => request<InstanceStatus[]>('GET', '/api/instances'),
  getInstance: (id: string) =>
    request<InstanceStatus>('GET', `/api/instances/${id}`),
  createInstance: (input: Partial<InstanceInput>) =>
    request<InstanceStatus>('POST', '/api/instances', input),
  updateInstance: (id: string, input: InstanceInput) =>
    request<InstanceStatus>('PUT', `/api/instances/${id}`, input),
  deleteInstance: (id: string, deleteFiles: boolean) =>
    request<void>(
      'DELETE',
      `/api/instances/${id}?deleteFiles=${deleteFiles ? 'true' : 'false'}`,
    ),

  power: (id: string, action: 'start' | 'stop' | 'restart' | 'kill') =>
    request<InstanceStatus>('POST', `/api/instances/${id}/${action}`),
  sendCommand: (id: string, command: string) =>
    request<void>('POST', `/api/instances/${id}/command`, { command }),
  logsSince: (id: string, since: number) =>
    request<{ lines: ConsoleLine[]; lastSeq: number }>(
      'GET',
      `/api/instances/${id}/logs?since=${since}`,
    ),

  getProperties: (id: string) =>
    request<PropertiesResponse>('GET', `/api/instances/${id}/properties`),
  saveProperties: (id: string, entries: PropertyEntry[]) =>
    request<PropertiesResponse>('PUT', `/api/instances/${id}/properties`, {
      entries,
    }),
  getEula: (id: string) =>
    request<EulaStatus>('GET', `/api/instances/${id}/eula`),
  acceptEula: (id: string) =>
    request<EulaStatus>('POST', `/api/instances/${id}/eula`),

  listCoreProjects: () => request<CoreProject[]>('GET', '/api/downloads/projects'),
  listCoreVersions: (project: string) =>
    request<CoreVersion[]>('GET', `/api/downloads/projects/${project}/versions`),
  latestCoreBuild: (project: string, version: string) =>
    request<CoreBuild>(
      'GET',
      `/api/downloads/projects/${project}/versions/${encodeURIComponent(version)}/build`,
    ),

  coreLibrary: () => request<CoreLibrary>('GET', '/api/cores'),
  startCoreDownload: (input: { project: string; version: string; overwrite?: boolean }) =>
    request<CoreDownloadJob>('POST', '/api/cores', {
      project: input.project,
      version: input.version,
      overwrite: input.overwrite ?? false,
    }),
  cancelCoreDownload: () => request<void>('POST', '/api/cores/cancel'),
  deleteCore: (id: string) => request<void>('DELETE', `/api/cores/${encodeURIComponent(id)}`),
  /** Copies a core out of the library into an instance directory. */
  applyCore: (
    id: string,
    input: { coreId: string; setAsJar: boolean; overwrite?: boolean },
  ) =>
    request<{ fileName: string; instance: InstanceStatus }>(
      'POST',
      `/api/instances/${id}/core`,
      {
        coreId: input.coreId,
        setAsJar: input.setAsJar,
        overwrite: input.overwrite ?? false,
      },
    ),

  pluginLibrary: () => request<PluginLibrary>('GET', '/api/plugins'),
  addPlugin: (input: {
    name: string
    repo: string
    assetPattern?: string
    prerelease?: boolean
    private?: boolean
    targetDir?: string
    note?: string
  }) =>
    request<LibraryPlugin>('POST', '/api/plugins', {
      name: input.name,
      repo: input.repo,
      assetPattern: input.assetPattern ?? '',
      prerelease: input.prerelease ?? false,
      private: input.private ?? false,
      targetDir: input.targetDir ?? '',
      note: input.note ?? '',
    }),
  editPlugin: (
    id: string,
    input: {
      name: string
      repo: string
      assetPattern?: string
      prerelease?: boolean
      private?: boolean
      targetDir?: string
      note?: string
    },
  ) =>
    request<LibraryPlugin>('PUT', `/api/plugins/${encodeURIComponent(id)}`, {
      name: input.name,
      repo: input.repo,
      assetPattern: input.assetPattern ?? '',
      prerelease: input.prerelease ?? false,
      private: input.private ?? false,
      targetDir: input.targetDir ?? '',
      note: input.note ?? '',
    }),
  /**
   * Stores the GitHub token private repositories are read with, or clears it
   * with an empty string. Write-only: nothing reads it back out.
   */
  setPluginToken: (token: string) =>
    request<PluginLibrary>('PUT', '/api/plugins/config/token', { token }),
  /** Chooses the proxy plugin jars download through: an id, or a URL prefix. */
  setPluginMirror: (mirror: string) =>
    request<PluginLibrary>('PUT', '/api/plugins/config/mirror', { mirror }),
  deletePlugin: (id: string) => request<void>('DELETE', `/api/plugins/${encodeURIComponent(id)}`),
  /** Asks upstream what versions exist. Always a network round trip. */
  pluginReleases: (id: string) =>
    request<PluginRelease[]>('GET', `/api/plugins/${encodeURIComponent(id)}/releases`),
  checkPlugin: (id: string) =>
    request<LibraryPlugin>('POST', `/api/plugins/${encodeURIComponent(id)}/check`),
  checkPlugins: () => request<PluginLibrary>('POST', '/api/plugins/check'),
  /** Downloads one release into the library. Empty tag means the newest. */
  downloadPlugin: (id: string, tag: string) =>
    request<PluginDownloadJob>('POST', `/api/plugins/${encodeURIComponent(id)}/download`, { tag }),
  cancelPluginDownload: () => request<void>('POST', '/api/plugins/cancel'),
  deletePluginVersion: (id: string, tag: string) =>
    request<void>(
      'DELETE',
      `/api/plugins/${encodeURIComponent(id)}/versions?tag=${encodeURIComponent(tag)}`,
    ),

  instancePlugins: (id: string) =>
    request<InstancePluginList>('GET', `/api/instances/${id}/plugins`),
  /** Installs a library version, or swaps the version already installed. */
  installInstancePlugin: (id: string, pluginId: string, tag: string) =>
    request<void>('POST', `/api/instances/${id}/plugins`, { pluginId, tag }),
  setInstancePluginEnabled: (id: string, key: string, enabled: boolean) =>
    request<void>('PUT', `/api/instances/${id}/plugins`, { key, enabled }),
  /** Starts tracking a jar the panel found, once it matched a library version. */
  adoptInstancePlugin: (id: string, key: string) =>
    request<void>('POST', `/api/instances/${id}/plugins/adopt`, { key }),
  uninstallInstancePlugin: (id: string, key: string) =>
    request<void>('DELETE', `/api/instances/${id}/plugins?key=${encodeURIComponent(key)}`),

  /** Lists a directory on the host. Empty path means the panel's servers root. */
  browseHost: (dir: string) =>
    request<HostListing>('GET', `/api/fs?path=${encodeURIComponent(dir)}`),

  javaOverview: () => request<JavaOverview>('GET', '/api/java'),
  javaMajors: () => request<JavaMajor[]>('GET', '/api/java/available'),
  installJava: (major: number, imageType: 'jre' | 'jdk', source: string) =>
    request<JavaInstallJob>('POST', '/api/java/install', { major, imageType, source }),
  cancelJavaInstall: () => request<void>('POST', '/api/java/install/cancel'),
  deleteJavaRuntime: (id: string) =>
    request<void>('DELETE', `/api/java/${encodeURIComponent(id)}`),

  updateStatus: () => request<UpdateStatus>('GET', '/api/update'),
  checkUpdate: () => request<UpdateStatus>('POST', '/api/update/check'),
  applyUpdate: () => request<UpdateStatus>('POST', '/api/update/apply'),
  setUpdateMirror: (mirror: string) =>
    request<UpdateStatus>('PUT', '/api/update/mirror', { mirror }),
  setUpdateChannel: (channel: UpdateChannel) =>
    request<UpdateStatus>('PUT', '/api/update/channel', { channel }),

  terminalStatus: () => request<TerminalStatus>('GET', '/api/terminal'),
  setTerminalEnabled: (enabled: boolean) =>
    request<TerminalStatus>('PUT', '/api/terminal', { enabled }),

  system: () => request<SystemInfo>('GET', '/api/system'),
  instanceMetrics: (id: string) =>
    request<InstanceMetrics>('GET', `/api/instances/${id}/metrics`),

  listFiles: (id: string, dir: string) =>
    request<FileListing>('GET', `/api/instances/${id}/files?path=${encodeURIComponent(dir)}`),
  readFile: (id: string, filePath: string) =>
    request<{ path: string; content: string }>(
      'GET',
      `/api/instances/${id}/files/content?path=${encodeURIComponent(filePath)}`,
    ),
  writeFile: (id: string, filePath: string, content: string) =>
    request<void>('PUT', `/api/instances/${id}/files/content`, {
      path: filePath,
      content,
    }),
  mkdir: (id: string, dir: string) =>
    request<void>('POST', `/api/instances/${id}/files/mkdir`, { path: dir }),
  renameFile: (id: string, from: string, to: string) =>
    request<void>('POST', `/api/instances/${id}/files/rename`, { from, to }),
  deleteFile: (id: string, filePath: string) =>
    request<void>('DELETE', `/api/instances/${id}/files?path=${encodeURIComponent(filePath)}`),
}

/**
 * Reads the running panel's version, or null while it is unreachable. Used to
 * watch for the panel coming back after a self-update: it is unauthenticated,
 * so it still answers before the session cookie is presented, and it never
 * throws — a failure here is the expected state mid-restart.
 */
export async function panelVersion(): Promise<string | null> {
  try {
    const response = await fetch('/api/health', {
      credentials: 'same-origin',
      cache: 'no-store',
    })
    if (!response.ok) return null
    const body = (await response.json()) as { version?: unknown }
    return typeof body.version === 'string' ? body.version : null
  } catch {
    return null
  }
}

/** Absolute ws:// URL for an instance console, matching the page's scheme. */
export function consoleSocketURL(id: string): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/instances/${id}/console`
}

/**
 * Absolute ws:// URL for a host shell, sized to the terminal that will drive it.
 *
 * The size is a query parameter rather than a first message because the shell
 * is started during the handshake: getting it right up front is what stops a
 * prompt from being drawn at 80x24 and reflowing a moment later.
 */
export function terminalSocketURL(cols: number, rows: number): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/terminal/session?cols=${cols}&rows=${rows}`
}

/** Direct link for a download; the browser handles the transfer itself. */
export function downloadURL(id: string, filePath: string): string {
  return `/api/instances/${id}/files/download?path=${encodeURIComponent(filePath)}`
}

/**
 * Uploads with XMLHttpRequest rather than fetch: only XHR reports upload
 * progress, and a 300 MB modpack jar with no progress bar looks like a hang.
 */
export function uploadFiles(
  id: string,
  dir: string,
  files: File[],
  onProgress: (fraction: number) => void,
  overwrite = false,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const form = new FormData()
    for (const file of files) form.append('file', file, file.name)

    const query = `path=${encodeURIComponent(dir)}${overwrite ? '&overwrite=true' : ''}`
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/api/instances/${id}/files/upload?${query}`)
    xhr.setRequestHeader(CSRF_HEADER, '1')
    xhr.withCredentials = true

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(event.loaded / event.total)
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve()
        return
      }
      let message = `上传失败 (HTTP ${xhr.status})`
      try {
        const parsed = JSON.parse(xhr.responseText)
        if (parsed?.error) message = parsed.error
      } catch {
        /* non-JSON error body */
      }
      reject(new ApiError(xhr.status, message))
    }
    xhr.onerror = () => reject(new ApiError(0, '上传失败：网络错误'))
    xhr.send(form)
  })
}
