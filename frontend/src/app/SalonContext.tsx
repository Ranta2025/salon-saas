import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { fetchMe } from '../lib/api'
import { pickSalonId, type Me, type SalonMembership } from '../lib/me'
import {
  clearSalonId,
  getSalonId,
  getToken,
  setSalonId as persistSalonId,
  setUserId,
} from '../lib/session'
import type { AuthUser } from '../types'

type SalonContextValue = {
  salonId: string | null
  /** Salon the active user belongs to, as returned by GET /api/v1/me. */
  salons: SalonMembership[]
  user: AuthUser | null
  loading: boolean
  /** Why the identity call failed; the panel still works with the stored salon. */
  error: string | null
  updateSalonId: (salonId: string) => void
  refresh: () => void
}

const SalonContext = createContext<SalonContextValue | null>(null)

function message(error: unknown): string {
  return error instanceof Error && error.message !== ''
    ? error.message
    : 'No pudimos cargar los salones de tu cuenta.'
}

/**
 * Active salon of the session. After login the app asks `/me`, keeps the first
 * salon of the account (or the stored one while it still belongs to the user)
 * and offers a switcher when there are several.
 */
export function SalonProvider({ children }: { children: ReactNode }) {
  const [salonId, setSalonIdState] = useState<string | null>(() => getSalonId())
  const [user, setUser] = useState<AuthUser | null>(null)
  const [salons, setSalons] = useState<SalonMembership[]>([])
  const [loading, setLoading] = useState<boolean>(() => getToken() !== null)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const refresh = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!getToken()) {
      setLoading(false)
      setSalons([])
      setUser(null)
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchMe()
      .then((me: Me) => {
        if (!active) {
          return
        }
        setUser(me.user)
        setUserId(me.user.id)
        setSalons(me.salons)
        const next = pickSalonId(getSalonId(), me.salons)
        if (next) {
          persistSalonId(next)
        } else {
          clearSalonId()
        }
        setSalonIdState(next)
      })
      .catch((err: unknown) => {
        if (active) {
          setError(message(err))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [reloadKey])

  const updateSalonId = useCallback((next: string) => {
    persistSalonId(next)
    setSalonIdState(getSalonId())
  }, [])

  const value = useMemo(
    () => ({
      salonId,
      salons,
      user,
      loading,
      error,
      updateSalonId,
      refresh,
    }),
    [salonId, salons, user, loading, error, updateSalonId, refresh],
  )

  return <SalonContext.Provider value={value}>{children}</SalonContext.Provider>
}

export function useSalon(): SalonContextValue {
  const value = useContext(SalonContext)
  if (!value) {
    throw new Error('useSalon must be used inside <SalonProvider>')
  }
  return value
}
