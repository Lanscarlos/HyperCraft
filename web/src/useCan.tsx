import { createContext, useCallback, useContext, useMemo, type ReactNode } from 'react'

import type { Capability, User } from './types'

/**
 * What the signed-in account may do, as a question any component can ask.
 *
 * A context rather than a prop, and deliberately: the answer is needed in
 * thirty-odd components that are otherwise unrelated, and threading it through
 * would put a `can` parameter on every layer in between — including the ones
 * that only pass it on. The set never changes within a session, so there is no
 * re-render cost to spreading it this wide.
 *
 * **Hiding is not the barrier.** The panel refuses on the server, per route and
 * per field; this only keeps people from being offered buttons that would come
 * back 403. A missing check here is a rough edge, not a hole — which is the
 * right way round, and the reason this file carries no security decisions of
 * its own.
 */
const CapabilityContext = createContext<ReadonlySet<Capability>>(new Set())

export function CapabilityProvider({ user, children }: { user: User; children: ReactNode }) {
  const held = useMemo(() => new Set(user.capabilities), [user.capabilities])
  return <CapabilityContext.Provider value={held}>{children}</CapabilityContext.Provider>
}

/**
 * Returns a predicate rather than a boolean, so it can be called inside a
 * `.filter()` — which is what most of the callers are doing. A hook taking the
 * capability would have to be called once per capability at the top level, and
 * the nav lists are built in loops.
 */
export function useCan(): (cap: Capability) => boolean {
  const held = useContext(CapabilityContext)
  return useCallback((cap: Capability) => held.has(cap), [held])
}

/**
 * The capability ids this front end gates on.
 *
 * The vocabulary itself lives in internal/authz and is served by
 * GET /api/capabilities — the role editor reads it from there, so a capability
 * added later needs no change here. These constants exist only for the handful
 * of places that hide a specific thing, and they are checked against the real
 * vocabulary by TestFrontendCapabilityIdsExist, because a typo here would
 * silently hide a page from the person who is allowed to use it.
 */
export const CAP = {
  instanceView: 'instance:view',
  instanceConsole: 'instance:console',
  instancePower: 'instance:power',
  instanceFilesRead: 'instance:files:read',
  instanceFilesWrite: 'instance:files:write',
  instancePlugins: 'instance:plugins',
  instanceConfig: 'instance:config',
  instanceHistory: 'instance:confighist',
  instanceSettings: 'instance:settings',
  instanceLaunch: 'instance:launch',
  instanceSchematics: 'instance:schematics',
  instanceDelete: 'instance:delete',

  panelSystem: 'panel:system',
  panelSecurity: 'panel:security',
  panelUsers: 'panel:users',
  panelCreate: 'panel:instances:create',
  panelNetwork: 'panel:network',
  panelJava: 'panel:java',
  panelDatabases: 'panel:databases',
  panelTerminal: 'panel:terminal',
  panelHostFS: 'panel:hostfs',
  panelUpdate: 'panel:update',
  panelSettings: 'panel:settings',
  libraryCores: 'library:cores',
  libraryPlugins: 'library:plugins',
  librarySchematics: 'library:schematics',
} as const satisfies Record<string, Capability>
