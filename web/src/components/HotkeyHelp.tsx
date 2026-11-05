import { Fragment } from 'react'
import { Modal } from '@/components/WorkspaceUI'
import { DEFAULT_HOTKEY_GROUP, useRegisteredHotkeys, type RegisteredHotkey } from '@/hooks/useHotkeys'

// ============================================================
// 快捷键帮助弹窗
//
// 内容来自 useHotkeys 的登记表：外壳与当前页面各自登记，这里只负责
// 按分组列出来。页面想在帮助里出现，调用 useHotkeys 即可，不需要改这里。
// ============================================================

/** keyLabel 把按键名换成更好认的符号。 */
function keyLabel(key: string): string {
  switch (key) {
    case 'ArrowUp': return '↑'
    case 'ArrowDown': return '↓'
    case 'ArrowLeft': return '←'
    case 'ArrowRight': return '→'
    case 'Escape': return 'Esc'
    case 'Enter': return '↵'
    case ' ': return 'Space'
    default: return key.length === 1 ? key.toUpperCase() : key
  }
}

function groupHotkeys(hotkeys: RegisteredHotkey[]): [string, RegisteredHotkey[]][] {
  const groups = new Map<string, RegisteredHotkey[]>()
  // 通用分组固定在最前，其余按登记顺序。
  groups.set(DEFAULT_HOTKEY_GROUP, [])
  for (const hotkey of hotkeys) {
    const list = groups.get(hotkey.group) ?? []
    // 同一按键被页面覆盖时，帮助里只留最后登记的那条。
    const existing = list.findIndex(item => item.keys === hotkey.keys)
    if (existing >= 0) list.splice(existing, 1)
    list.push(hotkey)
    groups.set(hotkey.group, list)
  }
  return [...groups].filter(([, list]) => list.length > 0)
}

export function HotkeyHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  const hotkeys = useRegisteredHotkeys()
  return (
    <Modal open={open} title="键盘快捷键" description="在输入框里打字时快捷键不生效。" onClose={onClose} size="small">
      {groupHotkeys(hotkeys).map(([group, list]) => (
        <section key={group} className="hotkey-group">
          <h3>{group}</h3>
          <dl className="hotkey-list">
            {list.map(hotkey => (
              <Fragment key={hotkey.keys}>
                <dt>
                  {hotkey.keys.split(/\s+/).map((key, index) => (
                    <Fragment key={index}>
                      {index > 0 && <span className="hotkey-then">然后</span>}
                      <kbd className="kbd">{keyLabel(key)}</kbd>
                    </Fragment>
                  ))}
                </dt>
                <dd>{hotkey.description}</dd>
              </Fragment>
            ))}
          </dl>
        </section>
      ))}
    </Modal>
  )
}
