import test from 'node:test'
import assert from 'node:assert/strict'
import {
  AGENDA_MARKER_LABEL,
  agendaMarkers,
  canOpenMyAgenda,
  formatAgendaDate,
  formatAgendaDateLower,
  initialCalendarView,
  isBookableInStaff,
  nextAgendaEntry,
  selectMyBookings,
  sortAgendaBookings,
  todayAgendaCount,
  todayCountLabel,
  visibleCalendarBookings,
  weekDates,
  weekOverview,
  weekRange,
  weekStartOf,
} from './myAgenda.ts'
import type { Booking } from '../../../types.ts'

const ZONE = 'America/Mexico_City'

function booking(over: Partial<Booking> & Pick<Booking, 'id' | 'starts_at' | 'ends_at'>): Booking {
  return {
    professional_id: 'p1',
    client_name: 'Ana',
    status: 'booked',
    ...over,
  }
}

// Friday 2025-11-21, 10:00 in the salon zone (UTC-6).
const NOW = new Date('2025-11-21T16:00:00.000Z')

test('selectMyBookings keeps only the rows of one professional and ignores a missing id', () => {
  const rows = [
    booking({ id: 'b1', professional_id: 'p1', starts_at: '2025-11-21T16:00:00Z', ends_at: '2025-11-21T16:30:00Z' }),
    booking({ id: 'b2', professional_id: 'p2', starts_at: '2025-11-21T17:00:00Z', ends_at: '2025-11-21T17:30:00Z' }),
    booking({ id: 'b3', professional_id: 'p1', starts_at: '2025-11-21T18:00:00Z', ends_at: '2025-11-21T18:30:00Z' }),
  ]
  assert.deepEqual(
    selectMyBookings(rows, 'p1').map((row) => row.id),
    ['b1', 'b3'],
  )
  assert.deepEqual(selectMyBookings(rows, null), [])
  assert.deepEqual(selectMyBookings(rows, '  '), [])
  assert.deepEqual(selectMyBookings(rows, 'p9'), [])
})

test('sortAgendaBookings orders by start, then by end, and never mutates the input', () => {
  const rows = [
    booking({ id: 'late', starts_at: '2025-11-21T18:00:00Z', ends_at: '2025-11-21T18:30:00Z' }),
    booking({ id: 'early-long', starts_at: '2025-11-21T16:00:00Z', ends_at: '2025-11-21T17:00:00Z' }),
    booking({ id: 'early-short', starts_at: '2025-11-21T16:00:00Z', ends_at: '2025-11-21T16:30:00Z' }),
  ]
  assert.deepEqual(
    sortAgendaBookings(rows).map((row) => row.id),
    ['early-short', 'early-long', 'late'],
  )
  assert.equal(rows[0]?.id, 'late')
})

test('agendaMarkers marks the booking in progress as now and the following one as next', () => {
  const rows = [
    booking({ id: 'later', starts_at: '2025-11-21T17:00:00Z', ends_at: '2025-11-21T17:30:00Z' }),
    booking({ id: 'current', starts_at: '2025-11-21T15:45:00Z', ends_at: '2025-11-21T16:15:00Z' }),
    booking({ id: 'past', starts_at: '2025-11-21T14:00:00Z', ends_at: '2025-11-21T14:30:00Z' }),
  ]
  const entries = agendaMarkers(rows, NOW)
  assert.deepEqual(
    entries.map((entry) => [entry.booking.id, entry.marker]),
    [
      ['past', null],
      ['current', 'now'],
      ['later', 'next'],
    ],
  )
  assert.equal(AGENDA_MARKER_LABEL.now, 'Ahora')
  assert.equal(AGENDA_MARKER_LABEL.next, 'Siguiente')
})

test('agendaMarkers takes the next marker from the first open future booking only', () => {
  const rows = [
    booking({ id: 'cancelled-next', starts_at: '2025-11-21T16:30:00Z', ends_at: '2025-11-21T17:00:00Z', status: 'cancelled' }),
    booking({ id: 'done-next', starts_at: '2025-11-21T17:00:00Z', ends_at: '2025-11-21T17:30:00Z', status: 'done' }),
    booking({ id: 'open-next', starts_at: '2025-11-21T18:00:00Z', ends_at: '2025-11-21T18:30:00Z' }),
    booking({ id: 'open-later', starts_at: '2025-11-21T19:00:00Z', ends_at: '2025-11-21T19:30:00Z' }),
  ]
  const entries = agendaMarkers(rows, NOW)
  assert.equal(entries.find((entry) => entry.booking.id === 'cancelled-next')?.marker, null)
  assert.equal(entries.find((entry) => entry.booking.id === 'done-next')?.marker, null)
  assert.equal(entries.find((entry) => entry.booking.id === 'open-next')?.marker, 'next')
  assert.equal(entries.find((entry) => entry.booking.id === 'open-later')?.marker, null)
})

test('agendaMarkers never marks anything without a clock (another day than today)', () => {
  const rows = [
    booking({ id: 'b1', starts_at: '2025-11-20T16:00:00Z', ends_at: '2025-11-20T16:30:00Z' }),
  ]
  assert.deepEqual(
    agendaMarkers(rows, null).map((entry) => entry.marker),
    [null],
  )
})

test('nextAgendaEntry is the next marker, never a past or closed booking', () => {
  const entries = agendaMarkers(
    [
      booking({ id: 'past', starts_at: '2025-11-21T14:00:00Z', ends_at: '2025-11-21T14:30:00Z' }),
      booking({ id: 'next', starts_at: '2025-11-21T16:30:00Z', ends_at: '2025-11-21T17:00:00Z' }),
    ],
    NOW,
  )
  assert.equal(nextAgendaEntry(entries)?.booking.id, 'next')
  assert.equal(
    nextAgendaEntry(
      agendaMarkers(
        [booking({ id: 'past', starts_at: '2025-11-21T14:00:00Z', ends_at: '2025-11-21T14:30:00Z' })],
        NOW,
      ),
    ),
    null,
  )
})

test('the week runs Monday to Sunday around the given salon date', () => {
  assert.equal(weekStartOf('2025-11-21'), '2025-11-17')
  assert.equal(weekStartOf('2025-11-17'), '2025-11-17')
  assert.equal(weekStartOf('2025-11-23'), '2025-11-17')
  assert.deepEqual(weekDates('2025-11-17'), [
    '2025-11-17',
    '2025-11-18',
    '2025-11-19',
    '2025-11-20',
    '2025-11-21',
    '2025-11-22',
    '2025-11-23',
  ])
})

test('weekly counts use the salon day, so a late-night booking does not fall on its UTC day', () => {
  // 23:30 on Friday 2025-11-21 in Mexico City is already Saturday 2025-11-22 in UTC.
  const nearMidnight = booking({
    id: 'near-midnight',
    starts_at: '2025-11-22T05:30:00.000Z',
    ends_at: '2025-11-22T06:00:00.000Z',
  })
  const rows = [
    nearMidnight,
    booking({ id: 'friday', starts_at: '2025-11-21T16:00:00.000Z', ends_at: '2025-11-21T16:30:00.000Z' }),
    booking({
      id: 'friday-cancelled',
      starts_at: '2025-11-21T18:00:00.000Z',
      ends_at: '2025-11-21T18:30:00.000Z',
      status: 'cancelled',
    }),
  ]
  const week = weekOverview(rows, '2025-11-17', ZONE, '2025-11-21')
  assert.deepEqual(
    week.map((day) => [day.date, day.count]),
    [
      ['2025-11-17', 0],
      ['2025-11-18', 0],
      ['2025-11-19', 0],
      ['2025-11-20', 0],
      ['2025-11-21', 2],
      ['2025-11-22', 0],
      ['2025-11-23', 0],
    ],
  )
  assert.equal(week[4]?.shortLabel, 'Vie')
  assert.equal(week[4]?.isToday, true)
  assert.equal(week[0]?.label, 'Lunes')
  assert.equal(week[6]?.label, 'Domingo')
  assert.deepEqual(
    week[4]?.bookings.map((row) => row.id),
    ['friday', 'near-midnight'],
  )
})

test('today counts and labels are exact Spanish copy', () => {
  const rows = [
    booking({ id: 'b1', starts_at: '2025-11-21T16:00:00.000Z', ends_at: '2025-11-21T16:30:00.000Z' }),
    booking({ id: 'b2', starts_at: '2025-11-22T05:30:00.000Z', ends_at: '2025-11-22T06:00:00.000Z' }),
    booking({ id: 'b3', starts_at: '2025-11-21T18:00:00.000Z', ends_at: '2025-11-21T18:30:00.000Z', status: 'cancelled' }),
  ]
  assert.equal(todayAgendaCount(rows, '2025-11-21', ZONE), 2)
  assert.equal(todayAgendaCount(rows, '2025-11-22', ZONE), 0)
  assert.equal(todayCountLabel(0), 'Sin turnos hoy')
  assert.equal(todayCountLabel(1), '1 turno hoy')
  assert.equal(todayCountLabel(3), '3 turnos hoy')
})

test('the salon date is printed in Spanish and capitalised', () => {
  assert.equal(formatAgendaDate('2025-11-21'), 'Viernes 21 de noviembre de 2025')
  assert.equal(formatAgendaDate('2025-01-01'), 'Miércoles 1 de enero de 2025')
  assert.equal(formatAgendaDateLower('2025-11-21'), 'viernes 21 de noviembre de 2025')
})

test('weekRange asks the API for Monday 00:00 to next Monday 00:00 in the salon zone', () => {
  assert.deepEqual(weekRange('2025-11-17', ZONE), {
    from: '2025-11-17T06:00:00.000Z',
    to: '2025-11-24T06:00:00.000Z',
  })
})

test('isBookableInStaff reads the staff list of the salon, which is already filtered by F2a', () => {
  const staff = [{ id: 'u1' }, { id: 'u2' }]
  assert.equal(isBookableInStaff(staff, 'u2'), true)
  assert.equal(isBookableInStaff(staff, 'u9'), false)
  assert.equal(isBookableInStaff(staff, null), false)
  assert.equal(isBookableInStaff(staff, ' '), false)
  assert.equal(isBookableInStaff([], 'u1'), false)
})

test('canOpenMyAgenda trusts /staff and falls back to the professional role only when it failed', () => {
  const staff = [{ id: 'u1' }]
  assert.equal(
    canOpenMyAgenda({ staff, staffLoaded: true, userId: 'u1', role: 'professional' }),
    true,
  )
  assert.equal(
    canOpenMyAgenda({ staff, staffLoaded: true, userId: 'u9', role: 'professional' }),
    false,
  )
  assert.equal(
    canOpenMyAgenda({ staff: [], staffLoaded: false, userId: 'u1', role: 'professional' }),
    true,
  )
  assert.equal(
    canOpenMyAgenda({ staff: [], staffLoaded: false, userId: 'u1', role: 'owner' }),
    false,
  )
  assert.equal(
    canOpenMyAgenda({ staff: [], staffLoaded: false, userId: 'u1', role: 'reception' }),
    false,
  )
  assert.equal(canOpenMyAgenda({ staff, staffLoaded: true, userId: null, role: 'owner' }), false)
})

test('the calendar opens on my agenda for whoever attends and on the whole salon otherwise', () => {
  assert.equal(initialCalendarView(true), 'mine')
  assert.equal(initialCalendarView(false), 'all')
})

test('the calendar toggle filters the visible bookings by professional', () => {
  const rows = [
    booking({ id: 'b1', professional_id: 'p1', starts_at: '2025-11-21T16:00:00Z', ends_at: '2025-11-21T16:30:00Z' }),
    booking({ id: 'b2', professional_id: 'p2', starts_at: '2025-11-21T17:00:00Z', ends_at: '2025-11-21T17:30:00Z' }),
  ]
  assert.deepEqual(
    visibleCalendarBookings(rows, 'mine', 'p1').map((row) => row.id),
    ['b1'],
  )
  assert.deepEqual(
    visibleCalendarBookings(rows, 'all', 'p1').map((row) => row.id),
    ['b1', 'b2'],
  )
  assert.deepEqual(visibleCalendarBookings(rows, 'mine', null), [])
})
