import { useState } from 'react'

/**
 * A plugin's face, wherever a plugin is listed.
 *
 * One component rather than three copies because the fallback is the whole
 * point of it and the fallback is easy to leave out. Every one of these URLs
 * is a request to somebody else's CDN — Modrinth's, Hangar's, SpigotMC's,
 * GitHub's — and an operator behind a restrictive network reaches none of
 * them. A broken-image glyph in that situation is worse than no icon at all,
 * so a failed load becomes the initial the row would have had anyway.
 */
export function PluginIcon({
  src,
  name,
  className,
}: {
  src?: string
  name: string
  /** The size and shape belong to the caller: 32px in a search result, 28px in
   *  a library card. */
  className: string
}) {
  const [broken, setBroken] = useState(false)

  if (!src || broken) {
    return (
      <span className={`${className} plugin-icon--blank`} aria-hidden="true">
        {name.slice(0, 1).toUpperCase()}
      </span>
    )
  }
  return (
    <img
      className={className}
      src={src}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  )
}
