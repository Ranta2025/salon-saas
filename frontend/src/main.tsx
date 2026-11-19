import React from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Link } from 'react-router-dom'

function Layout({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontFamily: 'system-ui', padding: 24, maxWidth: 900, margin: '0 auto' }}>
      <nav style={{ display: 'flex', gap: 12, marginBottom: 24 }}>
        <Link to="/">Salón SaaS</Link>
        <Link to="/dashboard">Dashboard</Link>
        <Link to="/calendar">Calendario</Link>
        <Link to="/pos">POS</Link>
        <Link to="/clients">Clientes</Link>
      </nav>
      {children}
    </div>
  )
}

function Home() {
  return <h1>Salón SaaS — esqueleto listo, arrancamos por auth + tenants</h1>
}

function Placeholder({ name }: { name: string }) {
  return <h2>{name} (pendiente Fase 1-3)</h2>
}

createRoot(document.getElementById('root')!).render(
  <BrowserRouter>
    <Layout>
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/dashboard" element={<Placeholder name="Dashboard" />} />
        <Route path="/calendar" element={<Placeholder name="Calendario de reservas" />} />
        <Route path="/pos" element={<Placeholder name="POS / Caja" />} />
        <Route path="/clients" element={<Placeholder name="Clientes" />} />
      </Routes>
    </Layout>
  </BrowserRouter>
)
