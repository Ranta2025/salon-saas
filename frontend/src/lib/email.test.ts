import test from 'node:test'
import assert from 'node:assert/strict'
import {
  EMAIL_INVALID_MESSAGE,
  isEmailErrorMessage,
  isValidEmail,
  normalizeEmail,
} from './email.ts'

test('normalizeEmail lowercases and trims like the server', () => {
  assert.equal(normalizeEmail('  Lia@Mail.com '), 'lia@mail.com')
  assert.equal(normalizeEmail(''), '')
})

test('isValidEmail applies the same rule as the API and accepts an empty optional email', () => {
  assert.equal(isValidEmail('lia@mail.com'), true)
  assert.equal(isValidEmail(' Lia@Mail.com '), true)
  assert.equal(isValidEmail(''), true)
  assert.equal(isValidEmail('   '), true)
  assert.equal(isValidEmail('lia@'), false)
  assert.equal(isValidEmail('lia.mail.com'), false)
  assert.equal(isValidEmail('a@b'), false)
  assert.equal(isValidEmail('a b@mail.com'), false)
  assert.equal(isValidEmail(`${'a'.repeat(250)}@mail.com`), false)
})

test('EMAIL_INVALID_MESSAGE is the exact message the server returns', () => {
  assert.equal(EMAIL_INVALID_MESSAGE, 'Email inválido')
  assert.equal(isEmailErrorMessage('Email inválido'), true)
  assert.equal(isEmailErrorMessage(' email inválido '), true)
  assert.equal(isEmailErrorMessage('Ese horario se acaba de ocupar'), false)
})
