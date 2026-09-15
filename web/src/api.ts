export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message)
  }
}
export async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(`/api/${path}`, {
    method,
    credentials: 'same-origin',
    signal: AbortSignal.timeout(30_000),
    headers: { 'Content-Type': 'application/json', 'X-V2Echo-Request': '1' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const result = await response.json()
  if (!response.ok) throw new APIError(result.error ?? '请求失败', response.status)
  return result as T
}
