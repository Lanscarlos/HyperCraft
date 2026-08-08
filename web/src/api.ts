import type {
  ConsoleLine,
  EulaStatus,
  InstanceInput,
  InstanceStatus,
  JarInfo,
  PropertiesResponse,
  PropertyEntry,
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
  listJars: (id: string) => request<JarInfo[]>('GET', `/api/instances/${id}/jars`),
}

/** Absolute ws:// URL for an instance console, matching the page's scheme. */
export function consoleSocketURL(id: string): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/instances/${id}/console`
}
