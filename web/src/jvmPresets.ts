/**
 * Ready-made JVM argument sets, for the launch settings form.
 *
 * Deliberately presets and not a builder. A per-flag form was the obvious
 * shape and the wrong one: the flags worth offering are a handful out of
 * hundreds, the useful ones come in sets that only work together, and every
 * control would need a Java-version predicate behind it — ZGC wants 15, its
 * generational mode 21, and -XX:+ZGenerational was removed again in 24. A
 * catalogue like that drifts out of date between two Java releases and is
 * wrong in the direction that keeps a server from starting.
 *
 * So a preset fills the same textarea a human types into, and the textarea
 * stays the source of truth. Nothing here can express something you could not
 * have typed, which is what keeps this from becoming a second, competing way
 * to configure a JVM.
 */

export interface JVMPreset {
  id: string
  label: string
  /** Why you would pick this one, in one line, under the buttons. */
  note: string
  /** The heap ceiling is an input because Aikar's numbers change above 12 GB —
   *  see below. Presets that do not care ignore it. */
  args: (maxMemoryMB: number) => string[]
}

/**
 * Aikar's flags, the G1 tuning most of the Paper world runs.
 *
 * Two sets of numbers, not one: above 12 GB the young generation is given a
 * bigger share and the regions are doubled, which is the author's own split
 * rather than ours. The panel picks the side from the heap ceiling already in
 * the form, because asking someone to know which side of 12 GB they are on is
 * asking them to read the upstream page the preset exists to save them from.
 *
 * These assume -Xms equals -Xmx. The form says so when it does not; it does
 * not quietly rewrite the memory fields, because those are a separate decision
 * that happens to be a precondition here.
 */
const aikar = (maxMemoryMB: number): string[] => {
  const large = maxMemoryMB >= 12 * 1024
  return [
    '-XX:+UseG1GC',
    '-XX:+ParallelRefProcEnabled',
    '-XX:MaxGCPauseMillis=200',
    '-XX:+UnlockExperimentalVMOptions',
    '-XX:+DisableExplicitGC',
    '-XX:+AlwaysPreTouch',
    `-XX:G1NewSizePercent=${large ? 40 : 30}`,
    `-XX:G1MaxNewSizePercent=${large ? 50 : 40}`,
    `-XX:G1HeapRegionSize=${large ? '16M' : '8M'}`,
    `-XX:G1ReservePercent=${large ? 15 : 20}`,
    '-XX:G1HeapWastePercent=5',
    '-XX:G1MixedGCCountTarget=4',
    `-XX:InitiatingHeapOccupancyPercent=${large ? 20 : 15}`,
    '-XX:G1MixedGCLiveThresholdPercent=90',
    '-XX:G1RSetUpdatingPauseTimePercent=5',
    '-XX:SurvivorRatio=32',
    '-XX:+PerfDisableSharedMem',
    '-XX:MaxTenuringThreshold=1',
    '-Dusing.aikars.flags=https://mcflags.emc.gs',
    '-Daikars.new.flags=true',
  ]
}

export const JVM_PRESETS: JVMPreset[] = [
  {
    id: 'aikar',
    label: "Aikar's Flags",
    note: '大多数 Paper / Spigot 服务端的默认选择，会按上面填的最大内存自动切一套数值。要求最小内存和最大内存填一样。',
    args: aikar,
  },
  {
    id: 'g1',
    label: 'G1GC 保守',
    note: '只开 G1 和一个暂停目标，不动任何实验性开关。机器内存紧张、或者 Aikar 那套跑出问题时用这个。',
    args: () => [
      '-XX:+UseG1GC',
      '-XX:MaxGCPauseMillis=200',
      '-XX:+ParallelRefProcEnabled',
      '-XX:+DisableExplicitGC',
    ],
  },
  {
    id: 'zgc',
    label: 'ZGC 低延迟',
    // No -XX:+ZGenerational here on purpose: it is required on 21 and 22,
    // became the default in 23, and was removed in 24 — a JVM that refuses to
    // start is a worse outcome than a slightly less tuned collector, so the
    // note asks rather than the preset guessing.
    note: '大内存（16 GB 以上）且在意卡顿时才划算，需要 Java 15+。Java 21 / 22 想用分代 ZGC 得自己再加一行 -XX:+ZGenerational。',
    args: () => [
      '-XX:+UseZGC',
      '-XX:+AlwaysPreTouch',
      '-XX:+ParallelRefProcEnabled',
      '-XX:+DisableExplicitGC',
      '-XX:+PerfDisableSharedMem',
    ],
  },
]
