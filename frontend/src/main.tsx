import React from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
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

createRoot(container).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
