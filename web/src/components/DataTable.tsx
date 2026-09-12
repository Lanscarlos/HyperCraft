import type { HTMLAttributes, ReactNode } from 'react'

type Density = 'default' | 'compact' | 'roomy'

interface TableProps extends HTMLAttributes<HTMLDivElement> {
  density?: Density
  children: ReactNode
}

const DENSITY: Record<Density, string> = {
  default: '',
  compact: 'dtable--compact',
  roomy: 'dtable--roomy',
}

/**
 * Every list of rows in the panel that is not a real <table>.
 *
 * There were four: ptable, plugin-table, rows, asset. The first three's
 * container rules were byte-identical apart from the radius, and between them
 * they had four header heights, four row heights and three paddings — with
 * nothing anywhere saying which to pick, so a new page used whichever one it
 * had been copied from.
 *
 * The columns are the only part that is genuinely per-table, and each caller
 * still declares those in its own block class, passed through `className`.
 * Everything else is shared.
 *
 * This is not for tabular data that wants to be a table. `.data-table` is a
 * real <table> with <th>/<td> and stays that way: a div grid with
 * `role="table"` only approximates the row-and-column navigation a screen
 * reader gets for free from the real thing.
 */
export function DataTable({ density = 'default', className, children, ...rest }: TableProps) {
  const classes = ['dtable', DENSITY[density]]
  if (className) classes.push(className)
  return (
    <div className={classes.filter(Boolean).join(' ')} {...rest}>
      {children}
    </div>
  )
}

export function DataTableHead({
  className,
  children,
  ...rest
}: HTMLAttributes<HTMLDivElement> & { children: ReactNode }) {
  return (
    <div className={['dtable__head', className].filter(Boolean).join(' ')} {...rest}>
      {children}
    </div>
  )
}

export function DataTableRow({
  className,
  children,
  ...rest
}: HTMLAttributes<HTMLDivElement> & { children: ReactNode }) {
  return (
    <div className={['dtable__row', className].filter(Boolean).join(' ')} {...rest}>
      {children}
    </div>
  )
}

export function DataTableEmpty({ children }: { children: ReactNode }) {
  return <div className="dtable__empty">{children}</div>
}
