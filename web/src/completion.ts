// Tab completion for the console input.
//
// A server console attached to a real terminal gets completion from JLine,
// which asks the running server what it knows. We are on the other side of a
// pipe, so we do the next best thing: a dictionary of the commands vanilla and
// Paper ship with, the players the console has told us are online, and the
// commands this browser has already sent.

/** Candidates for one positional argument. 'players' resolves at use time. */
type ArgSpec = 'players' | string[]

interface CommandSpec {
  name: string
  desc: string
  /** args[0] completes the first argument after the command name. */
  args?: ArgSpec[]
}

const GAMEMODES = ['survival', 'creative', 'adventure', 'spectator']

/** The gamerules worth completing; the list is stable across versions. */
const GAMERULES = [
  'announceAdvancements',
  'commandBlockOutput',
  'disableElytraMovementCheck',
  'doDaylightCycle',
  'doEntityDrops',
  'doFireTick',
  'doImmediateRespawn',
  'doInsomnia',
  'doLimitedCrafting',
  'doMobLoot',
  'doMobSpawning',
  'doPatrolSpawning',
  'doTileDrops',
  'doTraderSpawning',
  'doVinesSpread',
  'doWardenSpawning',
  'doWeatherCycle',
  'drowningDamage',
  'fallDamage',
  'fireDamage',
  'forgiveDeadPlayers',
  'freezeDamage',
  'keepInventory',
  'logAdminCommands',
  'maxCommandChainLength',
  'maxEntityCramming',
  'mobGriefing',
  'naturalRegeneration',
  'playersSleepingPercentage',
  'randomTickSpeed',
  'reducedDebugInfo',
  'sendCommandFeedback',
  'showDeathMessages',
  'spawnRadius',
  'spectatorsGenerateChunks',
  'universalAnger',
]

// Ordered the way a console operator thinks: the everyday commands first, the
// long tail after. Ties are broken alphabetically at match time anyway.
const COMMANDS: CommandSpec[] = [
  { name: 'help', desc: '列出服务器支持的命令' },
  { name: 'list', desc: '查看在线玩家' },
  { name: 'say', desc: '以服务器身份广播消息' },
  { name: 'tell', desc: '私聊某个玩家', args: ['players'] },
  { name: 'msg', desc: '私聊某个玩家', args: ['players'] },
  { name: 'w', desc: '私聊某个玩家', args: ['players'] },
  { name: 'me', desc: '以第三人称广播动作' },
  { name: 'tellraw', desc: '发送 JSON 格式消息', args: ['players'] },
  { name: 'kick', desc: '踢出玩家', args: ['players'] },
  { name: 'ban', desc: '封禁玩家', args: ['players'] },
  { name: 'ban-ip', desc: '封禁 IP', args: ['players'] },
  { name: 'banlist', desc: '查看封禁列表', args: [['players', 'ips']] },
  { name: 'pardon', desc: '解封玩家' },
  { name: 'pardon-ip', desc: '解封 IP' },
  { name: 'op', desc: '给予管理员权限', args: ['players'] },
  { name: 'deop', desc: '收回管理员权限', args: ['players'] },
  {
    name: 'whitelist',
    desc: '白名单管理',
    args: [['add', 'remove', 'list', 'on', 'off', 'reload'], 'players'],
  },
  { name: 'gamemode', desc: '切换游戏模式', args: [GAMEMODES, 'players'] },
  { name: 'defaultgamemode', desc: '设置默认游戏模式', args: [GAMEMODES] },
  { name: 'difficulty', desc: '设置难度', args: [['peaceful', 'easy', 'normal', 'hard']] },
  { name: 'gamerule', desc: '查看或修改游戏规则', args: [GAMERULES] },
  {
    name: 'time',
    desc: '查询或设置时间',
    args: [
      ['set', 'add', 'query'],
      ['day', 'night', 'noon', 'midnight', 'daytime', 'gametime'],
    ],
  },
  { name: 'weather', desc: '设置天气', args: [['clear', 'rain', 'thunder']] },
  { name: 'kill', desc: '清除实体或玩家', args: ['players'] },
  { name: 'tp', desc: '传送', args: ['players', 'players'] },
  { name: 'teleport', desc: '传送', args: ['players', 'players'] },
  { name: 'spectate', desc: '旁观某个玩家', args: ['players', 'players'] },
  { name: 'give', desc: '给予物品', args: ['players'] },
  { name: 'clear', desc: '清空背包', args: ['players'] },
  { name: 'effect', desc: '状态效果', args: [['give', 'clear'], 'players'] },
  { name: 'enchant', desc: '附魔手持物品', args: ['players'] },
  { name: 'experience', desc: '经验值', args: [['add', 'set', 'query'], 'players'] },
  { name: 'xp', desc: '经验值', args: [['add', 'set', 'query'], 'players'] },
  { name: 'advancement', desc: '进度管理', args: [['grant', 'revoke'], 'players'] },
  { name: 'attribute', desc: '实体属性', args: ['players'] },
  { name: 'spawnpoint', desc: '设置玩家重生点', args: ['players'] },
  { name: 'setworldspawn', desc: '设置世界出生点' },
  { name: 'setidletimeout', desc: '设置挂机踢出时间（分钟）' },
  { name: 'save-all', desc: '保存世界', args: [['flush']] },
  { name: 'save-on', desc: '恢复自动保存' },
  { name: 'save-off', desc: '暂停自动保存' },
  { name: 'stop', desc: '关闭服务器' },
  { name: 'reload', desc: '重载数据包 / 插件配置', args: [['confirm']] },
  { name: 'seed', desc: '查看世界种子' },
  { name: 'datapack', desc: '数据包管理', args: [['list', 'enable', 'disable']] },
  { name: 'debug', desc: '性能分析', args: [['start', 'stop', 'function']] },
  { name: 'forceload', desc: '强制加载区块', args: [['add', 'remove', 'query']] },
  { name: 'function', desc: '运行数据包函数' },
  { name: 'locate', desc: '寻找结构 / 生物群系', args: [['structure', 'biome', 'poi']] },
  { name: 'particle', desc: '生成粒子效果' },
  { name: 'playsound', desc: '播放声音' },
  { name: 'stopsound', desc: '停止声音', args: ['players'] },
  { name: 'recipe', desc: '配方解锁', args: [['give', 'take'], 'players'] },
  { name: 'scoreboard', desc: '计分板', args: [['objectives', 'players']] },
  { name: 'team', desc: '队伍管理', args: [['add', 'empty', 'join', 'leave', 'list', 'modify', 'remove']] },
  { name: 'tag', desc: '实体标签', args: ['players', ['add', 'remove', 'list']] },
  { name: 'title', desc: '屏幕标题', args: ['players', ['title', 'subtitle', 'actionbar', 'times', 'clear', 'reset']] },
  { name: 'bossbar', desc: 'Boss 血条', args: [['add', 'get', 'list', 'remove', 'set']] },
  { name: 'summon', desc: '生成实体' },
  { name: 'setblock', desc: '放置方块' },
  { name: 'fill', desc: '填充区域' },
  { name: 'clone', desc: '复制区域' },
  { name: 'execute', desc: '条件执行命令', args: [['as', 'at', 'if', 'unless', 'run', 'positioned', 'store']] },
  { name: 'worldborder', desc: '世界边界', args: [['add', 'center', 'damage', 'get', 'set', 'warning']] },
  { name: 'spreadplayers', desc: '随机分散玩家' },
  { name: 'trigger', desc: '触发计分板目标' },
  { name: 'publish', desc: '开放局域网' },
  { name: 'transfer', desc: '将玩家转移到其他服务器' },
  { name: 'tick', desc: '刻速率控制', args: [['query', 'rate', 'step', 'sprint', 'freeze', 'unfreeze']] },
  // Paper / Spigot / Bukkit additions.
  { name: 'version', desc: '查看服务端版本（Paper/Spigot）' },
  { name: 'plugins', desc: '查看已加载插件（Paper/Spigot）' },
  { name: 'timings', desc: '性能计时（Spigot）', args: [['on', 'off', 'paste', 'report']] },
  { name: 'mspt', desc: '查看每刻耗时（Paper）' },
  { name: 'tps', desc: '查看 TPS（Paper/Spigot）' },
  { name: 'spark', desc: 'Spark 性能分析', args: [['profiler', 'health', 'tps', 'gc']] },
  { name: 'restart', desc: '重启服务器（Spigot）' },
]

const COMMANDS_BY_NAME = new Map(COMMANDS.map((cmd) => [cmd.name, cmd]))

/** One thing Tab can insert. */
export interface Candidate {
  /** Replacement for the token being completed. */
  value: string
  /** What the whole input line becomes if this is accepted. */
  line: string
  /** Shown next to the highlighted candidate. */
  desc?: string
}

export interface CompletionContext {
  /** Players the console has seen join, most recent first. */
  players: string[]
  /** Commands already sent from this browser, newest first. */
  history: string[]
}

/**
 * Completes the token the caret sits at the end of.
 *
 * The console only ever completes at the end of the line — the same thing a
 * terminal does, and it keeps the caret arithmetic out of the UI.
 */
export function complete(input: string, ctx: CompletionContext): Candidate[] {
  // A leading slash is optional in a server console. Keep whichever the
  // operator typed so accepting a candidate does not surprise them.
  const slash = input.startsWith('/') ? '/' : ''
  const body = slash ? input.slice(1) : input

  const parts = body.split(/\s+/)
  // A trailing space means we are starting a fresh token, not extending one.
  const editing = /\s$/.test(body) ? '' : (parts.pop() ?? '')
  const before = parts.filter(Boolean)
  const prefix = slash + (before.length > 0 ? before.join(' ') + ' ' : '')
  const build = (value: string, desc?: string): Candidate => ({
    value,
    line: prefix + value,
    desc,
  })

  if (before.length === 0) {
    const commands = COMMANDS.filter((cmd) => startsWith(cmd.name, editing))
      .map((cmd) => build(cmd.name, cmd.desc))
    if (commands.length > 0) return dedupe(commands)

    // Nothing in the dictionary matched — most likely a plugin command, so
    // fall back to what this console has already sent.
    return dedupe(
      ctx.history
        .filter((line) => startsWith(line, input) && line !== input)
        .map((line) => ({ value: line, line, desc: '历史命令' })),
    )
  }

  const spec = COMMANDS_BY_NAME.get(before[0].toLowerCase())
  const argSpec = spec?.args?.[before.length - 1]
  const options =
    argSpec === 'players'
      ? ctx.players
      : Array.isArray(argSpec) && argSpec.length > 0
        ? argSpec
        : // No dictionary entry for this position: player names are the best
          // guess, since most console commands take one.
          ctx.players

  return dedupe(
    options.filter((option) => startsWith(option, editing)).map((option) => build(option)),
  )
}

function startsWith(value: string, prefix: string): boolean {
  return value.toLowerCase().startsWith(prefix.toLowerCase())
}

function dedupe(candidates: Candidate[]): Candidate[] {
  const seen = new Set<string>()
  return candidates.filter((candidate) => {
    if (seen.has(candidate.line)) return false
    seen.add(candidate.line)
    return true
  })
}

/**
 * The longest line every candidate agrees on, so one Tab can fill in as much
 * as is unambiguous before the list appears.
 */
export function commonPrefix(candidates: Candidate[]): string {
  if (candidates.length === 0) return ''
  let prefix = candidates[0].line
  for (const candidate of candidates.slice(1)) {
    while (prefix && !candidate.line.toLowerCase().startsWith(prefix.toLowerCase())) {
      prefix = prefix.slice(0, -1)
    }
  }
  return prefix
}

// ------------------------------------------------------------- online players

const JOINED = /(?:^|\]:? )([A-Za-z0-9_.]{1,16})(?: \(.*\))? joined the game/
const LEFT = /(?:^|\]:? )([A-Za-z0-9_.]{1,16}) left the game/
// The reply to "list", which lets a console that connected late learn every
// name at once instead of waiting for the next join.
const ONLINE_LIST = /players? online:\s*(.+)$/i

/** Colour codes sit right where these patterns expect names, so they go first. */
const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]/g

/**
 * Folds one console line into the set of players we believe are online.
 * Returns the new list, newest first, or the old one when nothing changed.
 */
export function trackPlayers(players: string[], raw: string): string[] {
  const text = raw.includes('\x1b') ? raw.replace(ANSI, '') : raw

  const listed = ONLINE_LIST.exec(text)
  if (listed) {
    const names = listed[1]
      .split(/,\s*/)
      .map((name) => name.trim())
      .filter((name) => /^[A-Za-z0-9_.]{1,16}$/.test(name))
    return names.length > 0 ? names : players
  }

  const joined = JOINED.exec(text)
  if (joined) {
    const name = joined[1]
    return players.includes(name) ? players : [name, ...players]
  }

  const left = LEFT.exec(text)
  if (left && players.includes(left[1])) {
    return players.filter((name) => name !== left[1])
  }

  return players
}
