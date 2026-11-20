/**
 * Session storage. Keeps the JWT and the active salon id in localStorage,
 * falling back to memory when storage is unavailable (private mode, tests).
 */

export const TOKEN_KEY = 'salon.token'
export const SALON_KEY = 'salon.salon_id'
export const USER_KEY = 'salon.user_id'

const memory = new Map<string, string>()

function backend(): Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage
    }
  } catch {
    // access to localStorage can throw (Safari private mode)
  }
  return {
    getItem: (key) => memory.get(key) ?? null,
    setItem: (key, value) => void memory.set(key, value),
    removeItem: (key) => void memory.delete(key),
  }
}

function read(key: string): string | null {
  try {
    const value = backend().getItem(key)
    return value === '' ? null : value
  } catch {
    return null
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) {
      backend().removeItem(key)
    } else {
      backend().setItem(key, value)
    }
  } catch {
    // ignore quota/security errors: the UI still works for the current render
  }
}

export function getToken(): string | null {
  return read(TOKEN_KEY)
}

export function setToken(token: string): void {
  write(TOKEN_KEY, token)
}

export function clearToken(): void {
  write(TOKEN_KEY, null)
}

export function getUserId(): string | null {
  return read(USER_KEY)
}

export function setUserId(userId: string): void {
  const trimmed = userId.trim()
  write(USER_KEY, trimmed === '' ? null : trimmed)
}

export function clearUserId(): void {
  write(USER_KEY, null)
}

export function getSalonId(): string | null {
  return read(SALON_KEY)
}

export function setSalonId(salonId: string): void {
  const trimmed = salonId.trim()
  write(SALON_KEY, trimmed === '' ? null : trimmed)
}

export function clearSalonId(): void {
  write(SALON_KEY, null)
}
