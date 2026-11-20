import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react'
import { submitQueuedOperation } from '../lib/api'
import { isBrowserOnline, subscribeOnlineStatus } from '../lib/connectivity'
import {
  OfflineQueue,
  type DroppedOperation,
  type QueuedOperation,
} from '../lib/offlineQueue'
import type { SaleInput } from '../types'
import { useSalon } from './SalonContext'

/** Single queue of the app. The transport is the API client (`submitQueuedOperation`). */
export const offlineQueue = new OfflineQueue({
  send: submitQueuedOperation,
  canSend: isBrowserOnline,
})

type OfflineContextValue = {
  online: boolean
  pending: QueuedOperation[]
  pendingCount: number
  dropped: DroppedOperation[]
  syncing: boolean
  /** True when a 401 paused the queue until the next successful login. */
  paused: boolean
  /** Operations confirmed by the server since the app loaded. */
  syncedTotal: number
  enqueueSale: (salonId: string, input: SaleInput) => QueuedOperation
  syncNow: () => Promise<void>
  dismissDropped: () => void
}

const OfflineContext = createContext<OfflineContextValue | null>(null)

/**
 * Connectivity + offline queue state for the whole shell. Flushes on mount (a
 * reload with pending work) and every time the browser reports being online.
 */
export function OfflineProvider({ children }: { children: ReactNode }) {
  const { user } = useSalon()
  const snapshot = useSyncExternalStore(
    useCallback((onChange: () => void) => offlineQueue.subscribe(onChange), []),
    useCallback(() => offlineQueue.snapshot(), []),
    useCallback(() => offlineQueue.snapshot(), []),
  )
  const [online, setOnline] = useState<boolean>(() => isBrowserOnline())
  const syncingRef = useRef(false)

  const syncNow = useCallback(async (): Promise<void> => {
    if (!isBrowserOnline() || syncingRef.current) {
      return
    }
    syncingRef.current = true
    try {
      await offlineQueue.flush()
    } finally {
      syncingRef.current = false
    }
  }, [])

  // One queue per account: pending sales of a previous user are never sent by
  // the session that signs in next on the same browser.
  useEffect(() => {
    offlineQueue.setScope(user?.id ?? null)
  }, [user])

  useEffect(() => {
    if (isBrowserOnline()) {
      void syncNow()
    }
    return subscribeOnlineStatus((next) => {
      setOnline(next)
      if (next) {
        void syncNow()
      }
    })
  }, [syncNow])

  const enqueueSale = useCallback(
    (salonId: string, input: SaleInput): QueuedOperation => {
      const operation = offlineQueue.enqueue('sale', salonId, input, input.client_op_id)
      // Try right away: if only the last request failed, the sale reaches the
      // server without waiting for the next online event.
      void syncNow()
      return operation
    },
    [syncNow],
  )

  const dismissDropped = useCallback(() => {
    offlineQueue.clearDropped()
  }, [])

  const value = useMemo<OfflineContextValue>(
    () => ({
      online,
      pending: snapshot.operations,
      pendingCount: snapshot.pendingCount,
      dropped: snapshot.dropped,
      syncing: snapshot.syncing,
      paused: snapshot.paused,
      syncedTotal: snapshot.syncedTotal,
      enqueueSale,
      syncNow,
      dismissDropped,
    }),
    [online, snapshot, enqueueSale, syncNow, dismissDropped],
  )

  return <OfflineContext.Provider value={value}>{children}</OfflineContext.Provider>
}

export function useOffline(): OfflineContextValue {
  const value = useContext(OfflineContext)
  if (!value) {
    throw new Error('useOffline must be used inside <OfflineProvider>')
  }
  return value
}
