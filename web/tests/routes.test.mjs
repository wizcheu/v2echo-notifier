import { test } from 'node:test'
import assert from 'node:assert/strict'
import { accountHref, parseRoute, pages, activePage } from '../src/routes.ts'
import { readHistoryLocation, historyHref } from '../src/historyLocation.ts'

test('every page round-trips with its account, including settings subpages', () => {
  for (const page of Object.keys(pages)) {
    const id = '测试 account/#?%'
    assert.deepEqual(parseRoute(accountHref(id, page)), { kind: 'workspace', accountID: id, page })
    assert.equal(parseRoute(accountHref(id, page) + '?status=pending').page, page)
  }
  assert.equal(activePage('device'), 'settings')
  assert.equal(activePage('runtime'), 'overview')
})
test('home, global pages and malformed links are distinct', () => {
  for (const hash of ['', '#', '#/']) assert.equal(parseRoute(hash).kind, 'home')
  assert.equal(parseRoute('#/accounts').kind, 'accounts')
  assert.equal(parseRoute('#/accounts/new').kind, 'add')
  for (const hash of ['#/unknown', '#/accounts/x/unknown', '#/accounts/%/settings'])
    assert.equal(parseRoute(hash).kind, 'missing')
})
test('history search, cursor stack and selected event survive a copied URL', () => {
  const state = { query: 'a&b / 回复', status: 'pending', cursors: [0, 100, 70], event: 'test/id' }
  assert.deepEqual(readHistoryLocation(historyHref(accountHref('a', 'pushHistory'), state)), state)
})
test('untrusted cursor and status parameters cannot generate invalid pagination requests', () => {
  assert.deepEqual(readHistoryLocation('#/x?cursors=-1,100&status=invalid').cursors, [0])
  assert.equal(readHistoryLocation('#/x?status=invalid').status, '')
  assert.deepEqual(readHistoryLocation('#/x?cursors=100,101').cursors, [0, 100])
  assert.deepEqual(readHistoryLocation('#/x?cursors=9007199254740992').cursors, [0])
})

test('check records use their own route and restore only valid filter and record values', async () => {
  const { readCheckLocation, checkHistoryHref } = await import('../src/checkHistoryLocation.ts')
  const state = { attention: true, record: 50 }
  const href = checkHistoryHref(accountHref('a', 'checkHistory'), state)
  assert.equal(parseRoute(href).page, 'checkHistory')
  assert.equal(activePage('checkHistory'), 'checkHistory')
  assert.deepEqual(readCheckLocation(href), state)
  for (const raw of ['-1', '0', '1.5', 'abc', '9007199254740992']) {
    assert.equal(readCheckLocation(`#/x?record=${raw}`).record, null)
  }
  assert.deepEqual(readCheckLocation('#/x?filter=unknown'), { attention: false, record: null })
})
