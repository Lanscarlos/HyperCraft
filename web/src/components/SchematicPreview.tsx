import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'

import { ApiError, api, downloadURL } from '../api'
import { bareName, blockColor, isAirBlock, isTranslucentBlock } from '../blockcolors'
import { formatBytes, formatDate } from '../format'
import { toast } from '../toast'
import type { FileEntry, SchematicPreview as Schematic } from '../types'
import { Modal } from './Modal'
import { Skeleton } from './Skeleton'

/**
 * What is inside a .schem, without pasting it into a world.
 *
 * A schematics folder is thirty files called build_final_v2.schem and the only
 * way to tell them apart used to be to load the world, stand somewhere empty
 * and paste one. The daemon reads the file (internal/schematic) and this draws
 * what it found: a solid view for "which build is this", a top-down view with
 * a height cut for "what is the floor plan", and the block list for "have I got
 * enough spruce".
 *
 * It is a preview and not an editor. Nothing here writes to the file, and
 * nothing places a block in a world.
 *
 * Two things open it now — a file in one server's directory, and an entry in
 * the panel-wide building library — and they differ only in which endpoint
 * hands over the payload. So the dialog takes a loader rather than a file: the
 * renderer below has no idea whose directory anything is in, which is what
 * keeps one way of reading a schematic in this panel instead of two that drift.
 */

type ViewMode = 'solid' | 'plan'

/** How a block's palette entry is treated by the renderer. */
const SKIP = 0
const OPAQUE = 1
const SEE_THROUGH = 2

/**
 * The preview dialog, over whatever `load` fetches.
 *
 * `load` has to be stable — wrap it in useCallback at the call site — because
 * it is the effect's dependency, and a new function per render would re-fetch
 * the schematic on every keystroke anywhere in the page above it.
 */
export function SchematicDialog({
  title,
  lead,
  load,
  actions,
  onClose,
}: {
  title: string
  lead?: ReactNode
  load: () => Promise<Schematic>
  /** Buttons beside 关闭 — 下载, 入库, 安装到实例 — since what you want to do
   *  next depends on where the file you are looking at lives. */
  actions?: ReactNode
  onClose: () => void
}) {
  const [data, setData] = useState<Schematic | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    setData(null)
    setError(null)
    load()
      .then((result) => {
        if (live) setData(result)
      })
      .catch((err: unknown) => {
        if (live) setError(err instanceof Error ? err.message : '读取失败')
      })
    return () => {
      live = false
    }
  }, [load])

  return (
    <Modal onClose={onClose} label={`预览 ${title}`}>
      <div className="modal__card modal__card--schem">
        <h2 className="modal__title">{title}</h2>
        {lead !== undefined && <p className="modal__lead">{lead}</p>}

        {error && <div className="alert alert--error">{error}</div>}
        {!error && !data && <SchematicSkeleton />}
        {data && <SchematicBody data={data} />}

        <div className="modal__actions">
          {actions}
          <button className="btn btn--primary" onClick={onClose}>
            关闭
          </button>
        </div>
      </div>
    </Modal>
  )
}

export function SchematicPreview({
  instanceId,
  entry,
  onClose,
}: {
  instanceId: string
  entry: FileEntry
  onClose: () => void
}) {
  const [imported, setImported] = useState(false)
  const [importing, setImporting] = useState(false)

  const load = useCallback(
    () => api.schematic(instanceId, entry.path),
    [instanceId, entry.path],
  )

  // 入库 is here because this is where the question comes up: you opened a file
  // in one server's schematics folder to find out what it is, and the answer
  // was "the spawn we spent a month on". Keeping it is one click from there
  // rather than a download and an upload through another page.
  const keep = async () => {
    setImporting(true)
    try {
      const stored = await api.importSchematic(instanceId, entry.path)
      setImported(true)
      toast(`已加入建筑库：${stored.name}`)
    } catch (err) {
      // 409 means the same bytes are already held, which is not a failure —
      // it is the answer to the question the button asks.
      if (err instanceof ApiError && err.status === 409) {
        setImported(true)
        toast(err.message)
      } else {
        toast(err instanceof Error ? err.message : '入库失败')
      }
    } finally {
      setImporting(false)
    }
  }

  return (
    <SchematicDialog
      title={entry.name}
      lead={`${formatBytes(entry.size)} · ${formatDate(entry.modified)}`}
      load={load}
      onClose={onClose}
      actions={
        <>
          <a className="btn" href={downloadURL(instanceId, entry.path)} download>
            下载
          </a>
          <button className="btn" onClick={() => void keep()} disabled={importing || imported}>
            {imported ? '已在建筑库' : importing ? '入库中…' : '加入建筑库'}
          </button>
        </>
      }
    />
  )
}

function SchematicSkeleton() {
  return (
    <div className="schem">
      <div className="schem__facts">
        {[0, 1, 2, 3].map((n) => (
          <div className="schem__fact" key={n}>
            <Skeleton w="52px" h={11} />
            <Skeleton w="76px" h={15} />
          </div>
        ))}
      </div>
      <div className="schem__body">
        <Skeleton w="100%" h={320} />
        <Skeleton w="100%" h={320} />
      </div>
    </div>
  )
}

function SchematicBody({ data }: { data: Schematic }) {
  const [view, setView] = useState<ViewMode>('solid')
  const [rotation, setRotation] = useState(0)
  // The height cut, as a y index. Starts at the top, i.e. showing everything.
  const [cut, setCut] = useState(data.height - 1)

  useEffect(() => {
    setCut(data.height - 1)
    setRotation(0)
  }, [data])

  const region = useMemo(() => buildRegion(data), [data])
  const legend = useMemo(() => buildLegend(data), [data])

  const notice = renderNotice(data)

  return (
    <div className="schem">
      <SchematicFacts data={data} kinds={legend.length} />

      {/* The controls sit above both columns rather than above the canvas
          alone: sharing a row with the block list's heading is what lets the
          canvas and the list start and end level. */}
      <div className="schem__controls">
        <div className="schem__modes" role="group" aria-label="视图">
          <button
            className={`chip${view === 'solid' ? ' chip--active' : ''}`}
            onClick={() => setView('solid')}
            disabled={!region}
            aria-pressed={view === 'solid'}
          >
            立体
          </button>
          <button
            className={`chip${view === 'plan' ? ' chip--active' : ''}`}
            onClick={() => setView('plan')}
            disabled={!region}
            aria-pressed={view === 'plan'}
          >
            俯视
          </button>
        </div>
        <button
          className="btn btn--icon btn--row"
          onClick={() => setRotation((r) => (r + 3) % 4)}
          disabled={!region}
          aria-label="逆时针旋转 90 度"
          title="逆时针旋转 90°"
        >
          ⟲
        </button>
        <button
          className="btn btn--icon btn--row"
          onClick={() => setRotation((r) => (r + 1) % 4)}
          disabled={!region}
          aria-label="顺时针旋转 90 度"
          title="顺时针旋转 90°"
        >
          ⟳
        </button>
        {/* The cut is the feature that makes a solid view useful: a roof hides
            everything under it, and this is how you look inside. */}
        <label className="schem__cut">
          <span>高度</span>
          <input
            type="range"
            min={0}
            max={Math.max(0, data.height - 1)}
            value={cut}
            disabled={!region || data.height < 2}
            onChange={(event) => setCut(Number(event.target.value))}
            aria-label="只显示到这一层"
          />
          <output>
            {cut + 1}/{data.height}
          </output>
        </label>
      </div>

      <div className="schem__body">
        {notice ? (
          <p className="schem__notice">{notice}</p>
        ) : (
          <SchematicCanvas region={region} view={view} rotation={rotation} cut={cut} />
        )}
        <BlockLegend legend={legend} total={data.nonAir} />
      </div>
    </div>
  )
}

/** Why there is no picture, or null when there is one. */
function renderNotice(data: Schematic): string | null {
  if (data.nonAir === 0) return '这个 schematic 是空的：选区里只有空气。'
  switch (data.omitted) {
    case 'volume':
      return '选区太大，画不出来。上面的尺寸和方块清单仍然是完整的。'
    case 'runs':
      return '这个建筑太细碎，渲染数据超出了上限。上面的尺寸和方块清单仍然是完整的。'
    case 'palette':
      return '方块种类太多，超出了渲染上限。上面的方块清单仍然是完整的。'
    default:
      return data.blocks ? null : '这个文件没有可渲染的方块数据。'
  }
}

function SchematicFacts({ data, kinds }: { data: Schematic; kinds: number }) {
  const facts: Array<[string, string]> = [
    ['尺寸', `${data.width} × ${data.height} × ${data.length}`],
    ['方块', `${data.nonAir.toLocaleString('zh-CN')} / ${data.volume.toLocaleString('zh-CN')}`],
    ['种类', `${kinds}`],
    ['格式', formatName(data)],
  ]
  const version = minecraftVersion(data.dataVersion)
  if (version) facts.push(['游戏版本', version])
  if (data.author) facts.push(['作者', data.author])
  if (data.name) facts.push(['名称', data.name])
  if (data.created) facts.push(['保存于', formatDate(data.created)])
  if (data.blockEntities > 0) facts.push(['方块实体', `${data.blockEntities}`])
  if (data.entities > 0) facts.push(['实体', `${data.entities}`])

  return (
    <dl className="schem__facts">
      {facts.map(([label, value]) => (
        <div className="schem__fact" key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function formatName(data: Schematic): string {
  return data.format === 'mcedit' ? 'MCEdit (1.12 及更早)' : `Sponge v${data.version}`
}

/* ------------------------------------------------------------ the legend */

interface LegendRow {
  state: string
  label: string
  count: number
  color: string
}

function buildLegend(data: Schematic): LegendRow[] {
  const rows: LegendRow[] = []
  for (let i = 0; i < data.palette.length; i++) {
    const state = data.palette[i]
    const count = data.counts[i] ?? 0
    if (count === 0 || isAirBlock(state)) continue
    rows.push({ state, label: bareName(state), count, color: blockColor(state) })
  }
  rows.sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
  return rows
}

/** How many rows the list shows before it stops. A build with 400 block states
 *  is real, but the tail of it is one stair variant per facing and reading it
 *  is not what anyone opened the preview for. */
const LEGEND_LIMIT = 60

function BlockLegend({ legend, total }: { legend: LegendRow[]; total: number }) {
  const [expanded, setExpanded] = useState(false)
  const shown = expanded ? legend : legend.slice(0, LEGEND_LIMIT)
  const hidden = legend.length - shown.length

  if (legend.length === 0) {
    return (
      <div className="schem__legend">
        <p className="muted">没有方块。</p>
      </div>
    )
  }

  return (
    <div className="schem__legend">
      <h3 className="schem__legend-title">方块清单</h3>
      <ul className="schem__blocks">
        {shown.map((row) => (
          <li key={row.state} title={row.state}>
            <span className="schem__swatch" style={{ background: row.color }} aria-hidden="true" />
            <span className="schem__block-name">{row.label}</span>
            <span className="schem__block-count">{row.count.toLocaleString('zh-CN')}</span>
            {/* The share bar is the whole reason the list is sorted: it turns
                "12480 stone" into "most of this build is stone". */}
            <span
              className="schem__block-share"
              style={{ width: `${total > 0 ? (row.count / total) * 100 : 0}%` }}
              aria-hidden="true"
            />
          </li>
        ))}
      </ul>
      {hidden > 0 && (
        <button className="btn btn--row" onClick={() => setExpanded(true)}>
          还有 {hidden} 种
        </button>
      )}
    </div>
  )
}

/* ------------------------------------------------------------ the canvas */

interface Region {
  width: number
  height: number
  length: number
  blocks: Uint16Array
  /** Per palette index: SKIP, OPAQUE or SEE_THROUGH. */
  kind: Uint8Array
  /** Per palette index, pre-shaded for the three faces of a cube. */
  top: string[]
  right: string[]
  left: string[]
}

function buildRegion(data: Schematic): Region | null {
  if (!data.blocks || data.volume <= 0) return null
  const blocks = decodeRuns(data.blocks, data.volume)
  if (!blocks) return null

  const size = data.palette.length
  const kind = new Uint8Array(size)
  const top: string[] = new Array(size)
  const right: string[] = new Array(size)
  const left: string[] = new Array(size)

  for (let i = 0; i < size; i++) {
    const state = data.palette[i]
    kind[i] = isAirBlock(state) ? SKIP : isTranslucentBlock(state) ? SEE_THROUGH : OPAQUE
    const color = blockColor(state)
    top[i] = color
    // One light source, up and to the left, which is what makes a grid of
    // squares read as cubes at all.
    right[i] = shade(color, 0.78)
    left[i] = shade(color, 0.56)
  }
  return { width: data.width, height: data.height, length: data.length, blocks, kind, top, right, left }
}

/** Expands the daemon's run-length payload: six bytes per run, a big-endian
 *  uint16 palette index then a big-endian uint32 length. */
function decodeRuns(encoded: string, volume: number): Uint16Array | null {
  let binary: string
  try {
    binary = atob(encoded)
  } catch {
    return null
  }
  const raw = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) raw[i] = binary.charCodeAt(i)

  const view = new DataView(raw.buffer)
  const out = new Uint16Array(volume)
  let at = 0
  for (let pos = 0; pos + 6 <= raw.length && at < volume; pos += 6) {
    const index = view.getUint16(pos)
    const length = Math.min(view.getUint32(pos + 2), volume - at)
    out.fill(index, at, at + length)
    at += length
  }
  return out
}

function SchematicCanvas({
  region,
  view,
  rotation,
  cut,
}: {
  region: Region | null
  view: ViewMode
  rotation: number
  cut: number
}) {
  const canvas = useRef<HTMLCanvasElement | null>(null)
  const box = useRef<HTMLDivElement | null>(null)
  const [size, setSize] = useState({ width: 0, height: 0 })

  // The canvas has to be told its pixel size; CSS only stretches whatever it
  // was last given, which on a resize is a blurry copy of the old drawing.
  useEffect(() => {
    const element = box.current
    if (!element) return
    const observer = new ResizeObserver(([record]) => {
      const rect = record.contentRect
      setSize({ width: Math.round(rect.width), height: Math.round(rect.height) })
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  const draw = useCallback(() => {
    const element = canvas.current
    if (!element || !region || size.width === 0 || size.height === 0) return

    const ratio = Math.min(window.devicePixelRatio || 1, 2)
    element.width = Math.round(size.width * ratio)
    element.height = Math.round(size.height * ratio)
    const ctx = element.getContext('2d')
    if (!ctx) return
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
    ctx.clearRect(0, 0, size.width, size.height)

    if (view === 'solid') {
      drawSolid(ctx, region, rotation, cut, size.width, size.height)
    } else {
      drawPlan(ctx, region, rotation, cut, size.width, size.height)
    }
  }, [region, view, rotation, cut, size])

  useEffect(() => {
    draw()
  }, [draw])

  return (
    <div className="schem__canvas" ref={box}>
      <canvas
        ref={canvas}
        style={{ width: '100%', height: '100%' }}
        role="img"
        aria-label={
          region
            ? `${region.width} × ${region.height} × ${region.length} 的建筑预览`
            : '没有可渲染的方块'
        }
      />
    </div>
  )
}

/**
 * How a rotated view maps onto the file's own axes.
 *
 * Blocks are stored in Y→Z→X order, so a world index is (y·L + z)·W + x. Every
 * rotation is still a linear walk over that array — only the step sizes and the
 * starting corner change — which is what keeps the render a flat loop over
 * millions of cells instead of a coordinate transform per block.
 */
function strides(region: Region, rotation: number) {
  const { width: W, length: L } = region
  switch (rotation & 3) {
    case 1:
      return { viewW: L, viewL: W, origin: (L - 1) * W, stepX: -W, stepZ: 1 }
    case 2:
      return { viewW: W, viewL: L, origin: W - 1 + (L - 1) * W, stepX: -1, stepZ: -W }
    case 3:
      return { viewW: L, viewL: W, origin: W - 1, stepX: W, stepZ: -1 }
    default:
      return { viewW: W, viewL: L, origin: 0, stepX: 1, stepZ: W }
  }
}

/**
 * The solid view: a 2:1 isometric projection, drawn back to front.
 *
 * Painter's algorithm in the order y → z → x. That order is correct for this
 * projection because the only cell that lands exactly on top of (x, y, z) is
 * (x+1, y+1, z+1), and the three that partly cover it are one step along x, z
 * or y — all of them later in the loop.
 */
function drawSolid(
  ctx: CanvasRenderingContext2D,
  region: Region,
  rotation: number,
  cut: number,
  width: number,
  height: number,
) {
  const { blocks, kind, top, right, left } = region
  const { viewW, viewL, origin, stepX, stepZ } = strides(region, rotation)
  const layer = region.width * region.length
  const levels = Math.min(cut, region.height - 1) + 1

  // One unit is half a tile wide and a whole tile tall, which is the 2:1 shape
  // Minecraft's own isometric renders use.
  const unit = Math.min(
    (width - 8) / (viewW + viewL),
    (height - 8) / ((viewW + viewL) / 2 + levels),
    26,
  )
  if (!(unit > 0)) return

  const spanX = (viewW + viewL) * unit
  const spanY = ((viewW + viewL) / 2 + levels) * unit
  const originX = (width - spanX) / 2 + viewL * unit
  const originY = (height - spanY) / 2 + (levels - 1) * unit + unit / 2

  const half = unit / 2
  // Below about two pixels a cube is a dot, and three faces of a dot is three
  // times the work for the same pixel.
  const coarse = unit < 2.2

  for (let y = 0; y < levels; y++) {
    const base = origin + y * layer
    for (let vz = 0; vz < viewL; vz++) {
      const rowBase = base + vz * stepZ
      for (let vx = 0; vx < viewW; vx++) {
        const index = rowBase + vx * stepX
        const palette = blocks[index]
        const material = kind[palette]
        if (material === SKIP) continue

        const coveredAbove = y + 1 < levels && kind[blocks[index + layer]] === OPAQUE
        const coveredRight = vx + 1 < viewW && kind[blocks[index + stepX]] === OPAQUE
        const coveredLeft = vz + 1 < viewL && kind[blocks[index + stepZ]] === OPAQUE
        if (coveredAbove && coveredRight && coveredLeft) continue

        const cx = originX + (vx - vz) * unit
        const cy = originY + (vx + vz) * half - y * unit

        if (material === SEE_THROUGH) ctx.globalAlpha = 0.5

        if (coarse) {
          ctx.fillStyle = top[palette]
          ctx.fillRect(cx - unit, cy - half, unit * 2, unit * 2)
        } else {
          if (!coveredAbove) {
            ctx.fillStyle = top[palette]
            ctx.beginPath()
            ctx.moveTo(cx, cy - half)
            ctx.lineTo(cx + unit, cy)
            ctx.lineTo(cx, cy + half)
            ctx.lineTo(cx - unit, cy)
            ctx.closePath()
            ctx.fill()
          }
          if (!coveredLeft) {
            ctx.fillStyle = left[palette]
            ctx.beginPath()
            ctx.moveTo(cx - unit, cy)
            ctx.lineTo(cx, cy + half)
            ctx.lineTo(cx, cy + half + unit)
            ctx.lineTo(cx - unit, cy + unit)
            ctx.closePath()
            ctx.fill()
          }
          if (!coveredRight) {
            ctx.fillStyle = right[palette]
            ctx.beginPath()
            ctx.moveTo(cx + unit, cy)
            ctx.lineTo(cx, cy + half)
            ctx.lineTo(cx, cy + half + unit)
            ctx.lineTo(cx + unit, cy + unit)
            ctx.closePath()
            ctx.fill()
          }
        }

        if (material === SEE_THROUGH) ctx.globalAlpha = 1
      }
    }
  }
}

/** How fast a layer below the cut fades out, and how faint it is allowed to
 *  get. Deep enough to read a floor plan against the storey under it, shallow
 *  enough that the cut layer stays the clearest thing on screen. */
const PLAN_FADE = 0.055
const PLAN_FLOOR = 0.22

/**
 * The top-down view: what you would see looking straight down with everything
 * above the cut taken away. Blocks further below fade, so a floor plan reads
 * against the storey underneath instead of floating on nothing.
 */
function drawPlan(
  ctx: CanvasRenderingContext2D,
  region: Region,
  rotation: number,
  cut: number,
  width: number,
  height: number,
) {
  const { blocks, kind, top } = region
  const { viewW, viewL, origin, stepX, stepZ } = strides(region, rotation)
  const layer = region.width * region.length
  const ceiling = Math.min(cut, region.height - 1)

  const cell = Math.min((width - 8) / viewW, (height - 8) / viewL)
  if (!(cell > 0)) return
  const originX = (width - viewW * cell) / 2
  const originY = (height - viewL * cell) / 2

  // Cell edges are rounded and shared, so neighbours tile exactly. Drawing
  // each cell at its own fractional width instead leaves either seams or
  // double-blended overlaps, and with the depth fade below the overlaps show
  // up as a grid ruled across the plan.
  const edgeX = new Float64Array(viewW + 1)
  for (let i = 0; i <= viewW; i++) edgeX[i] = Math.round(originX + i * cell)
  const edgeZ = new Float64Array(viewL + 1)
  for (let i = 0; i <= viewL; i++) edgeZ[i] = Math.round(originY + i * cell)

  for (let vz = 0; vz < viewL; vz++) {
    const rowBase = origin + vz * stepZ
    for (let vx = 0; vx < viewW; vx++) {
      const column = rowBase + vx * stepX
      for (let y = ceiling; y >= 0; y--) {
        const palette = blocks[column + y * layer]
        if (kind[palette] === SKIP) continue
        // Depth fades towards the canvas rather than towards black: the panel
        // has a light mode, and "further down" drawn as "darker" turns the
        // ground floor of a light-mode plan into a black hole.
        const depth = ceiling - y
        ctx.globalAlpha = depth === 0 ? 1 : Math.max(PLAN_FLOOR, 1 - depth * PLAN_FADE)
        ctx.fillStyle = top[palette]
        ctx.fillRect(edgeX[vx], edgeZ[vz], edgeX[vx + 1] - edgeX[vx], edgeZ[vz + 1] - edgeZ[vz])
        break
      }
      // A column with nothing in it leaves the background showing, which is
      // what makes the footprint of the build readable.
    }
  }
  ctx.globalAlpha = 1
}

/** Multiplies a hex colour's channels. Every colour reaching here is hex —
 *  see blockcolors.ts, which is why there is one code path and not three. */
function shade(hex: string, factor: number): string {
  const value = parseInt(hex.slice(1), 16)
  const channel = (offset: number) =>
    Math.max(0, Math.min(255, Math.round(((value >> offset) & 0xff) * factor)))
  const to2 = (n: number) => n.toString(16).padStart(2, '0')
  return `#${to2(channel(16))}${to2(channel(8))}${to2(channel(0))}`
}

/* ---------------------------------------------------------- data version */

/** Data version → the release that introduced it. Only releases, and only the
 *  ones a schematic in the wild is likely to carry: the point is to turn "3465"
 *  into something an operator can compare against their server. */
const RELEASES: Array<[number, string]> = [
  [1519, '1.13'],
  [1631, '1.13.2'],
  [1952, '1.14'],
  [1976, '1.14.4'],
  [2225, '1.15'],
  [2230, '1.15.2'],
  [2566, '1.16'],
  [2586, '1.16.5'],
  [2724, '1.17'],
  [2730, '1.17.1'],
  [2860, '1.18'],
  [2975, '1.18.2'],
  [3105, '1.19'],
  [3218, '1.19.3'],
  [3337, '1.19.4'],
  [3463, '1.20'],
  [3465, '1.20.1'],
  [3578, '1.20.2'],
  [3700, '1.20.4'],
  [3839, '1.20.6'],
  [3953, '1.21'],
  [4082, '1.21.3'],
  [4189, '1.21.4'],
  [4325, '1.21.5'],
  [4440, '1.21.8'],
]

function minecraftVersion(dataVersion: number): string | null {
  if (!dataVersion) return null
  let nearest: string | null = null
  for (const [version, name] of RELEASES) {
    if (dataVersion === version) return name
    if (dataVersion > version) nearest = name
  }
  if (!nearest) return `${dataVersion}（1.13 之前）`
  // A snapshot or a release this build has never heard of: say what it is at
  // least as new as rather than guessing a name.
  return `${nearest} 之后`
}
