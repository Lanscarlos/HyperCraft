import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import './styles.css'
import { watchSystem } from './theme'

// The mode itself was resolved by the inline script in index.html, before any
// of this loaded; all that is left is to keep following the system if that is
// what the preference says.
watchSystem()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
