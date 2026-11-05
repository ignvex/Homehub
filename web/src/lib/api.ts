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

/**
 * downloadFile 取回一个附件响应并存盘。
 *
 * 不直接用 <a href download>：服务端返回 401/500 时，浏览器会把 JSON 错误体
 * 存成一个 .opml / .html 文件，用户看不到失败原因。这里先检查状态码，
 * 失败时按统一响应抛出 ApiRequestError。
 */
export async function downloadFile(path: string, fallbackName: string): Promise<void> {
  let response: Response
  try {
    response = await fetch(`${BASE_URL}${path}`, { credentials: 'include' })
  } catch (cause) {
    if (cause instanceof TypeError) throw new ApiRequestError(0, '网络不可用，请检查连接')
    throw cause
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new ApiRequestError(response.status, body.message || `下载失败 (${response.status})`, body.code)
  }
  const name = /filename="?([^";]+)"?/i.exec(response.headers.get('Content-Disposition') ?? '')?.[1] || fallbackName
  const url = URL.createObjectURL(await response.blob())
  const anchor = document.createElement('a')
  anchor.href = url
  // 带 download 属性的链接会被 App 的客户端导航拦截放行（见 App.tsx）。
  anchor.download = name
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  // 立即撤销在部分浏览器里会中断下载，留一拍再回收。
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** 判断是否是被 AbortController 取消的请求。 */
export function isAbortError(cause: unknown): boolean {
  return cause instanceof DOMException && cause.name === 'AbortError'
}

export { ApiRequestError }
