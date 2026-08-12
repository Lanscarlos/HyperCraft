/**
 * One colour per block, for drawing a schematic without shipping Minecraft's
 * textures to the browser.
 *
 * The panel has no block textures and is not going to grow a resource-pack
 * loader — that would be tens of megabytes of assets, a version matrix, and a
 * licence question, to make a 400px preview slightly prettier. A flat colour
 * per block is enough for the job the preview actually does: recognising which
 * of thirty .schem files is the castle, and seeing that the roof is spruce and
 * not dark oak.
 *
 * The values approximate Minecraft's own map colours. They are here rather
 * than in Go because they are a rendering decision: the daemon reports what is
 * in the file, the browser decides what it looks like.
 *
 * Three layers, in order: an exact table, then the families (a dyed block, a
 * shape cut from a material, a wood type), then a hash. The hash is what keeps
 * a modded build legible — every unknown block still gets a stable colour of
 * its own instead of all of them sharing one placeholder grey.
 */

/** Blocks that occupy a cell without filling it. */
const AIR = new Set(['air', 'cave_air', 'void_air', 'structure_void', 'light', 'barrier'])

/** Blocks you can see through, so the render must not cull what is behind
 *  them and must draw them with some transparency. */
const TRANSLUCENT = /(glass|water|ice$|_ice$|slime_block|honey_block|nether_portal|barrier)/

const DYES: Record<string, string> = {
  white: '#e9ecec',
  orange: '#f07613',
  magenta: '#bd44b3',
  light_blue: '#3aafd9',
  yellow: '#f8c627',
  lime: '#70b919',
  pink: '#ed8dac',
  gray: '#3e4447',
  light_gray: '#8e8e86',
  cyan: '#158991',
  purple: '#8932b8',
  blue: '#35399d',
  brown: '#724728',
  green: '#546d1b',
  red: '#a12722',
  black: '#141519',
}

/** Terracotta is the one dyed family whose colours are not the dye — it is the
 *  dye fired into clay, which is why it is the palette every desert build is
 *  made of. Sharing the wool table here would turn a whole style of build the
 *  wrong colour. */
const TERRACOTTA: Record<string, string> = {
  white: '#d1b1a1',
  orange: '#a05325',
  magenta: '#95576c',
  light_blue: '#706c8a',
  yellow: '#ba8523',
  lime: '#677534',
  pink: '#a04d4e',
  gray: '#392a23',
  light_gray: '#876a61',
  cyan: '#565b5b',
  purple: '#764656',
  blue: '#4a3b5b',
  brown: '#4d3223',
  green: '#4c532a',
  red: '#8e3c2e',
  black: '#251610',
}

/** Wood tones, used for planks, logs, stairs, doors and everything else cut
 *  from a tree, and as the base the leaf colours below sit against. */
const WOODS: Record<string, string> = {
  oak: '#b4905a',
  spruce: '#7a5a34',
  birch: '#d7c185',
  jungle: '#a1714b',
  acacia: '#ba6337',
  dark_oak: '#4a3319',
  mangrove: '#77333a',
  cherry: '#e3b0ba',
  pale_oak: '#e3ddd2',
  bamboo: '#c9b756',
  crimson: '#7a3a55',
  warped: '#3a8e87',
}

const LEAVES: Record<string, string> = {
  oak: '#4e8a34',
  spruce: '#3d6b45',
  birch: '#6d9b48',
  jungle: '#4a8c2e',
  acacia: '#5f8f2e',
  dark_oak: '#3f7a2c',
  mangrove: '#4f8e3e',
  cherry: '#f0b6d0',
  pale_oak: '#6f9a55',
  azalea: '#4d8a3b',
  flowering_azalea: '#6f8f4e',
}

const COLORS: Record<string, string> = {
  // stone and its family
  stone: '#7d7d7d',
  smooth_stone: '#a0a0a0',
  cobblestone: '#7a7a7a',
  mossy_cobblestone: '#6d7a63',
  stone_bricks: '#767676',
  mossy_stone_bricks: '#6f7a63',
  cracked_stone_bricks: '#6e6e6e',
  chiseled_stone_bricks: '#727272',
  granite: '#9a6a5a',
  polished_granite: '#a5735f',
  diorite: '#cdcdc7',
  polished_diorite: '#d4d4cf',
  andesite: '#8a8a8a',
  polished_andesite: '#949494',
  deepslate: '#4d4d51',
  cobbled_deepslate: '#4a4a4e',
  polished_deepslate: '#484850',
  deepslate_bricks: '#464650',
  deepslate_tiles: '#3b3b43',
  tuff: '#6b6b62',
  calcite: '#dfded7',
  dripstone_block: '#8a6b5b',
  bedrock: '#565656',
  obsidian: '#150d1f',
  crying_obsidian: '#331f5c',
  amethyst_block: '#8f66c0',
  budding_amethyst: '#8259b5',

  // ground
  dirt: '#866043',
  coarse_dirt: '#7a5232',
  rooted_dirt: '#906d51',
  grass_block: '#7cbd6b',
  podzol: '#6b4423',
  mycelium: '#7a6e77',
  dirt_path: '#9c8250',
  farmland: '#6b482e',
  mud: '#3c3a3d',
  mud_bricks: '#8a6a4f',
  packed_mud: '#8d6c51',
  clay: '#a0a6b1',
  gravel: '#9a9494',
  sand: '#dbd3a0',
  red_sand: '#bf6a34',
  sandstone: '#e0d6a4',
  smooth_sandstone: '#e2d9a9',
  chiseled_sandstone: '#dbd19b',
  cut_sandstone: '#ded4a0',
  red_sandstone: '#b5651f',
  smooth_red_sandstone: '#ba6c25',
  soul_sand: '#52403a',
  soul_soil: '#4c3a33',
  moss_block: '#58762d',

  // liquids and weather
  water: '#3f5fd6',
  flowing_water: '#3f5fd6',
  lava: '#e06a12',
  flowing_lava: '#e06a12',
  ice: '#9cc4f0',
  packed_ice: '#8bb8ee',
  blue_ice: '#74a8f0',
  frosted_ice: '#a4cbf2',
  snow: '#f7fbfb',
  snow_block: '#f2f7f7',
  powder_snow: '#f4f9fb',

  // the nether and the end
  netherrack: '#6f2b2b',
  nether_bricks: '#2d1620',
  red_nether_bricks: '#460709',
  nether_wart_block: '#7a0a0a',
  warped_wart_block: '#167b6b',
  shroomlight: '#f0a03c',
  basalt: '#4d4b52',
  smooth_basalt: '#42414a',
  polished_basalt: '#575660',
  blackstone: '#2b2426',
  polished_blackstone: '#33292f',
  polished_blackstone_bricks: '#302730',
  gilded_blackstone: '#41302b',
  magma_block: '#8e3d18',
  glowstone: '#c3a15a',
  end_stone: '#dfe1a7',
  end_stone_bricks: '#d8dba0',
  purpur_block: '#a679a6',
  purpur_pillar: '#ab7fab',
  chorus_plant: '#5c3c5c',
  chorus_flower: '#a186a1',
  sculk: '#0f2c33',
  sculk_catalyst: '#1a3a42',

  // built materials
  bricks: '#96604c',
  quartz_block: '#ece5dd',
  smooth_quartz: '#e8e0d8',
  chiseled_quartz_block: '#e9e2da',
  quartz_pillar: '#eae3db',
  quartz_bricks: '#e7e0d7',
  prismarine: '#63ab9b',
  prismarine_bricks: '#5f9e91',
  dark_prismarine: '#33705d',
  sea_lantern: '#d3ddd6',
  glass: '#c6e6ef',
  tinted_glass: '#41393f',
  bookshelf: '#9a7f4f',
  crafting_table: '#7a563a',
  furnace: '#6b6b6b',
  blast_furnace: '#5e5e5e',
  smoker: '#6a5a4a',
  chest: '#8a6a35',
  trapped_chest: '#8a6a35',
  barrel: '#7c5c33',
  tnt: '#8f3a2a',
  sponge: '#c3c34d',
  wet_sponge: '#a5b04a',
  hay_block: '#b8930f',
  honeycomb_block: '#e5952b',
  honey_block: '#f9ab24',
  slime_block: '#6dbd5a',
  scaffolding: '#c9a34c',
  ladder: '#8a6a3c',
  cobweb: '#dfe4e4',
  torch: '#f5c542',
  lantern: '#e2a33c',
  soul_lantern: '#4fd4d4',
  campfire: '#8a5a2a',
  bell: '#f0c53a',

  // metal and mineral blocks
  iron_block: '#d8d8d8',
  gold_block: '#f6d84f',
  diamond_block: '#4fe0d3',
  emerald_block: '#2fd45b',
  lapis_block: '#1d47a8',
  redstone_block: '#a91b0e',
  coal_block: '#191919',
  netherite_block: '#443b3d',
  copper_block: '#c06d4f',
  exposed_copper: '#a97f68',
  weathered_copper: '#6f9270',
  oxidized_copper: '#4f9b83',
  raw_iron_block: '#a9866c',
  raw_gold_block: '#e0aa2c',
  raw_copper_block: '#9a6247',

  // ores keep their stone and take a tint of what is in them
  coal_ore: '#62615f',
  iron_ore: '#a0866f',
  gold_ore: '#9a8253',
  diamond_ore: '#6e9b98',
  emerald_ore: '#5f9469',
  lapis_ore: '#5c6b8d',
  redstone_ore: '#8a5a55',
  copper_ore: '#9a7f68',
  nether_quartz_ore: '#79413d',
  nether_gold_ore: '#96593a',
  ancient_debris: '#5c4239',

  // plants
  grass: '#77ab4e',
  short_grass: '#77ab4e',
  tall_grass: '#74a84c',
  fern: '#5f8f42',
  large_fern: '#5f8f42',
  vine: '#3f6f28',
  cactus: '#5b8f37',
  sugar_cane: '#8fbf5c',
  lily_pad: '#2b6b2b',
  bamboo: '#7d9b3b',
  kelp: '#3c7a3c',
  seagrass: '#3f8a45',
  dandelion: '#e8e33c',
  poppy: '#c1352b',
  pumpkin: '#c07615',
  carved_pumpkin: '#c07615',
  jack_o_lantern: '#d98a1c',
  melon: '#7a9b2c',
  brown_mushroom_block: '#8a6a4c',
  red_mushroom_block: '#a3312b',
  mushroom_stem: '#cfc7bb',

  // odds and ends that show up in builds
  terracotta: '#985f42',
  glowstone_dust: '#c3a15a',
  redstone_wire: '#a91b0e',
  rail: '#8a8071',
  iron_bars: '#84868a',
  anvil: '#494949',
  beacon: '#7ad6d0',
  spawner: '#28394b',
  ochre_froglight: '#e3dfa8',
  verdant_froglight: '#cfe3b5',
  pearlescent_froglight: '#e9d9e3',
  unknown: '#8a7f7c',
}

/** Dyed families and the table each takes its colour from. Longest suffix
 *  first: "_stained_glass_pane" has to win over "_stained_glass". */
const DYED: Array<[string, Record<string, string>, number]> = [
  ['_stained_glass_pane', DYES, 1],
  ['_stained_glass', DYES, 1],
  ['_glazed_terracotta', DYES, 0.9],
  ['_terracotta', TERRACOTTA, 1],
  ['_concrete_powder', DYES, 1.1],
  ['_concrete', DYES, 0.92],
  ['_shulker_box', DYES, 0.85],
  ['_wool', DYES, 1],
  ['_carpet', DYES, 1],
  ['_bed', DYES, 1],
  ['_wall_banner', DYES, 1],
  ['_banner', DYES, 1],
  ['_candle', DYES, 1],
]

/** Shapes cut from a material: the colour is the material's, so strip the
 *  shape and look the base up again. "_fence_gate" before "_fence". */
const SHAPES = [
  '_stairs',
  '_slab',
  '_wall_sign',
  '_hanging_sign',
  '_sign',
  '_wall_torch',
  '_wall_fan',
  '_fan',
  '_fence_gate',
  '_fence',
  '_pressure_plate',
  '_trapdoor',
  '_door',
  '_button',
  '_pane',
  '_bars',
  '_wall',
  '_block',
]

/** Strips the namespace and the block-state properties: the preview colours
 *  "minecraft:oak_stairs[facing=east,half=bottom]" the same as an oak stair
 *  facing anywhere else. */
export function bareName(state: string): string {
  let name = state
  const bracket = name.indexOf('[')
  if (bracket >= 0) name = name.slice(0, bracket)
  const colon = name.indexOf(':')
  if (colon >= 0) name = name.slice(colon + 1)
  return name
}

export function isAirBlock(state: string): boolean {
  return AIR.has(bareName(state))
}

export function isTranslucentBlock(state: string): boolean {
  return TRANSLUCENT.test(bareName(state))
}

const cache = new Map<string, string>()

/** The colour to draw one block state in. Always returns something. */
export function blockColor(state: string): string {
  const name = bareName(state)
  const hit = cache.get(name)
  if (hit) return hit
  const color = resolve(name, 0) ?? hashColor(name)
  cache.set(name, color)
  return color
}

function resolve(name: string, depth: number): string | undefined {
  if (depth > 3) return undefined

  const direct = COLORS[name]
  if (direct) return direct

  for (const [suffix, table, tint] of DYED) {
    if (name.endsWith(suffix)) {
      const dye = table[name.slice(0, -suffix.length)]
      if (dye) return tint === 1 ? dye : shift(dye, tint)
    }
  }

  // Wood first, because "oak_stairs" would otherwise be stripped to "oak" and
  // then found nowhere.
  const wood = woodColor(name)
  if (wood) return wood

  for (const suffix of SHAPES) {
    if (name.endsWith(suffix) && name.length > suffix.length) {
      const base = name.slice(0, -suffix.length)
      const found = resolve(base, depth + 1) ?? resolve(base + 's', depth + 1)
      if (found) return found
    }
  }

  // Plurals cut both ways: the table says "stone_bricks" and a stair strips
  // down to "stone_brick".
  if (!name.endsWith('s') && COLORS[`${name}s`]) return COLORS[`${name}s`]
  if (name.endsWith('s') && COLORS[name.slice(0, -1)]) return COLORS[name.slice(0, -1)]

  if (name.endsWith('_ore')) return '#7d7d7d'
  if (name.includes('water')) return COLORS.water
  if (name.includes('lava')) return COLORS.lava
  return undefined
}

/** Everything cut from one kind of tree, plus the leaves that grow on it. */
function woodColor(name: string): string | undefined {
  for (const [wood, color] of Object.entries(LEAVES)) {
    if (name === `${wood}_leaves` || name === `${wood}_sapling`) return color
  }
  for (const [wood, color] of Object.entries(WOODS)) {
    if (!name.startsWith(`${wood}_`) && name !== wood) continue
    const rest = name.slice(wood.length + 1)
    switch (rest) {
      case 'log':
      case 'wood':
      case 'stem':
      case 'hyphae':
        return shift(color, 0.82)
      case 'planks':
      case '':
        return color
      default:
        return color
    }
  }
  return undefined
}

/** Multiplies a hex colour's channels, for the "same block, slightly off"
 *  cases — stripped logs, powdered concrete, a shulker box. */
function shift(hex: string, factor: number): string {
  const value = parseInt(hex.slice(1), 16)
  const channel = (offset: number) =>
    Math.max(0, Math.min(255, Math.round(((value >> offset) & 0xff) * factor)))
  const to2 = (n: number) => n.toString(16).padStart(2, '0')
  return `#${to2(channel(16))}${to2(channel(8))}${to2(channel(0))}`
}

/** A stable colour for a block nothing above knows — a modded block, or a
 *  legacy id with no modern name. Distinct beats accurate here: the point is
 *  that two different unknown blocks do not look like one.
 *
 *  Returned as hex like everything else, not as hsl(): the renderer shades
 *  every colour it draws, and one string format is one shading path. */
function hashColor(name: string): string {
  let hash = 0x811c9dc5
  for (let i = 0; i < name.length; i++) {
    hash ^= name.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  // Kept away from full saturation so an unknown block cannot out-shout the
  // real ones next to it.
  return hslToHex(hash % 360, 26 + ((hash >>> 9) % 18), 42 + ((hash >>> 17) % 20))
}

function hslToHex(hue: number, saturation: number, lightness: number): string {
  const s = saturation / 100
  const l = lightness / 100
  const chroma = (1 - Math.abs(2 * l - 1)) * s
  const secondary = chroma * (1 - Math.abs(((hue / 60) % 2) - 1))
  const base = l - chroma / 2

  const sector = Math.floor(hue / 60) % 6
  const rgb = [
    [chroma, secondary, 0],
    [secondary, chroma, 0],
    [0, chroma, secondary],
    [0, secondary, chroma],
    [secondary, 0, chroma],
    [chroma, 0, secondary],
  ][sector]

  const to2 = (n: number) =>
    Math.round((n + base) * 255)
      .toString(16)
      .padStart(2, '0')
  return `#${to2(rgb[0])}${to2(rgb[1])}${to2(rgb[2])}`
}
