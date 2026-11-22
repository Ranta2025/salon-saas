import React from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { isLandingPath, preloadLandingHero } from './lib/landingPreload'
import { registerServiceWorker } from './lib/pwa'
import './styles/theme.css'
import './styles/base.css'
import './styles/landing.css'
import './styles/app.css'

const container = document.getElementById('root')
if (!container) {
  throw new Error('No se encontró el contenedor #root')
}

registerServiceWorker()

// Only the landing renders the hero: preloading it on the panel would make
// Chrome warn "preload not used" on every other route.
if (isLandingPath(window.location.pathname)) {
  preloadLandingHero()
}

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
