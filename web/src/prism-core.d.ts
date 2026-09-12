/**
 * Types for Prism's bare core entry.
 *
 * @types/prismjs only declares the package's main module, which is the build
 * that carries markup/css/clike/javascript with it. The editor imports the core
 * alone and names its grammars one by one (see highlight.ts) to keep them out
 * of the binary, so the core needs a declaration of its own — the same shape,
 * since it is the same object with an empty language table.
 */
declare module 'prismjs/components/prism-core' {
  import Prism from 'prismjs'

  export default Prism
}
