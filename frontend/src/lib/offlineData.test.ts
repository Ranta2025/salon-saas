import test from 'node:test'
import assert from 'node:assert/strict'
import { noteOfflineData, subscribeOfflineData } from './offlineData.ts'

test('saved-data answers are broadcast to the screens that show them', () => {
  const seen: string[] = []
  const unsubscribe = subscribeOfflineData((path) => seen.push(path))
  try {
    noteOfflineData('/api/v1/salons/s1/bookings?from=a')
    noteOfflineData('/api/v1/salons/s1/services')
  } finally {
    unsubscribe()
  }
  assert.deepEqual(seen, [
    '/api/v1/salons/s1/bookings?from=a',
    '/api/v1/salons/s1/services',
  ])
})

test('an unsubscribed listener stops receiving saved-data answers', () => {
  const seen: string[] = []
  const unsubscribe = subscribeOfflineData((path) => seen.push(path))
  unsubscribe()
  noteOfflineData('/api/v1/salons/s1/bookings')
  assert.deepEqual(seen, [])
})
