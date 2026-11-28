import test from 'node:test'
import assert from 'node:assert/strict'
import {
  COUNTRIES,
  DEFAULT_COUNTRY_ID,
  digitsOnly,
  displayPhone,
  formatNational,
  phoneCountry,
  phoneHint,
  splitPhone,
  toE164,
  validatePhone,
} from './phone.ts'

test('the country catalogue starts with Cuba and covers the markets of the salon', () => {
  assert.equal(DEFAULT_COUNTRY_ID, 'CU')
  assert.equal(COUNTRIES[0]?.id, 'CU')
  assert.equal(phoneCountry('CU').dialCode, '53')
  const dialCodes = COUNTRIES.map((country) => country.dialCode)
  for (const expected of ['53', '52', '1', '34', '54', '57', '58', '56', '51', '598', '507']) {
    assert.ok(dialCodes.includes(expected), `falta el código ${expected}`)
  }
  assert.ok(COUNTRIES.some((country) => country.id === 'DO'), 'falta República Dominicana')
  assert.equal(phoneCountry('CU').maxDigits, 8)
  assert.equal(phoneCountry('MX').maxDigits, 10)
  assert.equal(phoneCountry('PA').minDigits, 7)
})

test('digitsOnly keeps the numbers and drops the formatting', () => {
  assert.equal(digitsOnly('+53 5552 8595'), '5355528595')
  assert.equal(digitsOnly('(11) 5555-1234'), '1155551234')
  assert.equal(digitsOnly('sin números'), '')
})

test('formatNational groups the digits for reading, never inventing any', () => {
  assert.equal(formatNational('55528595', 'CU'), '5552 8595')
  assert.equal(formatNational('55', 'MX'), '55')
  assert.equal(formatNational('5512345', 'MX'), '55 1234 5')
  assert.equal(formatNational('5512345678', 'MX'), '55 1234 5678')
  assert.equal(formatNational('612345678', 'ES'), '612 345 678')
})

test('toE164 builds the `<dial><digits>` value the API receives', () => {
  assert.equal(toE164({ countryId: 'CU', digits: '55528595' }), '+5355528595')
  assert.equal(toE164({ countryId: 'MX', digits: '5512345678' }), '+525512345678')
  assert.equal(toE164({ countryId: 'UY', digits: '94123456' }), '+59894123456')
  // No digits is no value: the field is empty, not "+53".
  assert.equal(toE164({ countryId: 'CU', digits: '' }), '')
})

test('splitPhone detects the country of a pasted international number', () => {
  assert.deepEqual(splitPhone('+53 5552 8595'), { countryId: 'CU', digits: '55528595' })
  assert.deepEqual(splitPhone('0053 5552 8595'), { countryId: 'CU', digits: '55528595' })
  assert.deepEqual(splitPhone('+52 55 1234 5678'), { countryId: 'MX', digits: '5512345678' })
  assert.deepEqual(splitPhone('+34 612 345 678'), { countryId: 'ES', digits: '612345678' })
  assert.deepEqual(splitPhone('+1 (587) 555-1234'), { countryId: 'US', digits: '5875551234' })
  // +598 is matched before +5-style prefixes: the longest dial code wins.
  assert.deepEqual(splitPhone('+598 94 123 456'), { countryId: 'UY', digits: '94123456' })
  // Without the plus or 00, a number longer than the fallback country is read as
  // an international number when the remainder is a valid national number.
  assert.deepEqual(splitPhone('5355528595', 'CU'), { countryId: 'CU', digits: '55528595' })
  assert.deepEqual(splitPhone('525512345678', 'CU'), { countryId: 'MX', digits: '5512345678' })
})

test('splitPhone falls back to the default country for free-form legacy phones', () => {
  assert.deepEqual(splitPhone('55528595'), { countryId: 'CU', digits: '55528595' })
  assert.deepEqual(splitPhone(''), { countryId: 'CU', digits: '' })
  assert.deepEqual(splitPhone('sin teléfono'), { countryId: 'CU', digits: '' })
  assert.deepEqual(splitPhone('612 345 678', 'ES'), { countryId: 'ES', digits: '612345678' })
  // An unknown prefix keeps the digits instead of throwing them away.
  assert.deepEqual(splitPhone('+999 1234', 'MX'), { countryId: 'MX', digits: '9991234' })
})

test('validatePhone explains the length in Spanish and accepts a valid number', () => {
  assert.equal(validatePhone({ countryId: 'CU', digits: '55528595' }), null)
  assert.equal(validatePhone({ countryId: 'MX', digits: '5512345678' }), null)
  assert.equal(validatePhone({ countryId: 'PA', digits: '6123456' }), null)
  assert.equal(
    validatePhone({ countryId: 'CU', digits: '555285' }),
    'El teléfono de Cuba tiene 8 dígitos (escribiste 6).',
  )
  assert.equal(
    validatePhone({ countryId: 'CU', digits: '5552859512' }),
    'El teléfono de Cuba tiene 8 dígitos (escribiste 10).',
  )
  assert.equal(
    validatePhone({ countryId: 'PA', digits: '612345' }),
    'El teléfono de Panamá debe tener entre 7 y 8 dígitos (escribiste 6).',
  )
  assert.equal(validatePhone({ countryId: 'CU', digits: '' }), 'Escribí el número de teléfono.')
})

test('displayPhone reads an existing value back into code plus number', () => {
  assert.equal(displayPhone('+5355528595'), '+53 5552 8595')
  assert.equal(displayPhone('+52 55 1234 5678'), '+52 55 1234 5678')
  assert.equal(displayPhone('5552 8595'), '+53 5552 8595')
  assert.equal(displayPhone(''), '')
  // A free-form old record that does not fit the default country is shown as it
  // was stored: inventing a prefix would lie about the number.
  assert.equal(displayPhone('55 1234 5678'), '55 1234 5678')
})

test('phoneHint shows an example placeholder per country', () => {
  assert.equal(phoneHint('CU'), 'Ejemplo: 5xxx xxxx · 8 dígitos')
  assert.equal(phoneHint('MX'), 'Ejemplo: 55 xxxx xxxx · 10 dígitos')
  assert.equal(phoneHint('PA'), 'Ejemplo: 6xxx xxxx · entre 7 y 8 dígitos')
})
