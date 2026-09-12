import { Glyph } from './Glyph'
import type { GlyphName } from './Glyph'

/**
 * What kind of thing a file is, and the icon that says so.
 *
 * Lifted out of FileManager along with the glyph set: the listing and the tree
 * both draw file rows, and one file with two icons is two answers to the same
 * question.
 */

export type Kind =
  | 'dir'
  | 'jar'
  | 'archive'
  | 'image'
  | 'schem'
  | 'config'
  | 'text'
  | 'script'
  | 'data'
  | 'plain'

const KIND_BY_EXT: Record<string, Kind> = {
  '.jar': 'jar',
  '.zip': 'archive',
  '.tar': 'archive',
  '.gz': 'archive',
  '.tgz': 'archive',
  '.rar': 'archive',
  '.7z': 'archive',
  '.xz': 'archive',
  '.zst': 'archive',
  '.png': 'image',
  '.jpg': 'image',
  '.jpeg': 'image',
  '.gif': 'image',
  '.webp': 'image',
  '.bmp': 'image',
  '.ico': 'image',
  '.svg': 'image',
  '.yml': 'config',
  '.yaml': 'config',
  '.json': 'config',
  '.properties': 'config',
  '.toml': 'config',
  '.conf': 'config',
  '.cfg': 'config',
  '.ini': 'config',
  '.xml': 'config',
  '.env': 'config',
  '.mcmeta': 'config',
  '.txt': 'text',
  '.md': 'text',
  '.log': 'text',
  '.csv': 'text',
  '.lang': 'text',
  '.snbt': 'text',
  '.sh': 'script',
  '.bat': 'script',
  '.cmd': 'script',
  '.ps1': 'script',
  '.dat': 'data',
  '.dat_old': 'data',
  '.mca': 'data',
  '.mcr': 'data',
  '.nbt': 'data',
  '.schem': 'schem',
  '.schematic': 'schem',
  '.db': 'data',
  '.lock': 'data',
}

const GLYPH: Record<Kind, GlyphName> = {
  dir: 'folder',
  jar: 'jar',
  archive: 'archive',
  image: 'image',
  schem: 'cube',
  config: 'config',
  text: 'doc',
  script: 'script',
  data: 'data',
  plain: 'doc',
}

/** Four tints rather than nine. The point of the colour is to let the eye
 *  find the folders and then the configs; a rainbow would be a legend to
 *  learn. */
const TONE: Record<Kind, string> = {
  dir: 'dir',
  jar: 'pkg',
  archive: 'pkg',
  image: 'media',
  // Same tint as an image, and for the same reason: this is a file the panel
  // can show you rather than one you have to take away and open.
  schem: 'media',
  config: 'conf',
  script: 'conf',
  text: 'plain',
  data: 'plain',
  plain: 'plain',
}

export function extensionOf(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot).toLowerCase() : ''
}

export function kindOfName(name: string): Kind {
  return KIND_BY_EXT[extensionOf(name)] ?? 'plain'
}

/** One file's icon, tinted by what kind of file it is. Directories pass `dir`:
 *  a folder is not decided by its extension. */
export function FileIcon({ name, dir }: { name: string; dir?: boolean }) {
  const kind = dir ? 'dir' : kindOfName(name)
  return (
    <span className={`fileicon fileicon--${TONE[kind]}`}>
      <Glyph name={GLYPH[kind]} />
    </span>
  )
}
