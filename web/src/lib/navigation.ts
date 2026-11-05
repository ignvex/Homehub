import { createContext, useContext, useEffect } from 'react'

// ============================================================
// 客户端导航 — 无路由库，只有 history.pushState 与一个小 context。
// ============================================================

/** pushState / replaceState 之后派发，让依赖 URL 的 hook（useQueryState）重读。 */
export const LOCATION_CHANGE_EVENT = 'homehub:locationchange'

export interface AppLocation { pathname: string; search: string; hash: string }

export interface NavigateOptions { replace?: boolean }

export interface NavigationValue {
  location: AppLocation
  navigate: (href: string, options?: NavigateOptions) => void
}

export function readLocation(): AppLocation {
  return {
    pathname: window.location.pathname.replace(/\/$/, '') || '/',
    search: window.location.search,
    hash: window.location.hash,
  }
}

export function currentHref(): string {
  return `${window.location.pathname}${window.location.search}${window.location.hash}`
}

export function notifyLocationChange() {
  window.dispatchEvent(new Event(LOCATION_CHANGE_EVENT))
}

export const NavigationContext = createContext<NavigationValue>({
  location: readLocation(),
  // 没有 Provider 时退化为整页跳转
  navigate: href => window.location.assign(href),
})

export function useLocation(): AppLocation {
  return useContext(NavigationContext).location
}

export function useNavigate(): NavigationValue['navigate'] {
  return useContext(NavigationContext).navigate
}

// ============================================================
// 离开守卫：页面注册「是否有未保存更改」的检查，客户端导航前统一确认。
// ============================================================
type Guard = () => string | null
const guards = new Set<Guard>()

/** 依次询问已注册的守卫；任一守卫有未保存内容且用户取消时返回 false。 */
export function confirmLeave(): boolean {
  for (const guard of guards) {
    const message = guard()
    if (message && !window.confirm(message)) return false
  }
  return true
}

export const DEFAULT_LEAVE_MESSAGE = '有未保存的更改，确定要离开吗？'

/**
 * dirty 时注册离开守卫：客户端导航前 confirm，刷新/关闭标签页时触发浏览器的 beforeunload 提示。
 */
export function useLeaveGuard(dirty: boolean, message = DEFAULT_LEAVE_MESSAGE) {
  useEffect(() => {
    if (!dirty) return
    const guard: Guard = () => message
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    guards.add(guard)
    window.addEventListener('beforeunload', beforeUnload)
    return () => {
      guards.delete(guard)
      window.removeEventListener('beforeunload', beforeUnload)
    }
  }, [dirty, message])
}
