import { useRef } from 'react'
import { Modal } from '@/components/WorkspaceUI'

// ============================================================
// 文件导入：选择文件的按钮 + 导入结果弹窗
//
// OPML 与书签导入共用：两者都是「选一个文件 → 上传 → 看新建/跳过了多少」。
// ============================================================

export interface ImportSummary {
  created: number
  skipped: number
  groups_created: number
  /** 逐条失败的明细；书签导入没有这一项。 */
  errors?: { url: string; reason: string }[] | null
}

/** ImportFileButton 是一个触发隐藏文件框的按钮。选中后回调并清空，以便再次选同一文件。 */
export function ImportFileButton({ label, accept, busy, className = 'button button--quiet', onFile }: {
  label: string
  accept: string
  busy?: boolean
  className?: string
  onFile: (file: File) => void
}) {
  const input = useRef<HTMLInputElement>(null)
  return <>
    <input
      ref={input}
      className="visually-hidden"
      type="file"
      accept={accept}
      tabIndex={-1}
      aria-hidden="true"
      onChange={event => {
        const file = event.target.files?.[0]
        event.target.value = ''
        if (file) onFile(file)
      }}
    />
    <button type="button" className={className} disabled={busy} onClick={() => input.current?.click()}>
      {busy ? '导入中…' : label}
    </button>
  </>
}

/** ImportResultModal 展示一次导入的汇总；result 为 null 时关闭。 */
export function ImportResultModal({ title, result, unit, onClose }: {
  title: string
  result: ImportSummary | null
  /** 被导入对象的量词，如「个订阅源」「个书签」。 */
  unit: string
  onClose: () => void
}) {
  const errors = result?.errors ?? []
  return <Modal
    open={result !== null}
    title={title}
    size="small"
    onClose={onClose}
    footer={<button type="button" className="button button--primary" onClick={onClose}>完成</button>}
  >
    {result && <>
      <dl className="import-summary">
        <dt>新建</dt><dd>{result.created} {unit}</dd>
        <dt>跳过（已存在或无效）</dt><dd>{result.skipped} {unit}</dd>
        <dt>新建分组</dt><dd>{result.groups_created} 个</dd>
        {result.errors !== undefined && <><dt>失败</dt><dd>{errors.length} {unit}</dd></>}
      </dl>
      {errors.length > 0 && <ul className="import-errors" aria-label="失败明细">
        {errors.map((item, index) => <li key={`${item.url}-${index}`}>
          <code>{item.url || '（缺少 URL）'}</code>
          <span>{item.reason}</span>
        </li>)}
      </ul>}
    </>}
  </Modal>
}
