/**
 * The arguments that go after the jar — the ones the server itself reads,
 * not the JVM.
 *
 * A different shape from jvmFlags.ts on purpose, because the syntax is
 * different: `--key value`, space-separated, with no unit and no -XX:+/-
 * boolean form. Trying to describe both with one model would give each of
 * them fields the other needs and it does not.
 *
 * Deliberately short. A vocabulary that guesses is worse than one that admits
 * it does not know: an argument with no entry here still round-trips exactly
 * as typed, it just gets no note and no typed control.
 */

export interface ServerFlag {
  /** What this does, in one line. */
  note: string
  /** Whether the argument takes a value after it. */
  takesValue: boolean
  /** Shown in the value field when there is nothing in it yet. */
  placeholder?: string
  /** Which servers understand it. Empty means "most of them"; a proxy is
   *  told separately, because Velocity exits on anything it does not know. */
  only?: string
}

export const SERVER_FLAGS: Record<string, ServerFlag> = {
  '--nogui': {
    note: '关掉服务端自带的那个 Swing 窗口。无头机器上基本都要。',
    takesValue: false,
  },
  '--world-dir': {
    note: '存档放在哪，默认是实例目录本身。',
    takesValue: true,
    placeholder: 'worlds/',
  },
  '--universe': {
    note: '同 --world-dir，Bukkit 系用的名字。',
    takesValue: true,
    placeholder: 'worlds/',
  },
  '--world': {
    note: '主世界的名字，默认读 server.properties 的 level-name。',
    takesValue: true,
    placeholder: 'world',
  },
  '--port': {
    note: '覆盖 server.properties 里的端口。一般不用——改配置文件更清楚，面板的端口检查也只看那里。',
    takesValue: true,
    placeholder: '25565',
  },
  '--forceUpgrade': {
    // Named here rather than offered as a preset chip for the reason the old
    // 常用 row gave: it is a one-shot conversion, and a control that remembers
    // it is a control that runs it again on every restart.
    note: '启动时把所有区块升级到当前版本。很慢且不可逆——先备份，跑完记得删掉这一行。',
    takesValue: false,
  },
  '--eraseCache': {
    note: '清掉区块的光照和高度缓存，跨大版本升级后偶尔需要。同样是一次性的。',
    takesValue: false,
  },
  '--safeMode': {
    note: '只加载原版功能，不加载插件。用来判断一个问题是不是插件引起的。',
    takesValue: false,
  },
}

/** What an argument line is, once read. */
export interface ServerArg {
  /** Stable across edits so React and the focus handoff can follow one card. */
  id: string
  /** The line exactly as it was written. Untouched cards are saved from this
   *  rather than rebuilt, which is what keeps a one-argument change a
   *  one-line diff in 配置历史. */
  raw: string
  /** The `--flag` part, empty for something unparseable. */
  name: string
  /** What followed it on the same line. */
  value: string
}

let counter = 0
const nextId = () => `sa${(counter += 1)}`

export function parseServerArg(raw: string): ServerArg {
  const text = raw.trim()
  const cut = text.indexOf(' ')
  if (!text.startsWith('--') || cut < 0) {
    return { id: nextId(), raw: text, name: text.startsWith('--') ? text : '', value: '' }
  }
  return {
    id: nextId(),
    raw: text,
    name: text.slice(0, cut),
    value: text.slice(cut + 1).trim(),
  }
}

export function parseServerArgs(text: string): ServerArg[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map(parseServerArg)
}

export function formatServerArgs(args: ServerArg[]): string {
  return args
    .map((arg) => arg.raw)
    .filter(Boolean)
    .join('\n')
}

/** Rebuilds one argument's line from its parts, leaving every other line
 *  exactly as it was. */
export function reflowServerArg(arg: ServerArg, changes: Partial<ServerArg>): ServerArg {
  const next = { ...arg, ...changes }
  const value = next.value.trim()
  return { ...next, raw: value ? `${next.name} ${value}` : next.name }
}

export function knownServerFlag(arg: ServerArg): ServerFlag | null {
  return SERVER_FLAGS[arg.name] ?? null
}

/** Completions for a half-typed argument. Proxies get none: Velocity exits on
 *  an argument it does not recognise, so a list of Minecraft server flags
 *  there would be a list of ways to stop it booting. */
export function suggestServerFlags(
  draft: string,
  proxy: boolean,
): { sample: string; note: string }[] {
  if (proxy) return []
  const needle = draft.trim().toLowerCase()
  return Object.entries(SERVER_FLAGS)
    .filter(([name]) => name.toLowerCase().includes(needle.replace(/^-+/, '')))
    .slice(0, 6)
    .map(([name, flag]) => ({
      sample: flag.takesValue ? `${name} ${flag.placeholder ?? ''}`.trim() : name,
      note: flag.note,
    }))
}
