import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import { ConfirmHost } from './components/ConfirmDialog'
import './styles.css'
import { watchSystem } from './theme'

// The mode itself was resolved by the inline script in index.html, before any
// of this loaded; all that is left is to keep following the system if that is
// what the preference says.
watchSystem()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    {/* Outside the app, and outside the login gate with it: a confirmation is
        asked from wherever the panel happens to be, and the one place it is
        drawn should not be inside the tree it might be asking about. */}
    <ConfirmHost />
  </StrictMode>,
)
