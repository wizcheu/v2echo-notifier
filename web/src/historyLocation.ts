const statuses = ['pending', 'apns_accepted', 'rejected', 'blocked', 'expired', 'skipped']
export function readHistoryLocation(hash: string) {
  const params = new URLSearchParams(hash.split('?')[1] || '')
  const status = params.get('status') || ''
  const raw = (params.get('cursors') || '').split(',').map(Number)
  const cursors = [0]
  for (const cursor of raw.slice(0, 100)) {
    if (!Number.isSafeInteger(cursor) || cursor <= 0 || (cursors.length > 1 && cursor >= cursors[cursors.length - 1]))
      break
    cursors.push(cursor)
  }
  return {
    query: (params.get('q') || '').slice(0, 200),
    status: statuses.includes(status) ? status : '',
    cursors,
    event: params.get('event') || '',
  }
}
export function historyHref(base: string, state: ReturnType<typeof readHistoryLocation>) {
  const params = new URLSearchParams()
  if (state.query) params.set('q', state.query)
  if (state.status) params.set('status', state.status)
  if (state.cursors.length > 1) params.set('cursors', state.cursors.slice(1).join(','))
  if (state.event) params.set('event', state.event)
  return base.split('?')[0] + (params.size ? `?${params}` : '')
}
