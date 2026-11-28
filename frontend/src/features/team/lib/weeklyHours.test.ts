import test from 'node:test'
import assert from 'node:assert/strict'
import {
  addSegment,
  beginSave,
  breakLabel,
  commitSave,
  copyDraftDay,
  copySegmentsDay,
  copyTargets,
  dayBreaks,
  daySummary,
  draftToDays,
  followsSalon,
  formatClock,
  formatClockShort,
  isClock,
  MAX_SEGMENTS_PER_DAY,
  normalizeMemberHours,
  normalizeSalonHours,
  parseClock,
  publicHoursRows,
  removeSegment,
  rollbackSave,
  sameSegments,
  salonSegments,
  segmentsForDay,
  segmentsSummary,
  setDaySegments,
  setDraftDay,
  todayWeekday,
  validateDraft,
  validateSegments,
  WEEKDAY_LABELS,
  WEEKDAY_SHORT,
  WEEKDAYS,
  weekDraft,
  weekSummary,
  type DaySegment,
} from './weeklyHours.ts'

const DEFAULTS = { open: '09:00', close: '19:00' }

const workweekSegments: DaySegment[] = WEEKDAYS.filter((day) => day <= 5).flatMap((weekday) => [
  { weekday, start: '09:00', end: '13:00' },
  { weekday, start: '14:00', end: '19:00' },
])

test('clocks are read and written as strict 24h HH:MM', () => {
  assert.equal(parseClock('09:00'), 540)
  assert.equal(parseClock('00:00'), 0)
  assert.equal(parseClock('23:59'), 1439)
  assert.equal(parseClock('9:00'), null)
  assert.equal(parseClock('24:00'), null)
  assert.equal(parseClock('09:60'), null)
  assert.equal(parseClock(''), null)
  assert.equal(isClock('13:05'), true)
  assert.equal(isClock('9:00'), false)
  assert.equal(formatClock(540), '09:00')
  assert.equal(formatClock(9 * 60 + 5), '09:05')
  assert.equal(formatClock(0), '00:00')
  assert.equal(formatClock(1439), '23:59')
  assert.equal(formatClockShort(540), '9:00')
  assert.equal(formatClockShort(13 * 60 + 5), '13:05')
})

test('weekDraft expands seven rows and keeps the settings window on closed days', () => {
  const draft = weekDraft([{ weekday: 1, open: '09:00', close: '19:00' }], DEFAULTS)
  assert.equal(draft.length, 7)
  assert.deepEqual(draft[0], { weekday: 1, open: '09:00', close: '19:00', closed: false })
  assert.deepEqual(draft[1], { weekday: 2, open: '09:00', close: '19:00', closed: true })
  assert.deepEqual(draftToDays(draft), [{ weekday: 1, open: '09:00', close: '19:00' }])
  assert.deepEqual(
    draftToDays(weekDraft([], { open: '08:30', close: '20:00' })),
    [],
    'no rows means the single window of the settings page',
  )
})

test('validateDraft rejects a closing time that is not after the opening one', () => {
  const draft = setDraftDay(weekDraft([], DEFAULTS), 2, { closed: false, close: '09:00' })
  assert.deepEqual(validateDraft(draft), {
    ok: false,
    weekday: 2,
    message: 'Horario inválido: el inicio debe ser anterior al fin',
  })

  const broken = setDraftDay(weekDraft([], DEFAULTS), 3, { closed: false, open: '9:00' })
  assert.deepEqual(validateDraft(broken), {
    ok: false,
    weekday: 3,
    message: 'Escribí la hora como HH:MM (por ejemplo 09:30)',
  })

  const valid = setDraftDay(weekDraft([], DEFAULTS), 1, { closed: false, close: '18:00' })
  assert.deepEqual(validateDraft(valid), {
    ok: true,
    value: [{ weekday: 1, open: '09:00', close: '18:00' }],
  })
})

test('weekSummary of the week groups equal consecutive days and marks closed ones', () => {
  const days = [
    { weekday: 1, open: '09:00', close: '19:00' },
    { weekday: 2, open: '09:00', close: '19:00' },
    { weekday: 3, open: '09:00', close: '19:00' },
    { weekday: 4, open: '09:00', close: '19:00' },
    { weekday: 5, open: '09:00', close: '19:00' },
    { weekday: 6, open: '09:00', close: '14:00' },
  ]
  assert.equal(
    weekSummary(days),
    'Lun a vie 9:00–19:00 · Sáb 9:00–14:00 · Dom cerrado',
  )
  assert.equal(
    weekSummary(WEEKDAYS.map((weekday) => ({ weekday, open: '09:00', close: '20:00' }))),
    'Lun a dom 9:00–20:00',
  )
  assert.equal(weekSummary([]), 'Lun a dom cerrado')
})

test('copyDraftDay copies one day to the chosen targets only', () => {
  const draft = weekDraft([{ weekday: 1, open: '10:00', close: '18:00' }], DEFAULTS)
  const copied = copyDraftDay(draft, 1, [2, 3, 4, 5])
  for (const weekday of [1, 2, 3, 4, 5]) {
    assert.deepEqual(copied[weekday - 1], {
      weekday,
      open: '10:00',
      close: '18:00',
      closed: false,
    })
  }
  assert.deepEqual(copied[5], { weekday: 6, open: '09:00', close: '19:00', closed: true })
  assert.deepEqual(copied[6], { weekday: 7, open: '09:00', close: '19:00', closed: true })

  const closedSource = copyDraftDay(draft, 6, [7])
  assert.deepEqual(closedSource[6], { weekday: 7, open: '09:00', close: '19:00', closed: true })
})

test('validateSegments mirrors the server sentences', () => {
  assert.deepEqual(validateSegments([{ weekday: 1, start: '10:00', end: '09:00' }]), {
    ok: false,
    weekday: 1,
    message: 'Horario inválido: el inicio debe ser anterior al fin',
  })
  assert.deepEqual(validateSegments([{ weekday: 1, start: '10:00', end: '10:00' }]), {
    ok: false,
    weekday: 1,
    message: 'Horario inválido: el inicio debe ser anterior al fin',
  })
  assert.deepEqual(
    validateSegments([
      { weekday: 1, start: '09:00', end: '13:00' },
      { weekday: 1, start: '12:00', end: '15:00' },
    ]),
    { ok: false, weekday: 1, message: 'Los tramos de un mismo día no pueden solaparse' },
  )
  assert.deepEqual(
    validateSegments([
      { weekday: 2, start: '09:00', end: '13:00' },
      { weekday: 2, start: '09:00', end: '13:00' },
    ]),
    { ok: false, weekday: 2, message: 'Los tramos de un mismo día no pueden solaparse' },
  )
  assert.deepEqual(
    validateSegments(
      Array.from({ length: MAX_SEGMENTS_PER_DAY + 1 }, (_, index) => ({
        weekday: 3,
        start: formatClock(8 * 60 + index * 60),
        end: formatClock(8 * 60 + index * 60 + 30),
      })),
    ),
    { ok: false, weekday: 3, message: 'Máximo 4 tramos por día' },
  )
  assert.deepEqual(validateSegments([{ weekday: 0, start: '09:00', end: '13:00' }]), {
    ok: false,
    weekday: 0,
    message: 'Día inválido',
  })
  assert.deepEqual(validateSegments([{ weekday: 1, start: '9:00', end: '13:00' }]), {
    ok: false,
    weekday: 1,
    message: 'Escribí la hora como HH:MM (por ejemplo 09:30)',
  })
  assert.deepEqual(
    validateSegments([
      { weekday: 1, start: '14:00', end: '19:00' },
      { weekday: 1, start: '09:00', end: '13:00' },
    ]),
    {
      ok: true,
      value: [
        { weekday: 1, start: '09:00', end: '13:00' },
        { weekday: 1, start: '14:00', end: '19:00' },
      ],
    },
  )
})

test('copySegmentsDay replaces the targets with a copy of the source day', () => {
  const segments: DaySegment[] = [
    { weekday: 1, start: '09:00', end: '13:00' },
    { weekday: 1, start: '14:00', end: '19:00' },
    { weekday: 4, start: '10:00', end: '12:00' },
  ]
  const copied = copySegmentsDay(segments, 1, [2, 3])
  assert.deepEqual(copied, [
    { weekday: 1, start: '09:00', end: '13:00' },
    { weekday: 1, start: '14:00', end: '19:00' },
    { weekday: 2, start: '09:00', end: '13:00' },
    { weekday: 2, start: '14:00', end: '19:00' },
    { weekday: 3, start: '09:00', end: '13:00' },
    { weekday: 3, start: '14:00', end: '19:00' },
    { weekday: 4, start: '10:00', end: '12:00' },
  ])
  assert.deepEqual(copySegmentsDay(segments, 5, [1]), [
    { weekday: 4, start: '10:00', end: '12:00' },
  ])
})

test('copyTargets offers whole-week, workweek, weekend and single days', () => {
  const monday = copyTargets(1)
  const all = monday.find((target) => target.value === 'all')
  assert.deepEqual(all?.weekdays, [2, 3, 4, 5, 6, 7])
  const workweek = monday.find((target) => target.value === 'weekdays')
  assert.deepEqual(workweek?.weekdays, [2, 3, 4, 5])
  assert.equal(workweek?.label, 'Lunes a viernes')
  const weekend = monday.find((target) => target.value === 'weekend')
  assert.deepEqual(weekend?.weekdays, [6, 7])
  assert.deepEqual(
    monday.filter((target) => target.value !== 'all' && target.value !== 'weekdays' && target.value !== 'weekend').map((target) => target.value),
    ['2', '3', '4', '5', '6', '7'],
  )
  assert.deepEqual(copyTargets(6).find((target) => target.value === 'weekend')?.weekdays, [7])
})

test('setDaySegments, addSegment and removeSegment edit one day at a time', () => {
  const base: DaySegment[] = [{ weekday: 1, start: '09:00', end: '13:00' }]
  assert.deepEqual(setDaySegments(base, 1, [{ start: '10:00', end: '11:00' }]), [
    { weekday: 1, start: '10:00', end: '11:00' },
  ])
  assert.deepEqual(addSegment([], 1, { start: '09:00', end: '13:00' }), [
    { weekday: 1, start: '09:00', end: '13:00' },
  ])
  const four = Array.from({ length: MAX_SEGMENTS_PER_DAY }, (_, index) => ({
    weekday: 2,
    start: formatClock(8 * 60 + index * 60),
    end: formatClock(8 * 60 + index * 60 + 30),
  }))
  assert.equal(addSegment(four, 2, { start: '20:00', end: '21:00' }).length, 4)
  assert.deepEqual(removeSegment(base, 1, 0), [])
  assert.deepEqual(segmentsForDay(base, 1), [{ weekday: 1, start: '09:00', end: '13:00' }])
  assert.deepEqual(segmentsForDay(base, 2), [])
})

test('the gaps between segments of a day are its breaks', () => {
  const segments: DaySegment[] = [
    { weekday: 1, start: '09:00', end: '12:00' },
    { weekday: 1, start: '13:00', end: '15:00' },
    { weekday: 1, start: '16:00', end: '18:00' },
  ]
  assert.deepEqual(dayBreaks(segments, 1), [
    { start: '12:00', end: '13:00' },
    { start: '15:00', end: '16:00' },
  ])
  assert.deepEqual(dayBreaks([{ weekday: 1, start: '09:00', end: '19:00' }], 1), [])
  assert.deepEqual(dayBreaks([{ weekday: 2, start: '09:00', end: '19:00' }], 1), [])
  assert.equal(breakLabel({ start: '13:00', end: '14:00' }), 'Descanso 13:00–14:00')
  assert.equal(
    daySummary([{ weekday: 1, start: '09:00', end: '13:00' }], 1),
    '9:00–13:00',
  )
  assert.equal(daySummary([], 2), 'Día libre')
})

test('segmentsSummary groups equal consecutive days and calls free days "libre"', () => {
  const segments: DaySegment[] = [
    ...workweekSegments,
    { weekday: 7, start: '10:00', end: '14:00' },
  ]
  assert.equal(
    segmentsSummary(segments),
    'Lun a vie 9:00–13:00, 14:00–19:00 · Sáb libre · Dom 10:00–14:00',
  )
  assert.equal(segmentsSummary([]), 'Lun a dom libre')
})

test('no segments means the professional follows the salon', () => {
  assert.equal(followsSalon([]), true)
  assert.equal(followsSalon([{ weekday: 1, start: '09:00', end: '13:00' }]), false)
  assert.deepEqual(salonSegments([{ weekday: 1, open: '09:00', close: '19:00' }]), [
    { weekday: 1, start: '09:00', end: '19:00' },
  ])
  assert.equal(sameSegments([], []), true)
  assert.equal(
    sameSegments(
      [{ weekday: 1, start: '09:00', end: '13:00' }],
      [{ weekday: 1, start: '09:00', end: '13:00' }],
    ),
    true,
  )
  assert.equal(
    sameSegments(
      [{ weekday: 1, start: '09:00', end: '13:00' }],
      [{ weekday: 1, start: '09:00', end: '14:00' }],
    ),
    false,
  )
})

test('todayWeekday reads the weekday in the salon zone, not in UTC', () => {
  // 2026-03-10T02:30Z is still Monday 20:30 in Mexico City.
  const nearMidnight = new Date('2026-03-10T02:30:00Z')
  assert.equal(todayWeekday(nearMidnight, 'UTC'), 2)
  assert.equal(todayWeekday(nearMidnight, 'America/Mexico_City'), 1)
  assert.equal(todayWeekday(new Date('2026-03-09T12:00:00Z'), 'UTC'), 1)
  assert.equal(todayWeekday(new Date('2026-03-15T23:00:00Z'), 'UTC'), 7)
  // An unusable zone falls back to UTC instead of throwing.
  assert.equal(todayWeekday(nearMidnight, 'Not/AZone'), 2)
})

test('publicHoursRows prints every day with "Cerrado" and marks today', () => {
  const rows = publicHoursRows(
    [
      { weekday: 1, open: '09:00', close: '19:00' },
      { weekday: 2, open: '09:00', close: '19:00' },
      { weekday: 3, open: '09:00', close: '19:00' },
      { weekday: 4, open: '09:00', close: '19:00' },
      { weekday: 5, open: '09:00', close: '19:00' },
      { weekday: 6, open: '09:00', close: '14:00' },
    ],
    6,
  )
  assert.equal(rows.length, 7)
  assert.deepEqual(rows[0], {
    weekday: 1,
    label: 'Lunes',
    time: '9:00 – 19:00',
    closed: false,
    isToday: false,
  })
  assert.deepEqual(rows[5], {
    weekday: 6,
    label: 'Sábado',
    time: '9:00 – 14:00',
    closed: false,
    isToday: true,
  })
  assert.deepEqual(rows[6], {
    weekday: 7,
    label: 'Domingo',
    time: 'Cerrado',
    closed: true,
    isToday: false,
  })
  assert.equal(WEEKDAY_LABELS[7], 'Domingo')
  assert.equal(WEEKDAY_SHORT[3], 'Mié')
})

test('normalizeSalonHours keeps usable rows and reads the custom flag', () => {
  const hours = normalizeSalonHours({
    days: [
      { weekday: 2, open: '10:00', close: '18:00' },
      { weekday: 1, open: '09:00', close: '19:00' },
      { weekday: 9, open: '09:00', close: '19:00' },
      { weekday: 3, open: '19:00', close: '09:00' },
      { weekday: 4, open: '9:00', close: '19:00' },
    ],
    custom: true,
  })
  assert.deepEqual(hours, {
    days: [
      { weekday: 1, open: '09:00', close: '19:00' },
      { weekday: 2, open: '10:00', close: '18:00' },
    ],
    custom: true,
  })
  assert.deepEqual(normalizeSalonHours({ days: [{ weekday: 1, open: '09:00', close: '19:00' }] }), {
    days: [{ weekday: 1, open: '09:00', close: '19:00' }],
    custom: false,
  })
  assert.deepEqual(normalizeSalonHours(null), { days: [], custom: false })
  assert.deepEqual(normalizeSalonHours({ days: 'nope' }), { days: [], custom: false })
})

test('normalizeMemberHours derives "custom" from the rows, never from the flag', () => {
  const hours = normalizeMemberHours({
    segments: [
      { weekday: 1, start: '09:00', end: '13:00' },
      { weekday: 1, start: '13:00', end: '17:00' },
      { weekday: 8, start: '09:00', end: '13:00' },
      { weekday: 2, start: '10:00', end: '10:00' },
    ],
    custom: false,
  })
  assert.deepEqual(hours, {
    segments: [
      { weekday: 1, start: '09:00', end: '13:00' },
      { weekday: 1, start: '13:00', end: '17:00' },
    ],
    custom: true,
  })
  assert.deepEqual(normalizeMemberHours({ segments: [], custom: true }), {
    segments: [],
    custom: false,
  })
  assert.deepEqual(normalizeMemberHours({ segments: 'nope' }), { segments: [], custom: false })
})

test('the optimistic save keeps the previous value so it can roll back', () => {
  const started = beginSave(['saved'], ['draft'])
  assert.deepEqual(started, { value: ['draft'], saved: ['saved'] })
  assert.deepEqual(rollbackSave(started), { value: ['saved'], saved: ['saved'] })
  assert.deepEqual(commitSave(started, ['server']), { value: ['server'], saved: ['server'] })
  assert.deepEqual(started, { value: ['draft'], saved: ['saved'] }, 'the input is not mutated')

  const list = [{ weekday: 1, start: '09:00', end: '13:00' }]
  const fresh = beginSave([], list)
  assert.deepEqual(fresh, { value: list, saved: [] })
  assert.deepEqual(commitSave(fresh, list), { value: list, saved: list })
})
