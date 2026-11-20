import test from 'node:test'
import assert from 'node:assert/strict'
import {
  addDaysToDateValue,
  dayRange,
  formatHour,
  formatTimeOfDay,
  groupByProfessional,
  minutesFromMidnight,
  positionInWindow,
  toDateInputValue,
  fromDateInputValue,
  overlapLayout,
  toDateTimeIso,
  addMinutesToIso,
} from './datetime.ts'

const TZ = 'America/Mexico_City'

test('dayRange covers the whole requested local day in RFC3339', () => {
  const { from, to } = dayRange(new Date('2025-03-14T18:30:00Z'), TZ)
  assert.equal(from, '2025-03-14T06:00:00.000Z')
  assert.equal(to, '2025-03-15T06:00:00.000Z')
})

test('dayRange handles a DST day without losing hours', () => {
  const { from, to } = dayRange(new Date('2025-11-02T12:00:00Z'), TZ)
  const span = (Date.parse(to) - Date.parse(from)) / 3_600_000
  assert.ok(span >= 23 && span <= 25, `span ${span}h should be a calendar day`)
})

test('toDateInputValue / fromDateInputValue round trip the calendar control', () => {
  const d = new Date('2025-03-14T18:30:00Z')
  assert.equal(toDateInputValue(d, TZ), '2025-03-14')
  assert.equal(fromDateInputValue('2025-03-14', TZ), '2025-03-14T06:00:00.000Z')
  // moving the input by one day moves the window by one day
  assert.equal(fromDateInputValue('2025-03-15', TZ), '2025-03-15T06:00:00.000Z')
})

test('addDaysToDateValue walks the calendar across month and year borders', () => {
  assert.equal(addDaysToDateValue('2025-03-14', 1), '2025-03-15')
  assert.equal(addDaysToDateValue('2025-03-14', -1), '2025-03-13')
  assert.equal(addDaysToDateValue('2025-01-31', 1), '2025-02-01')
  assert.equal(addDaysToDateValue('2025-12-31', 1), '2026-01-01')
  assert.equal(addDaysToDateValue('2024-02-28', 1), '2024-02-29')
})

test('minutesFromMidnight and positionInWindow drive the timeline geometry', () => {
  assert.equal(minutesFromMidnight('2025-03-14T15:30:00Z', TZ), 9 * 60 + 30)
  const win = { startHour: 9, endHour: 19 }
  assert.deepEqual(positionInWindow(9 * 60, 9 * 60, win), { offset: 0, duration: 0 })
  const p = positionInWindow(10 * 60, 11 * 60, win)
  assert.equal(p.offset, 60 / (10 * 60))
  assert.ok(Math.abs(p.duration - 1 / 10) < 1e-9)
  // a service starting before the window keeps its visible part
  assert.deepEqual(positionInWindow(8 * 60, 10 * 60, win), { offset: 0, duration: 0.1 })
})

test('positionInWindow clamps appointments outside the visible window', () => {
  const win = { startHour: 9, endHour: 19 }
  assert.equal(positionInWindow(7 * 60, 8 * 60, win).offset, 0)
  assert.equal(positionInWindow(22 * 60, 23 * 60, win).duration, 0)
})

test('formatTimeOfDay and formatHour are stable for the UI', () => {
  assert.equal(formatHour(9), '09:00')
  assert.equal(formatHour(15), '15:00')
  assert.equal(formatTimeOfDay('2025-03-14T15:30:00Z', TZ), '09:30')
})

test('groupByProfessional keeps unmatched professionals visible', () => {
  const groups = groupByProfessional(
    [
      { professional_id: 'pro-1', starts_at: '2025-03-14T16:00:00Z', ends_at: '2025-03-14T17:00:00Z' },
      { professional_id: 'pro-2', starts_at: '2025-03-14T18:00:00Z', ends_at: '2025-03-14T18:30:00Z' },
      { professional_id: 'pro-1', starts_at: '2025-03-14T19:00:00Z', ends_at: '2025-03-14T20:00:00Z' },
    ],
    [{ id: 'pro-1', name: 'Ana' }],
  )
  assert.equal(groups.length, 2)
  assert.equal(groups[0]?.name, 'Ana')
  assert.equal(groups[0]?.bookings.length, 2)
  assert.equal(groups[1]?.name, 'Profesional pro-2')
})

test('overlapLayout pushes overlapping appointments into separate columns', () => {
  const blocks = overlapLayout([
    { id: 'a', start: 0, end: 60 },
    { id: 'b', start: 30, end: 90 },
    { id: 'c', start: 120, end: 180 },
  ])
  assert.deepEqual(blocks.map((b) => [b.id, b.column, b.columns]), [
    ['a', 0, 2],
    ['b', 1, 2],
    ['c', 0, 1],
  ])
})

test('toDateTimeIso turns a date and a local time into the matching UTC instant', () => {
  assert.equal(toDateTimeIso('2025-03-14', '16:30', TZ), '2025-03-14T22:30:00.000Z')
  assert.equal(toDateTimeIso('2025-03-14', '00:15', TZ), '2025-03-14T06:15:00.000Z')
  assert.equal(toDateTimeIso('2025-03-14', '16:30', 'UTC'), '2025-03-14T16:30:00.000Z')
  assert.equal(toDateTimeIso('2025-03-14', '11:00', 'America/Bogota'), '2025-03-14T16:00:00.000Z')
})

test('toDateTimeIso rejects an unusable date or time instead of sending NaN to the API', () => {
  assert.throws(() => toDateTimeIso('14/03/2025', '16:30', TZ), TypeError)
  assert.throws(() => toDateTimeIso('2025-03-14', '25:00', TZ), TypeError)
  assert.throws(() => toDateTimeIso('2025-03-14', '16:75', TZ), TypeError)
})

test('addMinutesToIso keeps the calendar of the appointment', () => {
  assert.equal(addMinutesToIso('2025-03-14T22:30:00.000Z', 90), '2025-03-15T00:00:00.000Z')
  assert.equal(addMinutesToIso('2025-03-14T16:00:00Z', 45), '2025-03-14T16:45:00.000Z')
})
