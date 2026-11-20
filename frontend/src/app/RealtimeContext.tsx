import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import {
  RealtimeClient,
  type RealtimeEventType,
  type RealtimeStatus,
  type TypedRealtimeEvent,
} from '../lib/realtime'
import { useSalon } from './SalonContext'

type RealtimeContextValue = {
  status: RealtimeStatus
  subscribe: <K extends RealtimeEventType>(
    type: K,
    handler: (event: TypedRealtimeEvent<K>) => void,
  ) => () => void
}

const RealtimeContext = createContext<RealtimeContextValue | null>(null)

/**
 * One websocket for the whole panel. Notifications, dashboard and calendar share
 * it through `subscribe`, so the server sees a single connection per session and
 * every page reacts to the same `booking.created`/`sale.created` stream.
 */
export function RealtimeProvider({ children }: { children: ReactNode }) {
  const { salonId } = useSalon()
  const [status, setStatus] = useState<RealtimeStatus>('idle')
  const client = useMemo(() => new RealtimeClient(), [salonId])

  useEffect(() => {
    if (!salonId) {
      return
    }
    const offStatus = client.onStatus(setStatus)
    client.start()
    return () => {
      offStatus()
      client.stop()
    }
  }, [client, salonId])

  const value = useMemo<RealtimeContextValue>(
    () => ({
      status,
      subscribe: (type, handler) => client.subscribe(type, handler),
    }),
    [client, status],
  )

  return <RealtimeContext.Provider value={value}>{children}</RealtimeContext.Provider>
}

export function useRealtime(): RealtimeContextValue {
  const value = useContext(RealtimeContext)
  if (!value) {
    throw new Error('useRealtime must be used inside <RealtimeProvider>')
  }
  return value
}
