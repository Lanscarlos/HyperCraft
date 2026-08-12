package schematic

import "fmt"

// Pre-1.13 schematics store a numeric block id and a four-bit data value where
// modern ones store a block state string. The tables here translate the vanilla
// range into the modern names, so the rest of the panel — the palette list, the
// colours the browser renders with — only ever deals with one vocabulary.
//
// The translation is deliberately one-way and lossy: it exists so an operator
// can recognise their build, not so anything can be pasted back. Data values
// that only carry rotation or growth stage are dropped; the ones that select a
// different block (wool colour, wood type, stone variant) are not.

// dyeColors is the 16-value order every coloured legacy block uses.
var dyeColors = [16]string{
	"white", "orange", "magenta", "light_blue",
	"yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue",
	"brown", "green", "red", "black",
}

// legacyVariant is an id whose data value picks a different block. mask is
// applied first because the other bits usually hold orientation — a spruce log
// laid on its side is data 6, not data 1.
type legacyVariant struct {
	mask  int
	names []string
}

func colored(suffix string) []string {
	out := make([]string, len(dyeColors))
	for i, color := range dyeColors {
		out[i] = color + suffix
	}
	return out
}

var legacyVariants = map[int]legacyVariant{
	1:   {7, []string{"stone", "granite", "polished_granite", "diorite", "polished_diorite", "andesite", "polished_andesite"}},
	3:   {3, []string{"dirt", "coarse_dirt", "podzol"}},
	5:   {7, []string{"oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks"}},
	6:   {7, []string{"oak_sapling", "spruce_sapling", "birch_sapling", "jungle_sapling", "acacia_sapling", "dark_oak_sapling"}},
	12:  {1, []string{"sand", "red_sand"}},
	17:  {3, []string{"oak_log", "spruce_log", "birch_log", "jungle_log"}},
	18:  {3, []string{"oak_leaves", "spruce_leaves", "birch_leaves", "jungle_leaves"}},
	24:  {3, []string{"sandstone", "chiseled_sandstone", "cut_sandstone"}},
	31:  {3, []string{"dead_bush", "grass", "fern"}},
	35:  {15, colored("_wool")},
	38:  {15, []string{"poppy", "blue_orchid", "allium", "azure_bluet", "red_tulip", "orange_tulip", "white_tulip", "pink_tulip", "oxeye_daisy"}},
	43:  {7, []string{"smooth_stone", "sandstone", "oak_planks", "cobblestone", "bricks", "stone_bricks", "nether_bricks", "quartz_block"}},
	44:  {7, []string{"smooth_stone_slab", "sandstone_slab", "oak_slab", "cobblestone_slab", "brick_slab", "stone_brick_slab", "nether_brick_slab", "quartz_slab"}},
	95:  {15, colored("_stained_glass")},
	98:  {3, []string{"stone_bricks", "mossy_stone_bricks", "cracked_stone_bricks", "chiseled_stone_bricks"}},
	125: {7, []string{"oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks"}},
	126: {7, []string{"oak_slab", "spruce_slab", "birch_slab", "jungle_slab", "acacia_slab", "dark_oak_slab"}},
	139: {1, []string{"cobblestone_wall", "mossy_cobblestone_wall"}},
	155: {3, []string{"quartz_block", "chiseled_quartz_block", "quartz_pillar", "quartz_pillar"}},
	159: {15, colored("_terracotta")},
	160: {15, colored("_stained_glass_pane")},
	161: {1, []string{"acacia_leaves", "dark_oak_leaves"}},
	162: {1, []string{"acacia_log", "dark_oak_log"}},
	168: {3, []string{"prismarine", "prismarine_bricks", "dark_prismarine"}},
	171: {15, colored("_carpet")},
	175: {7, []string{"sunflower", "lilac", "tall_grass", "large_fern", "rose_bush", "peony"}},
	179: {3, []string{"red_sandstone", "chiseled_red_sandstone", "cut_red_sandstone"}},
	251: {15, colored("_concrete")},
	252: {15, colored("_concrete_powder")},
}

// legacyNames covers the ids whose data value does not change what the block
// is. Ids missing from both tables come back as minecraft:legacy_<id>, which is
// what a modded block from a pre-1.13 pack looks like.
var legacyNames = map[int]string{
	0: "air", 2: "grass_block", 4: "cobblestone", 7: "bedrock",
	8: "water", 9: "water", 10: "lava", 11: "lava",
	13: "gravel", 14: "gold_ore", 15: "iron_ore", 16: "coal_ore",
	19: "sponge", 20: "glass", 21: "lapis_ore", 22: "lapis_block",
	23: "dispenser", 25: "note_block", 26: "red_bed", 27: "powered_rail",
	28: "detector_rail", 29: "sticky_piston", 30: "cobweb", 32: "dead_bush",
	33: "piston", 34: "piston_head", 36: "moving_piston", 37: "dandelion",
	39: "brown_mushroom", 40: "red_mushroom", 41: "gold_block", 42: "iron_block",
	45: "bricks", 46: "tnt", 47: "bookshelf", 48: "mossy_cobblestone",
	49: "obsidian", 50: "torch", 51: "fire", 52: "spawner",
	53: "oak_stairs", 54: "chest", 55: "redstone_wire", 56: "diamond_ore",
	57: "diamond_block", 58: "crafting_table", 59: "wheat", 60: "farmland",
	61: "furnace", 62: "furnace", 63: "oak_sign", 64: "oak_door",
	65: "ladder", 66: "rail", 67: "cobblestone_stairs", 68: "oak_wall_sign",
	69: "lever", 70: "stone_pressure_plate", 71: "iron_door", 72: "oak_pressure_plate",
	73: "redstone_ore", 74: "redstone_ore", 75: "redstone_torch", 76: "redstone_torch",
	77: "stone_button", 78: "snow", 79: "ice", 80: "snow_block",
	81: "cactus", 82: "clay", 83: "sugar_cane", 84: "jukebox",
	85: "oak_fence", 86: "carved_pumpkin", 87: "netherrack", 88: "soul_sand",
	89: "glowstone", 90: "nether_portal", 91: "jack_o_lantern", 92: "cake",
	93: "repeater", 94: "repeater", 96: "oak_trapdoor", 97: "infested_stone",
	99: "brown_mushroom_block", 100: "red_mushroom_block", 101: "iron_bars", 102: "glass_pane",
	103: "melon", 104: "pumpkin_stem", 105: "melon_stem", 106: "vine",
	107: "oak_fence_gate", 108: "brick_stairs", 109: "stone_brick_stairs", 110: "mycelium",
	111: "lily_pad", 112: "nether_bricks", 113: "nether_brick_fence", 114: "nether_brick_stairs",
	115: "nether_wart", 116: "enchanting_table", 117: "brewing_stand", 118: "cauldron",
	119: "end_portal", 120: "end_portal_frame", 121: "end_stone", 122: "dragon_egg",
	123: "redstone_lamp", 124: "redstone_lamp", 127: "cocoa", 128: "sandstone_stairs",
	129: "emerald_ore", 130: "ender_chest", 131: "tripwire_hook", 132: "tripwire",
	133: "emerald_block", 134: "spruce_stairs", 135: "birch_stairs", 136: "jungle_stairs",
	137: "command_block", 138: "beacon", 140: "flower_pot", 141: "carrots",
	142: "potatoes", 143: "oak_button", 144: "skeleton_skull", 145: "anvil",
	146: "trapped_chest", 147: "light_weighted_pressure_plate", 148: "heavy_weighted_pressure_plate",
	149: "comparator", 150: "comparator", 151: "daylight_detector", 152: "redstone_block",
	153: "nether_quartz_ore", 154: "hopper", 156: "quartz_stairs", 157: "activator_rail",
	158: "dropper", 163: "acacia_stairs", 164: "dark_oak_stairs", 165: "slime_block",
	166: "barrier", 167: "iron_trapdoor", 169: "sea_lantern", 170: "hay_block",
	172: "terracotta", 173: "coal_block", 174: "packed_ice", 176: "white_banner",
	177: "white_wall_banner", 178: "daylight_detector", 180: "red_sandstone_stairs",
	181: "red_sandstone_slab", 182: "red_sandstone_slab", 183: "spruce_fence_gate",
	184: "birch_fence_gate", 185: "jungle_fence_gate", 186: "dark_oak_fence_gate",
	187: "acacia_fence_gate", 188: "spruce_fence", 189: "birch_fence", 190: "jungle_fence",
	191: "dark_oak_fence", 192: "acacia_fence", 193: "spruce_door", 194: "birch_door",
	195: "jungle_door", 196: "acacia_door", 197: "dark_oak_door", 198: "end_rod",
	199: "chorus_plant", 200: "chorus_flower", 201: "purpur_block", 202: "purpur_pillar",
	203: "purpur_stairs", 204: "purpur_slab", 205: "purpur_slab", 206: "end_stone_bricks",
	207: "beetroots", 208: "dirt_path", 209: "end_gateway", 210: "repeating_command_block",
	211: "chain_command_block", 212: "frosted_ice", 213: "magma_block", 214: "nether_wart_block",
	215: "red_nether_bricks", 216: "bone_block", 217: "structure_void", 218: "observer",
	253: "structure_block", 255: "structure_block",
}

func init() {
	// Two runs of sixteen, one colour each, in the same order as every other
	// coloured block. Written as a loop rather than 32 more table lines.
	for i, color := range dyeColors {
		legacyNames[219+i] = color + "_shulker_box"
		legacyNames[235+i] = color + "_glazed_terracotta"
	}
}

// legacyName renders one id/data pair as a modern namespaced block state.
func legacyName(id, data int) string {
	if variant, ok := legacyVariants[id]; ok {
		if index := data & variant.mask; index < len(variant.names) {
			return "minecraft:" + variant.names[index]
		}
	}
	if name, ok := legacyNames[id]; ok {
		return "minecraft:" + name
	}
	if data != 0 {
		return fmt.Sprintf("minecraft:legacy_%d_%d", id, data)
	}
	return fmt.Sprintf("minecraft:legacy_%d", id)
}
