import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { getSalonId, setSalonId as persistSalonId } from '../lib/session'

type SalonContextValue = {
  salonId: string | null
  updateSalonId: (salonId: string) => void
}

const SalonContext = createContext<SalonContextValue | null>(null)

/**
 * Active salon id. The API has no "my salons" endpoint yet, so the id is kept
 * in localStorage and can be edited from the app header.
 */
export function SalonProvider({ children }: { children: ReactNode }) {
  const [salonId, setSalonIdState] = useState<string | null>(() => getSalonId())

  const updateSalonId = useCallback((next: string) => {
    persistSalonId(next)
    setSalonIdState(getSalonId())
  }, [])

  const value = useMemo(() => ({ salonId, updateSalonId }), [salonId, updateSalonId])

  return <SalonContext.Provider value={value}>{children}</SalonContext.Provider>
}

export function useSalon(): SalonContextValue {
  const value = useContext(SalonContext)
  if (!value) {
    throw new Error('useSalon must be used inside <SalonProvider>')
  }
  return value
}
