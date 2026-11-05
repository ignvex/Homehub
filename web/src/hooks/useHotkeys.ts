import { useEffect, useRef, useSyncExternalStore } from 'react'

// ============================================================
// 全局键盘快捷键
//
// 整个应用只挂一个 keydown 监听，所有 useHotkeys 调用往同一张表里登记。
// 这样帮助弹窗（HotkeyHelp）能列出「当前页面上真正生效的」快捷键，
// 页面卸载时它的条目随之消失。
//
// 不触发的情况：
//   - 焦点在输入框 / textarea / select / contenteditable 里（用户在打字）
//   - 按着 Ctrl / ⌘ / Alt（那是浏览器和系统的快捷键；Shift 不算，
//     因为 `?` 本身就要 Shift）
//   - 有 <dialog> 打开（弹窗里的按键属于弹窗）
//   - 输入法组字中，或事件已被别处 preventDefault
//
// keys 的写法：单键用 KeyboardEvent.key 的值（"j"、"/"、"?"、"Escape"），
// 序列用空格分隔（"g d" = 先按 g 再按 d，间隔不超过 SEQUENCE_TIMEOUT_MS）。
// 字母不区分大小写。
// ============================================================

export interface Hotkey {
  /** "j"、"?"、"g d" 这样的按键描述。 */
  keys: string
  /** 帮助弹窗里的说明。 */
  description: string
  /** 帮助弹窗里的分组名，缺省为「通用」。 */
  group?: string
  /**
   * 按下时调用。省略表示「只登记说明」：按键由页面自己处理
   * （例如 Ctrl+K 这类带修饰键的组合），这里只负责出现在帮助里。
   */
  handler?: (event: KeyboardEvent) => void
  /** false 时既不触发也不出现在帮助里。 */
  enabled?: boolean
}

/** RegisteredHotkey 是帮助弹窗读取的只读视图。 */
export interface RegisteredHotkey {
  keys: string
  description: string
  group: string
}

interface Entry extends RegisteredHotkey {
  sequence: string[]
  run?: (event: KeyboardEvent) => void
}

const SEQUENCE_TIMEOUT_MS = 1000
export const DEFAULT_HOTKEY_GROUP = '通用'

let entries: Entry[] = []
let snapshot: RegisteredHotkey[] = []
const listeners = new Set<() => void>()
let pending: string[] = []
let pendingTimer: number | undefined

function normalizeKey(key: string): string {
  return key.length === 1 ? key.toLowerCase() : key
}

function parseKeys(keys: string): string[] {
  return keys.trim().split(/\s+/).map(normalizeKey)
}

function publish() {
  snapshot = entries.map(({ keys, description, group }) => ({ keys, description, group }))
  for (const listener of listeners) listener()
}

/** isTypingTarget 判断按键是否落在可输入的控件里。 */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  return target.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"]') !== null
}

function resetPending() {
  pending = []
  window.clearTimeout(pendingTimer)
  pendingTimer = undefined
}

function startsWith(sequence: string[], prefix: string[]): boolean {
  return sequence.length > prefix.length && prefix.every((key, index) => sequence[index] === key)
}

function sameSequence(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((key, index) => key === b[index])
}

function onKeyDown(event: KeyboardEvent) {
  if (event.defaultPrevented || event.isComposing) return
  if (event.ctrlKey || event.metaKey || event.altKey) { resetPending(); return }
  if (isTypingTarget(event.target) || document.querySelector('dialog[open]')) { resetPending(); return }
  // 单独按下的修饰键（例如为了打 ? 先按的 Shift）不打断序列。
  if (event.key === 'Shift' || event.key === 'Control' || event.key === 'Meta' || event.key === 'Alt') return

  const key = normalizeKey(event.key)
  // 后登记的优先：页面的快捷键覆盖外壳的同名快捷键。
  const runnable = entries.filter(entry => entry.run).reverse()

  const attempt = (buffer: string[]): boolean => {
    const exact = runnable.find(entry => sameSequence(entry.sequence, buffer))
    if (exact) {
      // 按住不放的自动重复只对单键有意义（例如 j/k 连续翻条目）。
      if (event.repeat && buffer.length > 1) return true
      resetPending()
      event.preventDefault()
      exact.run?.(event)
      return true
    }
    if (runnable.some(entry => startsWith(entry.sequence, buffer))) {
      pending = buffer
      window.clearTimeout(pendingTimer)
      pendingTimer = window.setTimeout(resetPending, SEQUENCE_TIMEOUT_MS)
      return true
    }
    return false
  }

  if (pending.length && attempt([...pending, key])) return
  resetPending()
  attempt([key])
}

function register(added: Entry[]): () => void {
  if (!entries.length && added.length) document.addEventListener('keydown', onKeyDown)
  entries = [...entries, ...added]
  publish()
  return () => {
    entries = entries.filter(entry => !added.includes(entry))
    if (!entries.length) {
      document.removeEventListener('keydown', onKeyDown)
      resetPending()
    }
    publish()
  }
}

/**
 * useHotkeys 在组件挂载期间登记快捷键。
 *
 * handler 每次渲染都可以是新函数（通过 ref 读最新值），不会导致重复登记；
 * 只有 keys / description / group / enabled 变化时才重新登记。
 */
export function useHotkeys(hotkeys: Hotkey[]) {
  const latest = useRef(hotkeys)
  latest.current = hotkeys
  const signature = JSON.stringify(hotkeys.map(({ keys, description, group, enabled, handler }) => [keys, description, group, enabled, Boolean(handler)]))

  useEffect(() => {
    const added: Entry[] = []
    latest.current.forEach((hotkey, index) => {
      if (hotkey.enabled === false) return
      added.push({
        keys: hotkey.keys,
        description: hotkey.description,
        group: hotkey.group ?? DEFAULT_HOTKEY_GROUP,
        sequence: parseKeys(hotkey.keys),
        run: hotkey.handler ? event => latest.current[index]?.handler?.(event) : undefined,
      })
    })
    if (!added.length) return
    return register(added)
  }, [signature])
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** useRegisteredHotkeys 返回当前登记的全部快捷键，供帮助弹窗渲染。 */
export function useRegisteredHotkeys(): RegisteredHotkey[] {
  return useSyncExternalStore(subscribe, () => snapshot, () => snapshot)
}
