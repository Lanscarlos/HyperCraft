import type { ReactNode } from 'react'

import { ask } from '../confirm'
import { formatAgo, formatBytes, formatDate } from '../format'
import { Badge } from './Badge'
import { Button } from './Button'
import { DataTable, DataTableHead, DataTableRow } from './DataTable'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'
import { Note } from './Note'

/**
 * The shelf, as one shape for all five of them.
 *
 * 服务端核心, Java 运行时, 数据库, 插件 and 建筑与地图 are five lists of the
 * same thing: something the panel downloaded once so every instance can be
 * stamped out of it. They differ in what is on the shelf and in nothing else —
 * so the row, the menu on it, the way it says who is using it and the way it
 * refuses to be deleted while somebody is are written once, here, rather than
 * five times with five sets of column widths.
 *
 * What each page still owns is the *content* of three columns: 兼容性 and
 * 运行要求 mean different things per shelf (a Minecraft range, a CPU
 * architecture, a port), and the head labels say which. Everything else — the
 * grid, the heights, which columns a narrow screen drops — is the same list.
 */

/** One row's worth of a shelf, in the order the columns read. */
export interface ResourceEntry {
  id: string
  /** The letter in the 32px tile. The first of the name, normally. */
  tile: string
  name: string
  /** Under the name, in the mono face. The file this row actually is. */
  fileName?: string
  /** Type and state chips beside the name. Only non-routine states, per
   *  docs/design-system.md — a row with nothing wrong with it carries none. */
  chips?: ReactNode
  /** 版本 · 构建. */
  version: ReactNode
  /** 兼容性 — what this runs with. See the page's own head label. */
  compat: ReactNode
  /** 运行要求 — what this needs to run. */
  requires: ReactNode
  size: number
  /** The instances using it. Empty is a real answer and reads as 未使用. */
  usedBy: string[]
  addedAt: string
  menu: MenuItem[]
}

/** The three head labels that differ per shelf. The other five never do. */
export interface ResourceHeads {
  /** 支持 MC / 架构 / 创建版本 … */
  compat: string
  /** 需要 Java / 端口 … */
  requires: string
}

export function ResourceTable({
  heads,
  label,
  children,
}: {
  heads: ResourceHeads
  label: string
  children: ReactNode
}) {
  return (
    <DataTable className="reslist" role="table" aria-label={label}>
      <DataTableHead className="reslist__head" role="row">
        <span role="columnheader">名称</span>
        <span role="columnheader">版本 · 构建</span>
        <span role="columnheader">{heads.compat}</span>
        <span role="columnheader">{heads.requires}</span>
        <span className="reslist__num" role="columnheader">
          体积
        </span>
        <span role="columnheader">使用中</span>
        <span role="columnheader">加入</span>
        {/* The menu column. Its header would name a control that is not there
            until the row is hovered, so it stays empty and the cell below it
            is what carries the label. */}
        <span role="columnheader" aria-label="操作" />
      </DataTableHead>
      {children}
    </DataTable>
  )
}

export function ResourceRow({ entry }: { entry: ResourceEntry }) {
  return (
    <DataTableRow className="reslist__row" role="row">
      <ResourceName entry={entry} />

      <span className="reslist__cell" role="cell">
        {entry.version}
      </span>
      <span className="reslist__cell" role="cell">
        {entry.compat}
      </span>
      <span className="reslist__cell" role="cell">
        {entry.requires}
      </span>
      <span className="reslist__cell reslist__num" role="cell">
        {formatBytes(entry.size)}
      </span>

      <span className="reslist__cell" role="cell">
        <ResourceUsers names={entry.usedBy} />
      </span>

      {/* Relative, because what you want off this column is "is this old", and
          that is a comparison with the rows above and below it. The date
          itself is one hover away for when it is the date you wanted. */}
      <span className="reslist__cell" role="cell" title={formatDate(entry.addedAt)}>
        {formatAgo(entry.addedAt)}
      </span>

      <span role="cell">
        <Menu
          items={entry.menu}
          className="reslist__more"
          ariaLabel={`${entry.name} 的操作`}
          title="更多操作"
        >
          <span aria-hidden="true">⋯</span>
        </Menu>
      </span>
    </DataTableRow>
  )
}

function ResourceName({ entry }: { entry: ResourceEntry }) {
  return (
    <span className="reslist__name" role="cell">
      <span className="reslist__tile" aria-hidden="true">
        {entry.tile.slice(0, 1).toUpperCase()}
      </span>
      <span className="reslist__label">
        <span className="reslist__title">
          <strong title={entry.name}>{entry.name}</strong>
          {entry.chips}
        </span>
        {entry.fileName !== undefined && (
          <code className="reslist__file" title={entry.fileName}>
            {entry.fileName}
          </code>
        )}
      </span>
    </span>
  )
}

/**
 * Who is using it.
 *
 * Empty says so in words rather than leaving the cell blank: a blank cell in a
 * column of names reads as "the panel does not know", and the difference
 * between "nothing uses this" and "we could not tell" is the difference
 * between deleting it and not.
 */
export function ResourceUsers({ names }: { names: string[] }) {
  if (names.length === 0) return <span className="reslist__idle">未使用</span>
  return (
    <span className="reslist__users">
      {names.map((name) => (
        <Badge key={name} title={name}>
          {name}
        </Badge>
      ))}
    </span>
  )
}

/**
 * The shelf's own row while something is still coming down.
 *
 * In the list rather than above it, because the row it is going to become is
 * what the operator is waiting for: a bar at the top of the page and a list
 * that is one row short say the same thing twice and agree with each other
 * only at the end.
 */
export function ResourcePendingRow({
  title,
  fileName,
  downloaded,
  total,
}: {
  title: string
  fileName: string
  downloaded: number
  total: number
}) {
  const fraction = total > 0 ? downloaded / total : 0
  return (
    <DataTableRow className="reslist__row reslist__row--pending" role="row">
      <span className="reslist__name" role="cell">
        <span className="reslist__tile reslist__tile--pending" aria-hidden="true">
          ↓
        </span>
        <span className="reslist__label">
          <span className="reslist__title">
            <strong>{title}</strong>
            <Badge tone="update">下载中</Badge>
          </span>
          <code className="reslist__file">{fileName}</code>
        </span>
      </span>
      <span className="reslist__progress" role="cell">
        <span className="progress">
          <span
            className="progress__bar"
            style={{ width: `${Math.round(fraction * 100)}%` }}
          />
        </span>
        <span className="reslist__idle">
          {total > 0
            ? `${Math.round(fraction * 100)}% · ${formatBytes(downloaded)} / ${formatBytes(total)}`
            : formatBytes(downloaded)}
        </span>
      </span>
    </DataTableRow>
  )
}

/**
 * Asking before a shelf entry is deleted — and refusing when something is
 * using it.
 *
 * The refusal is the point. Every one of the five shelves is something an
 * instance points at, and the panel knows which instances those are, so a
 * delete that would strand one is a question the operator should never be
 * asked in the first place. There is no force: the way past this is to stop
 * using it, which is a change to the instance and belongs on the instance.
 */
export async function confirmResourceDelete({
  name,
  detail,
  usedBy,
  usedByNote,
  onInspect,
}: {
  name: string
  /** What is about to be lost, when nothing is using it. */
  detail: ReactNode
  usedBy: string[]
  /** What being in use actually costs *on this shelf*. A core and a Java
   *  runtime are not in the same danger, and saying they are teaches the
   *  operator to read past this box. */
  usedByNote: (names: string[]) => ReactNode
  /** Where 查看使用者 goes. */
  onInspect?: (names: string[]) => void
}): Promise<boolean> {
  if (usedBy.length > 0) {
    const ok = await ask({
      title: '有实例正在用它，没法删',
      lead: name,
      detail: usedByNote(usedBy),
      confirmLabel: '查看使用者',
      cancelLabel: '取消',
    })
    if (ok) onInspect?.(usedBy)
    return false
  }

  return ask({
    title: '删除这一项？',
    lead: name,
    detail,
    confirmLabel: '删除',
    danger: true,
  })
}

/**
 * What is on the shelf that nothing is using.
 *
 * A shelf only ever grows: every version downloaded to test something is still
 * there a year later, and nothing on the page ever said so. This is the one
 * place that adds up what could go, and it only ever offers to remove entries
 * with no users — see confirmResourceDelete.
 */
export function StorageHygiene({
  idle,
  bytes,
  unit,
  onClean,
  busy,
}: {
  idle: number
  bytes: number
  /** 个核心 / 个运行时 / 个插件 … */
  unit: string
  onClean: () => void
  busy?: boolean
}) {
  return (
    <div className="reshint">
      <div className="reshint__body">
        <strong>
          {idle} {unit}没有被任何实例使用，合计 {formatBytes(bytes)}
        </strong>
        <span className="reshint__note">
          删除只动这几项，正在被实例使用的不会被碰。
        </span>
      </div>
      <Button type="button" onClick={onClean} disabled={busy || idle === 0}>
        清理未使用
      </Button>
    </div>
  )
}

/** The standing note under a list. One line, and it earns its place by being
 *  about something the operator can act on rather than about how the panel
 *  works — see the note this replaced on 服务端核心. */
export function ResourceHint({ children }: { children: ReactNode }) {
  return <Note>{children}</Note>
}
