import type { PluginTab } from '../routes'
import { PLUGIN_TABS } from '../routes'

/**
 * 已装插件 / 获取插件, in both scopes.
 *
 * These two are one page in two states rather than two destinations. "What is
 * on this server" and "what should be" are asked in the same breath, and the
 * answer to the second is only meaningful in the context of the first — which
 * server, which version, which loader. Splitting them across the sidebar would
 * mean leaving the server in order to add something to it, and the panel-wide
 * plugin page would become the only place to install from, which is the one
 * arrangement that guarantees the operator is never where the decision was made.
 *
 * Rendered as a strip rather than as sidebar rows because the pair belongs to
 * the page: the instance scope has no second sidebar level to put them in, and
 * a control that appears in one scope and not the other would be two designs.
 */
export function PluginTabs({
  tab,
  onSelect,
  hrefFor,
  /** Counts beside a tab's label, when there is something worth saying. */
  badges,
}: {
  tab: PluginTab
  onSelect: (tab: PluginTab) => void
  hrefFor: (tab: PluginTab) => string
  badges?: Partial<Record<PluginTab, React.ReactNode>>
}) {
  return (
    <nav className="subtabs" aria-label="插件">
      {PLUGIN_TABS.map((entry) => {
        const current = entry.id === tab
        return (
          <a
            key={entry.id}
            className={`subtabs__tab${current ? ' subtabs__tab--active' : ''}`}
            href={hrefFor(entry.id)}
            aria-current={current ? 'page' : undefined}
            onClick={(event) => {
              // Left click only, and never with a modifier: ⌘-click has to
              // stay a new tab, which is the whole reason these are links.
              if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
              event.preventDefault()
              onSelect(entry.id)
            }}
          >
            {entry.label}
            {badges?.[entry.id]}
          </a>
        )
      })}
    </nav>
  )
}
