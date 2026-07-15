/** API 请求封装：兼容统一响应与裸 JSON。 */
const BASE_URL = '/api/v1'

class ApiRequestError extends Error {
  status: number
  code?: number

  constructor(status: number, message: string, code?: number) {
    super(message)
    this.name = 'ApiRequestError'
    this.status = status
    this.code = code
  }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(`${BASE_URL}${path}`, {
    ...options,
    credentials: 'include',
    headers,
    signal: options.signal,
  })
  const body = await response.json().catch(() => ({}))

  const isAuthProbe = path === '/auth/status'
  if (response.status === 401 && window.location.pathname !== '/login' && !isAuthProbe) {
    const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`
    window.location.assign(`/login?returnTo=${encodeURIComponent(returnTo)}`)
    throw new ApiRequestError(401, '登录状态已失效，请重新登录')
  }

  if (!response.ok) {
    throw new ApiRequestError(
      response.status,
      body.message || body.error || `请求失败 (${response.status})`, body.code,
    )
  }

  if (body && typeof body === 'object' && 'code' in body && 'message' in body) {
    if (body.code !== 0) {
      throw new ApiRequestError(response.status, body.message || '请求失败')
    }
    return body.data as T
  }

  return body as T
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, data?: unknown) =>
    request<T>(path, {
      method: 'POST',
      body: data instanceof FormData ? data : data ? JSON.stringify(data) : undefined,
    }),
  put: <T>(path: string, data?: unknown) =>
    request<T>(path, {
      method: 'PUT',
      body: data instanceof FormData ? data : data ? JSON.stringify(data) : undefined,
    }),
  patch: <T>(path: string, data?: unknown) =>
    request<T>(path, {
      method: 'PATCH',
      body: data instanceof FormData ? data : data ? JSON.stringify(data) : undefined,
    }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}

export { ApiRequestError }
