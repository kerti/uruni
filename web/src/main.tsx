import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { IconContext } from '@phosphor-icons/react'
import './index.css'
import App from './App.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {/* Every icon Phosphor duotone, 24px unless its own class sizes it.
        index.css drops the shade where it would collide with a dark fill. */}
    <IconContext.Provider value={{ weight: 'duotone', size: 24 }}>
      <App />
    </IconContext.Provider>
  </StrictMode>,
)
