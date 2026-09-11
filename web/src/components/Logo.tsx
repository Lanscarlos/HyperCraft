/* The brand mark, kept in one place because it lives in four: the sidebar, the
   login card, the favicon in index.html and docs/images/logo.svg. It used to be
   the ⛏ glyph in two of them and the cube in the other two, which is how the
   panel and the README ended up wearing different faces.

   Only the cube is here. The rounded brand tile around it belongs to whoever
   places the mark, because that tile is themed: --brand-face and --on-primary
   invert between light and dark, and the faces below take their ink from the
   container via currentColor so the mark inverts with it. The three opacities
   are the isometric shading, not three separate colours. */
export function Logo({ className }: { className?: string }) {
  return (
    <svg
      className={className}
      viewBox="18 13 28 32"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
    >
      <polygon points="32,13 46,21 32,29 18,21" opacity="0.96" />
      <polygon points="18,21 32,29 32,45 18,37" opacity="0.72" />
      <polygon points="46,21 32,29 32,45 46,37" opacity="0.52" />
    </svg>
  )
}
