import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import { ConfirmHost } from './components/ConfirmDialog'
import './styles.css'
import { syncFavicon, watchSystem } from './theme'

// The mode itself was resolved by the inline script in index.html, before any
// of this loaded; all that is left is to keep following the system if that is
// what the preference says.
watchSystem()

// The 配色 was resolved there too, but the tab icon could not be: it is an
// href, and the colours it needs are in the stylesheet that had not arrived
// yet. The document ships with the default scheme's mark, so this is the one
// repaint a reload under any other scheme still owes.
syncFavicon()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    {/* Outside the app, and outside the login gate with it: a confirmation is
        asked from wherever the panel happens to be, and the one place it is
        drawn should not be inside the tree it might be asking about. */}
    <ConfirmHost />
  </StrictMode>,
)
