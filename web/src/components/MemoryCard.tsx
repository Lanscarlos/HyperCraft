import { Section } from './Section'

/** One other server's claim on this machine's memory. */
export interface MemoryClaim {
  name: string
  /** The ceiling that will really apply — 0 where nobody knows. */
  mb: number
}

interface Props {
  min: number
  max: number
  onMin: (value: number) => void
  onMax: (value: number) => void
  locked: boolean
  onLocked: (locked: boolean) => void
  /** The host's physical memory in bytes; 0 before the first metrics poll. */
  hostTotalBytes: number
  others: MemoryClaim[]
}

const MB = 1024 * 1024

/**
 * The heap, against what the machine has left.
 *
 * The bar is the only reference anybody actually lacks when filling in a
 * ceiling: "is 8 GB a lot here" has no answer without knowing what the box
 * has and what the other servers already claim. It counts configured ceilings
 * rather than live usage — hence 已分配, not 已用 — because the question being
 * answered is whether these numbers can coexist, not what is resident now.
 */
export function MemoryCard({
  min,
  max,
  onMin,
  onMax,
  locked,
  onLocked,
  hostTotalBytes,
  others,
}: Props) {
  const known = others.filter((one) => one.mb > 0)
  const unknown = others.length - known.length
  const othersMB = known.reduce((sum, one) => sum + one.mb, 0)
  const totalMB = Math.round(hostTotalBytes / MB)
  // Over-committed: the ceilings already add up to more than the box has.
  // Which is not automatically wrong — servers rarely all peak together — but
  // it is the one thing here worth colouring.
  const over = totalMB > 0 && othersMB + max > totalMB

  const setMin = (value: number) => {
    onMin(value)
    if (locked) onMax(value)
  }
  const setMax = (value: number) => {
    onMax(value)
    if (locked) onMin(value)
  }

  return (
    <Section
      form
      title={
        <>
          <span className="originmark originmark--memory" aria-hidden="true" />
          内存
        </>
      }
      note="给这台服务器多少堆内存。数值是启动参数里的 -Xms 和 -Xmx。"
      tools={
        <label className="checkbox checkbox--inline">
          <input
            type="checkbox"
            checked={locked}
            onChange={(e) => {
              onLocked(e.target.checked)
              // Snapping on the way in, not silently on the next edit: a
              // switch that changes a value you are looking at has to change
              // it now, where you can see it happen.
              if (e.target.checked) onMin(max)
            }}
          />
          <span>锁定 Xms = Xmx</span>
        </label>
      }
    >
      <div className="field-row">
        <label className="field field--num">
          <span>最小内存 (MB)</span>
          <input
            type="number"
            min={0}
            step={256}
            value={min}
            onChange={(e) => setMin(Number(e.target.value))}
          />
        </label>
        <label className="field field--num">
          <span>最大内存 (MB)</span>
          <input
            type="number"
            min={0}
            step={256}
            value={max}
            onChange={(e) => setMax(Number(e.target.value))}
          />
        </label>
      </div>

      {/* Nothing at all until the first metrics poll lands: a bar drawn
          against a total of zero would read as "this machine has no memory",
          which is worse than the reader waiting a second for it. */}
      {totalMB > 0 && (
        <div className="memorybar">
          <div className="memorybar__head">
            <span>宿主机内存分配</span>
            <span className="memorybar__total">
              已分配 {fmtGB(othersMB + max)} / {fmtGB(totalMB)} GB
            </span>
          </div>
          <div className="memorybar__track">
            <span
              className="memorybar__fill memorybar__fill--others"
              style={{ width: pct(othersMB, totalMB) }}
            />
            <span
              className={`memorybar__fill memorybar__fill--self${
                over ? ' memorybar__fill--over' : ''
              }`}
              style={{ width: pct(max, totalMB) }}
            />
          </div>
          <div className="memorybar__legend">
            <span className="memorybar__key">
              <span
                className="originmark memorybar__swatch--others"
                aria-hidden="true"
              />
              其他实例 {fmtGB(othersMB)} GB
            </span>
            <span className="memorybar__key">
              <span className="originmark originmark--memory" aria-hidden="true" />
              本实例 {fmtGB(max)} GB
            </span>
            <span className="memorybar__key memorybar__key--end">
              剩余 {fmtGB(Math.max(0, totalMB - othersMB - max))} GB
            </span>
          </div>
          {over && (
            <small className="muted">
              这些上限加起来超过物理内存了。服务器很少同时吃满，但一旦同时吃满，
              内核会挑一个杀掉。
            </small>
          )}
          {unknown > 0 && (
            <small className="muted">
              另有 {unknown} 个实例的上限面板不知道（用参数文件启动的，上限写在它们自己的
              文件里），没有算进来。
            </small>
          )}
        </div>
      )}
    </Section>
  )
}

/** One decimal, because 2.5 GB is a number people set and 2.56 is not. */
function fmtGB(mb: number): string {
  return (mb / 1024).toFixed(1)
}

function pct(mb: number, totalMB: number): string {
  return `${Math.max(0, Math.min(100, (mb / totalMB) * 100))}%`
}
