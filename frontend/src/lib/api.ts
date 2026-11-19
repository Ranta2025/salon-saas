const API = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

export async function ping(): Promise<{ pong: boolean }> {
  const r = await fetch(`${API}/api/v1/ping`)
  return r.json()
}

export async function health(): Promise<{ ok: boolean; service: string }> {
  const r = await fetch(`${API}/health`)
  return r.json()
}
