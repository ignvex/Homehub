import { useCallback, useEffect, useState } from 'react'

/** 客户端导航（pushState）后由 App 派发，让各 hook 重读 URL。 */
export const LOCATION_CHANGE_EVENT = 'homehub:locationchange'

function read(key: string, fallback: string): string {
  return new URLSearchParams(window.location.search).get(key) ?? fallback
}

/**
 * 把一个字符串状态同步到 URL 查询参数。
 *
 * - 写入用 history.replaceState，不新增历史条目
 * - 值等于 defaultValue 时从 URL 中移除该参数，保持地址简洁
 * - popstate（前进/后退）与客户端导航时重新读取
 */
export function useQueryState(key: string, defaultValue = ''): [string, (value: string) => void] {
  const [value, setValue] = useState(() => read(key, defaultValue))

  useEffect(() => {
    const sync = () => setValue(read(key, defaultValue))
    window.addEventListener('popstate', sync)
    window.addEventListener(LOCATION_CHANGE_EVENT, sync)
    return () => {
      window.removeEventListener('popstate', sync)
      window.removeEventListener(LOCATION_CHANGE_EVENT, sync)
    }
  }, [key, defaultValue])

  const update = useCallback((next: string) => {
    setValue(next)
    const url = new URL(window.location.href)
    if (next === defaultValue || next === '') url.searchParams.delete(key)
    else url.searchParams.set(key, next)
    const target = `${url.pathname}${url.search}${url.hash}`
    if (target !== `${window.location.pathname}${window.location.search}${window.location.hash}`) {
      history.replaceState(history.state, '', target)
    }
  }, [key, defaultValue])

  return [value, update]
}

/** 数字版本：URL 中的非法值回退到 defaultValue。 */
export function useQueryNumber(key: string, defaultValue: number): [number, (value: number) => void] {
  const [raw, setRaw] = useQueryState(key, String(defaultValue))
  const parsed = Number.parseInt(raw, 10)
  const value = Number.isFinite(parsed) && parsed > 0 ? parsed : defaultValue
  const update = useCallback((next: number) => setRaw(String(next)), [setRaw])
  return [value, update]
}
