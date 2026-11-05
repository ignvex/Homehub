// HomeHub Service Worker（手写，不依赖构建插件）
//
// 只做两件事：
//   - 导航请求 network-first，断网时回落到缓存的 index.html（离线壳）
//   - /assets/* 是 vite 产出的带哈希文件，内容不会变，cache-first
//
// 其余请求一律不拦截，直接走网络。尤其是 /api/、/static/uploads/、
// /swagger/ 与任何非 GET 请求：数据必须实时，而且响应跟登录态绑定，
// 缓存下来会在换账号或登出后把别人的数据端出来。
//
// 改动本文件的缓存策略时递增 VERSION：activate 会删除所有旧版本缓存。
const VERSION = 'v1'
const SHELL_CACHE = `homehub-shell-${VERSION}`
const ASSET_CACHE = `homehub-assets-${VERSION}`
// 用 "/" 而不是 "/index.html"：后端用 http.ServeFile 提供文件，它会把以
// /index.html 结尾的请求 301 到 "./"；被重定向过的响应不能拿来回应导航请求。
const SHELL_URL = '/'
// 每次部署都会产出一批新哈希文件；只保留最近这么多条，避免缓存无限增长。
const MAX_ASSET_ENTRIES = 80

const NEVER_CACHE_PREFIXES = ['/api/', '/static/uploads/', '/swagger/']

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(SHELL_CACHE)
      .then(cache => cache.add(new Request(SHELL_URL, { cache: 'no-cache' })))
      .catch(() => undefined)
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', event => {
  const keep = new Set([SHELL_CACHE, ASSET_CACHE])
  event.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(key => key.startsWith('homehub-') && !keep.has(key)).map(key => caches.delete(key))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', event => {
  const { request } = event
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  if (NEVER_CACHE_PREFIXES.some(prefix => url.pathname.startsWith(prefix))) return

  if (request.mode === 'navigate') {
    event.respondWith(networkFirstShell(request))
    return
  }
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(cacheFirstAsset(request))
  }
})

async function networkFirstShell(request) {
  try {
    const response = await fetch(request)
    // 所有 SPA 路由返回的都是同一份 index.html，存成一条即可。
    const type = response.headers.get('Content-Type') || ''
    if (response.ok && !response.redirected && type.includes('text/html')) {
      const cache = await caches.open(SHELL_CACHE)
      await cache.put(SHELL_URL, response.clone())
    }
    return response
  } catch (error) {
    const cached = await caches.match(SHELL_URL, { cacheName: SHELL_CACHE })
    if (cached) return cached
    throw error
  }
}

async function cacheFirstAsset(request) {
  const cache = await caches.open(ASSET_CACHE)
  const cached = await cache.match(request)
  if (cached) return cached
  const response = await fetch(request)
  if (response.ok) {
    await cache.put(request, response.clone())
    void trimCache(cache)
  }
  return response
}

async function trimCache(cache) {
  const keys = await cache.keys()
  // keys() 按插入顺序返回，先删最早的。
  for (const key of keys.slice(0, Math.max(0, keys.length - MAX_ASSET_ENTRIES))) {
    await cache.delete(key)
  }
}
