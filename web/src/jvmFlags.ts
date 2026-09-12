/**
 * The grammar of a JVM argument — what the row editor builds its controls from.
 *
 * Deliberately a grammar and not a catalogue. jvmPresets.ts argues at length
 * why the panel keeps no list of the flags you are allowed to set: the useful
 * ones are a handful out of hundreds, and each carries a Java-version
 * predicate that goes stale between releases. That argument still holds, and
 * it is an argument about *which flags exist*, not about *how a flag is
 * written*. `-XX:+Name` has been a boolean on every JVM that ever shipped and
 * `-XX:Name=200` is a number whether or not anyone here has heard of Name.
 *
 * So the type is read off the syntax, and nothing in this file gates what you
 * may write: anything it cannot classify stays a plain text row, edited and
 * saved verbatim. KNOWN_FLAGS only adds a line of Chinese to the dozen flags
 * worth explaining and offers them as completions — it never decides what is
 * allowed, which is the line jvmPresets.ts asked not to cross.
 */

export type FlagKind = 'boolean' | 'number' | 'text' | 'raw'

export interface Flag {
  /** Stable across edits, so a row keeps its identity (and its focus) while
   *  its text changes. Not derived from the text: two identical lines are two
   *  rows, and a row being retyped is still the same row. */
  id: string
  /**
   * Exactly what this line says right now.
   *
   * The editor writes `raw` back untouched for every row nobody edited, which
   * is what keeps a trip through the list view from quietly reformatting args
   * the operator never looked at. A control that changes a row recomputes it
   * through formatFlag().
   */
  raw: string
  kind: FlagKind
  /** `-XX:` for HotSpot flags, `-D` for system properties, '' for raw. */
  prefix: string
  /** The flag's name with no prefix, sign or value: the part a control must
   *  not touch. Empty for raw. */
  name: string
  /** number: the digits alone. text: everything after the `=`. */
  value: string
  /** number: a size suffix if the flag had one — one of '', k, K, m, M, g, G. */
  unit: string
  /** boolean: whether the line reads `-XX:+Name` rather than `-XX:-Name`. */
  on: boolean
}

let seq = 0
const nextId = () => `flag-${++seq}`

const BOOLEAN = /^-XX:([+-])([A-Za-z0-9_]+)$/
const NUMBER = /^-XX:([A-Za-z0-9_]+)=(-?\d+)([kKmMgG]?)$/
const XX_TEXT = /^-XX:([A-Za-z0-9_]+)=(.*)$/
// A system property needs the `=`: bare -Dfoo is legal, sets the value to the
// empty string, and round-trips as itself only if left alone — so it falls
// through to raw rather than growing an `=` the operator did not type.
const PROPERTY = /^-D([^=\s]+)=(.*)$/

const base = (raw: string): Flag => ({
  id: nextId(),
  raw,
  kind: 'raw',
  prefix: '',
  name: '',
  value: '',
  unit: '',
  on: false,
})

/** Reads one line into the most specific shape it fits, raw being the floor. */
export function parseFlag(line: string): Flag {
  const raw = line.trim()
  const flag = base(raw)

  let m = BOOLEAN.exec(raw)
  if (m) return { ...flag, kind: 'boolean', prefix: '-XX:', name: m[2], on: m[1] === '+' }

  m = NUMBER.exec(raw)
  if (m) return { ...flag, kind: 'number', prefix: '-XX:', name: m[1], value: m[2], unit: m[3] }

  m = XX_TEXT.exec(raw)
  if (m) return { ...flag, kind: 'text', prefix: '-XX:', name: m[1], value: m[2] }

  m = PROPERTY.exec(raw)
  if (m) return { ...flag, kind: 'text', prefix: '-D', name: m[1], value: m[2] }

  return flag
}

/**
 * The inverse, and the invariant this file rests on:
 * formatFlag(parseFlag(x)) === x.trim() for every x.
 *
 * Every branch above reconstructs from anchored captures, so the only way to
 * break that is to add a pattern whose format branch drops something it
 * matched. There is no test suite on this side of the repo to catch it —
 * check a new pattern by hand against a line that uses each capture.
 */
export function formatFlag(flag: Flag): string {
  switch (flag.kind) {
    case 'boolean':
      return `-XX:${flag.on ? '+' : '-'}${flag.name}`
    case 'number':
      return `${flag.prefix}${flag.name}=${flag.value}${flag.unit}`
    case 'text':
      return `${flag.prefix}${flag.name}=${flag.value}`
    default:
      return flag.raw
  }
}

/** Re-reads a row after one of its controls changed it. */
export function reflow(flag: Flag, patch: Partial<Flag>): Flag {
  const next = { ...flag, ...patch }
  return { ...next, raw: formatFlag(next) }
}

/** Splits the textarea's contents into rows. Blank lines are dropped because
 *  saving drops them too (see fromLines in LaunchSettings) — keeping them
 *  would put rows in the list that cannot survive a save. */
export function parseFlags(text: string): Flag[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map(parseFlag)
}

export const formatFlags = (flags: Flag[]): string => flags.map((f) => f.raw).join('\n')

/** What a row is looked up by: prefix and name, without sign or value, so
 *  `-XX:+UseG1GC` and `-XX:-UseG1GC` are the same flag. */
export const flagKey = (flag: Flag): string =>
  flag.kind === 'raw' ? '' : `${flag.prefix}${flag.name}`

export interface KnownFlag {
  /** A complete, typeable line — both the completion candidate and what the
   *  key is derived from, so the two can never disagree. */
  sample: string
  /** One line of what it does, under the row. */
  note: string
  /** Advisory bounds for the number control. The input does not clamp to them:
   *  a JVM flag out of its usual range is a decision, not a typo, and the
   *  panel is not in a position to tell the two apart. */
  min?: number
  max?: number
  /** What the number is counted in, printed after the field. Only where the
   *  unit is not already in the flag's own name — MaxGCPauseMillis says Millis
   *  itself, but 毫秒 next to the box is what someone skimming reads. */
  suffix?: string
}

/**
 * The flags worth a sentence of explanation — roughly what the presets use,
 * plus the handful people reach for afterwards.
 *
 * Short on purpose. This list's job is to explain and to save typing, not to
 * be complete; a flag missing from it is fully usable, just unannotated, and
 * that is the property that lets the list go a few Java releases out of date
 * without anything breaking.
 */
export const KNOWN_FLAGS: KnownFlag[] = [
  // ---- collectors
  { sample: '-XX:+UseG1GC', note: 'G1 收集器。Minecraft 服务端的常规选择，Java 9 起也是默认。' },
  { sample: '-XX:+UseZGC', note: 'ZGC，低停顿。大内存（16 GB 以上）才划算，需要 Java 15+。' },
  { sample: '-XX:+ZGenerational', note: '分代 ZGC。Java 21 / 22 需要显式打开，23 起是默认，24 已移除这个开关。' },
  { sample: '-XX:+UseParallelGC', note: '吞吐优先的老收集器。核心少、内存小的机器上有时反而更稳。' },

  // ---- the switches the presets turn on
  { sample: '-XX:+ParallelRefProcEnabled', note: '并行处理引用对象，缩短 GC 停顿。' },
  { sample: '-XX:+DisableExplicitGC', note: '忽略插件里的 System.gc()，防止它们强行触发一次完整 GC。' },
  { sample: '-XX:+AlwaysPreTouch', note: '启动时就把堆内存全部摸一遍，启动慢几秒，换运行时不再抖动。' },
  { sample: '-XX:+PerfDisableSharedMem', note: '不把 JVM 统计信息写进 /tmp，避免磁盘卡顿拖住 GC。' },
  { sample: '-XX:+UnlockExperimentalVMOptions', note: '解锁实验性参数。后面用到实验性开关时才需要。' },
  { sample: '-XX:+UseStringDeduplication', note: '合并重复字符串，省一点内存。只对 G1 有效。' },

  // ---- the numbers
  { sample: '-XX:MaxGCPauseMillis=200', note: 'GC 停顿的目标毫秒数。调小会更频繁地 GC，不是越小越好。', min: 1, max: 10000, suffix: '毫秒' },
  { sample: '-XX:G1NewSizePercent=30', note: '新生代占堆的最小比例。Aikar 那套按内存给 30 或 40。', min: 0, max: 100, suffix: '%' },
  { sample: '-XX:G1MaxNewSizePercent=40', note: '新生代占堆的最大比例。', min: 0, max: 100, suffix: '%' },
  { sample: '-XX:G1HeapRegionSize=8M', note: 'G1 分区大小。堆大于 12 GB 时 Aikar 用 16M。' },
  { sample: '-XX:G1ReservePercent=20', note: '预留给晋升失败的堆比例，调高更保守。', min: 0, max: 50, suffix: '%' },
  { sample: '-XX:G1HeapWastePercent=5', note: '允许浪费的堆比例，低于它就不再混合回收。', min: 0, max: 100, suffix: '%' },
  { sample: '-XX:G1MixedGCCountTarget=4', note: '一轮混合回收分几次做完。', min: 1, max: 32, suffix: '次' },
  { sample: '-XX:InitiatingHeapOccupancyPercent=15', note: '堆占用到这个比例就开始并发标记。', min: 0, max: 100, suffix: '%' },
  { sample: '-XX:G1MixedGCLiveThresholdPercent=90', note: '存活对象超过这个比例的分区不参与混合回收。', min: 0, max: 100, suffix: '%' },
  { sample: '-XX:G1RSetUpdatingPauseTimePercent=5', note: '停顿里留给 RSet 更新的时间比例。', min: 0, max: 100, suffix: '%' },
  { sample: '-XX:SurvivorRatio=32', note: 'Eden 与 Survivor 区的大小比例。', min: 1, max: 1024 },
  { sample: '-XX:MaxTenuringThreshold=1', note: '对象熬过几次 GC 就晋升到老年代。', min: 0, max: 15, suffix: '次' },
  { sample: '-XX:ParallelGCThreads=8', note: 'GC 停顿期间用几个线程。留空让 JVM 按核心数决定通常更好。', min: 1, max: 64, suffix: '个线程' },
  { sample: '-XX:ConcGCThreads=2', note: '并发标记用几个线程，一般取上面那个的四分之一。', min: 1, max: 32, suffix: '个线程' },

  // ---- system properties
  { sample: '-Dusing.aikars.flags=https://mcflags.emc.gs', note: 'Aikar 那套参数的标记，Paper 用它判断你是不是照着调过。' },
  { sample: '-Daikars.new.flags=true', note: '同上，配套的第二个标记。' },
  { sample: '-Dfile.encoding=UTF-8', note: '强制 JVM 用 UTF-8 读写，中文乱码时先试这个。' },
  { sample: '-Dterminal.jline=false', note: '关掉 JLine 的行编辑。面板的伪终端开关会自动管这个，一般不用手写。' },
  { sample: '-Dterminal.ansi=true', note: '强制输出 ANSI 颜色。同上，面板的「强制彩色」会自动加。' },
]

const INDEX = new Map(KNOWN_FLAGS.map((entry) => [flagKey(parseFlag(entry.sample)), entry]))

/** The annotation for a row, if there is one. Absent is the normal case. */
export const knownFor = (flag: Flag): KnownFlag | undefined => INDEX.get(flagKey(flag))

/** A size suffix only makes sense on flags that name a size; everywhere else
 *  the dropdown would be a third thing to get wrong. A flag that already
 *  carries one keeps it regardless of what it is called. */
export const wantsUnit = (flag: Flag): boolean => flag.unit !== '' || /Size$/.test(flag.name)

/**
 * Completion candidates for the argument being typed.
 *
 * Matches the note as well as the flag, so 停顿 finds MaxGCPauseMillis without
 * anyone having to know it is spelled that way — which is the only reason the
 * notes are worth carrying into the picker at all. Whatever is typed stays
 * submittable no matter what this returns; an empty result is a list with
 * nothing in it, never a rejection.
 */
export function suggestFlags(query: string, limit = 8): KnownFlag[] {
  const raw = query.trim()
  const q = raw.toLowerCase()
  if (q === '') return KNOWN_FLAGS.slice(0, limit)

  const scored: Array<[number, KnownFlag]> = []
  for (const entry of KNOWN_FLAGS) {
    const sample = entry.sample.toLowerCase()
    // A flag you have started spelling outranks one that merely contains the
    // letters, which outranks one whose Chinese note mentions them.
    const tier = sample.startsWith(q) ? 0 : sample.includes(q) ? 1 : entry.note.includes(raw) ? 2 : -1
    if (tier < 0) continue
    // Within a tier, earlier is more on-topic. It matters most for the notes:
    // searching 停顿 should reach MaxGCPauseMillis, whose note opens on the
    // word, before UseZGC, which mentions it in passing — and catalogue order,
    // which is what a plain tie leaves you with, has no opinion about that.
    const where = tier === 2 ? entry.note.indexOf(raw) : sample.indexOf(q)
    scored.push([tier * 1000 + where, entry])
  }
  return scored
    .sort((a, b) => a[0] - b[0])
    .slice(0, limit)
    .map(([, entry]) => entry)
}
