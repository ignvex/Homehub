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

  let response: Response
  try {
    response = await fetch(`${BASE_URL}${path}`, {
      ...options,
      credentials: 'include',
      headers,
    })
  } catch (cause) {
    // 取消请求要原样抛出，调用方据此识别 AbortError 并忽略。
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause
    if (cause instanceof TypeError) throw new ApiRequestError(0, '网络不可用，请检查连接')
    throw cause
  }
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

function encodeBody(data: unknown): BodyInit | undefined {
  return data instanceof FormData ? data : data ? JSON.stringify(data) : undefined
}

/** 每个方法都接受可选的 RequestInit（如 signal），透传给 fetch。 */
export const api = {
  get: <T>(path: string, init: RequestInit = {}) => request<T>(path, init),
  post: <T>(path: string, data?: unknown, init: RequestInit = {}) =>
    request<T>(path, { ...init, method: 'POST', body: encodeBody(data) }),
  put: <T>(path: string, data?: unknown, init: RequestInit = {}) =>
    request<T>(path, { ...init, method: 'PUT', body: encodeBody(data) }),
  patch: <T>(path: string, data?: unknown, init: RequestInit = {}) =>
    request<T>(path, { ...init, method: 'PATCH', body: encodeBody(data) }),
  delete: <T>(path: string, init: RequestInit = {}) => request<T>(path, { ...init, method: 'DELETE' }),
}

/** 判断是否是被 AbortController 取消的请求。 */
export function isAbortError(cause: unknown): boolean {
  return cause instanceof DOMException && cause.name === 'AbortError'
}

export { ApiRequestError }
