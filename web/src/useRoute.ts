import { useSyncExternalStore } from 'react'
import { parseRoute } from './routes'
function subscribe(callback: () => void) {
  window.addEventListener('hashchange', callback)
  return () => window.removeEventListener('hashchange', callback)
}
export function useRoute() {
  const hash = useSyncExternalStore(
    subscribe,
    () => window.location.hash,
    () => '',
  )
  return { route: parseRoute(hash), hash }
}
