export function readCheckLocation(hash: string) {
  const params = new URLSearchParams(hash.split('?')[1] || '')
  const id = Number(params.get('record'))
  return {
    attention: params.get('filter') === 'attention',
    record: Number.isSafeInteger(id) && id > 0 ? id : null,
  }
}

export function checkHistoryHref(base: string, state: ReturnType<typeof readCheckLocation>) {
  const params = new URLSearchParams()
  if (state.attention) params.set('filter', 'attention')
  if (state.record) params.set('record', String(state.record))
  return base.split('?')[0] + (params.size ? `?${params}` : '')
}
