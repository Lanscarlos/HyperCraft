import { useEffect, useMemo, useRef, useState } from 'react'

import { DUR } from '../motion'
import type { Route } from '../routes'
import { HOST_SECTIONS, LIBRARY_SECTIONS, SETTINGS_SECTIONS } from '../routes'
import type { InstanceStatus } from '../types'
import { STATE_LABELS, isLive } from '../types'
import { useDismiss } from '../useDismiss'
import { Icon } from './Icon'

interface Props {
  instances: InstanceStatus[]
  onClose: () => void
  onNavigate: (route: Route) => void
  onCreate: () => void
}

interface Entry {
  id: string
  group: string
  label: string
  /** Extra words the query can match without being shown as the label. */
  keywords?: string
  hint?: string
  state?: InstanceStatus['state']
  run: () => void
}

/**
 * ⌘K: the way across the panel that does not go through the sidebar.
 *
 * The sidebar deliberately shows a handful of servers, which is only workable
 * if there is a fast path to the other twenty-five. This is it — and because
 * the pages are in the same list, it doubles as the way to reach 磁盘 or
 * 插件源 without first remembering which group they were filed under.
 */
export function CommandPalette({ instances, onClose, onNavigate, onCreate }: Props) {
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const input = useRef<HTMLInputElement | null>(null)
  const listRef = useRef<HTMLDivElement | null>(null)
  // The shortest exit in the panel, and it overlaps the thing it was used to
  // reach: the route changes on the keystroke, the box spends the next ninety
  // milliseconds getting out of the way of a page that is already arriving.
  const { leaving, close } = useDismiss(onClose, DUR.fast)

  useEffect(() => {
    input.current?.focus()
  }, [])

  const entries = useMemo<Entry[]>(() => {
    const go = (route: Route) => () => onNavigate(route)
    const items: Entry[] = []

    for (const item of instances) {
      items.push({
        id: `i:${item.id}`,
        group: '实例',
        label: item.name,
        keywords: item.directory,
        hint: STATE_LABELS[item.state],
        state: item.state,
        run: go({ kind: 'instance', id: item.id, section: 'console' }),
      })
    }

    items.push(
      { id: 'p:overview', group: '页面', label: '概览', keywords: 'dashboard shouye 首页', run: go({ kind: 'overview' }) },
      {
        id: 'p:instances',
        group: '页面',
        label: '所有实例',
        keywords: 'instances list',
        run: go({ kind: 'instances', query: '', state: 'all' }),
      },
    )
    for (const section of LIBRARY_SECTIONS) {
      items.push({
        id: `p:lib:${section.id}`,
        group: '页面',
        label: `资源库 · ${section.label}`,
        keywords: section.id,
        run: go({ kind: 'library', section: section.id }),
      })
    }
    for (const section of HOST_SECTIONS) {
      items.push({
        id: `p:host:${section.id}`,
        group: '页面',
        label: `主机 · ${section.label}`,
        keywords: `host node ${section.id}`,
        hint: section.id === 'terminal' ? '整机 shell' : undefined,
        run: go({ kind: 'host', section: section.id }),
      })
    }
    for (const section of SETTINGS_SECTIONS) {
      items.push({
        id: `p:set:${section.id}`,
        group: '页面',
        label: `面板设置 · ${section.label}`,
        keywords: `settings ${section.id}`,
        run: go({ kind: 'settings', section: section.id }),
      })
    }

    items.push({
      id: 'a:new',
      group: '操作',
      label: '新建实例',
      keywords: 'create new server',
      run: onCreate,
    })

    return items
  }, [instances, onNavigate, onCreate])

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (needle === '') {
      // Nothing typed: the running servers first, because that is what you are
      // most likely reaching for, then everything else in its own order.
      const live = entries.filter((entry) => entry.state && isLive(entry.state))
      const rest = entries.filter((entry) => !live.includes(entry))
      return [...live, ...rest].slice(0, 12)
    }
    return entries
      .filter((entry) =>
        `${entry.label} ${entry.keywords ?? ''}`.toLowerCase().includes(needle),
      )
      .slice(0, 20)
  }, [entries, query])

  // A shorter list can leave the cursor past its end.
  useEffect(() => {
    setCursor((value) => Math.min(value, Math.max(0, shown.length - 1)))
  }, [shown.length])

  useEffect(() => {
    listRef.current
      ?.querySelector('[data-active="true"]')
      ?.scrollIntoView({ block: 'nearest' })
  }, [cursor, shown])

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      close()
      return
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setCursor((value) => (shown.length === 0 ? 0 : (value + 1) % shown.length))
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      setCursor((value) => (shown.length === 0 ? 0 : (value - 1 + shown.length) % shown.length))
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      const entry = shown[cursor]
      if (entry) {
        close()
        entry.run()
      }
    }
  }

  let lastGroup = ''

  return (
    <div
      className="palette"
      data-state={leaving ? 'out' : 'in'}
      role="dialog"
      aria-modal="true"
      aria-label="搜索与跳转"
    >
      <div className="palette__scrim" onClick={close} />
      <div className="palette__box" onKeyDown={onKeyDown}>
        <div className="palette__field">
          <Icon name="search" />
          <input
            ref={input}
            value={query}
            onChange={(event) => {
              setQuery(event.target.value)
              setCursor(0)
            }}
            placeholder="搜索实例、页面…"
            aria-label="搜索实例或页面"
            autoComplete="off"
            spellCheck={false}
          />
          <kbd className="sidebar__kbd">Esc</kbd>
        </div>

        <div className="palette__list" ref={listRef}>
          {shown.length === 0 && <p className="palette__empty">没有匹配的实例或页面。</p>}
          {shown.map((entry, index) => {
            const head = entry.group !== lastGroup ? entry.group : null
            lastGroup = entry.group
            return (
              <div key={entry.id}>
                {head && <div className="palette__group">{head}</div>}
                <button
                  className="palette__item"
                  data-active={index === cursor ? 'true' : undefined}
                  // Movement, not entry: opening the palette with ⌘K must not
                  // hand the selection to whatever the resting pointer is over.
                  onMouseMove={() => setCursor(index)}
                  onClick={() => {
                    close()
                    entry.run()
                  }}
                >
                  {entry.state ? (
                    <span className={`status__dot status__dot--${entry.state}`} />
                  ) : (
                    <span className="palette__bullet" aria-hidden="true" />
                  )}
                  <span className="palette__label">{entry.label}</span>
                  {entry.hint && <span className="palette__hint">{entry.hint}</span>}
                </button>
              </div>
            )
          })}
        </div>

        <div className="palette__foot">
          <span>↑↓ 选择</span>
          <span>↵ 打开</span>
          <span>Esc 关闭</span>
        </div>
      </div>
    </div>
  )
}
