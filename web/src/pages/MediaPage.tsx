import { useEffect, useRef, useState } from 'react'
import { CopyIcon, DeleteButton } from '@/components/Icon'
import { ConfirmDialog, PageState } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useAuth } from '@/lib/auth'
import { mediaURL } from '@/lib/media'
import { UPLOAD_ACCEPT, mediaAssetsApi, type MediaAsset } from '@/lib/mediaAssets'
import { CONTENT_WRITE_LEVEL, MEDIA_WRITE_LEVEL, hasLevel } from '@/lib/permissions'
import { formatDateTime, formatFileSize } from '@/lib/types'

export function MediaPage() {
  const { auth } = useAuth()
  const canUpload = hasLevel(auth, MEDIA_WRITE_LEVEL)
  const canDeleteAny = hasLevel(auth, CONTENT_WRITE_LEVEL)
  // 4 级只能删除自己上传的；历史数据（owner_id=0）只有 1–3 级能动。
  const canDelete = (item: MediaAsset) => canDeleteAny || (canUpload && !!item.owner_id && item.owner_id === auth?.id)
  const notice = useNotice()
  const [items, setItems] = useState<MediaAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [uploading, setUploading] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<MediaAsset | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const input = useRef<HTMLInputElement>(null)

  async function load(silent = false) {
    if (!silent) setLoading(true)
    try { setItems(await mediaAssetsApi.list()); setLoadError('') }
    catch (cause) { const message = errorMessage(cause, '无法读取媒体资源'); if (silent) notice.error(message); else setLoadError(message) }
    finally { setLoading(false) }
  }
  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void load() }, [])

  async function upload(files: FileList | null) {
    if (!files?.length) return
    setUploading(true)
    let success = 0
    try {
      for (const file of Array.from(files)) { await mediaAssetsApi.upload(file); success++ }
      notice.success(success === 1 ? '媒体已上传' : `已上传 ${success} 个媒体文件`)
    } catch (cause) {
      const message = errorMessage(cause, '上传失败')
      notice.error(success ? `已上传 ${success} 个，之后失败：${message}` : message)
    } finally {
      if (input.current) input.current.value = ''
      setUploading(false)
      if (success) await load(true)
    }
  }

  function closeDelete() { setPendingDelete(null); setDeleteError('') }
  async function remove() {
    if (!pendingDelete) return
    setDeleting(true); setDeleteError('')
    try { await mediaAssetsApi.delete(pendingDelete.id); notice.success('媒体已删除'); closeDelete(); await load(true) }
    catch (cause) { setDeleteError(errorMessage(cause, '删除失败')) }
    finally { setDeleting(false) }
  }

  // 复制文本到剪贴板，优先 Clipboard API，降级 execCommand（兼容 HTTP）
  async function copyText(text: string, label: string) {
    try {
      await navigator.clipboard.writeText(text)
      notice.success(label)
      return
    } catch { /* Clipboard API 不可用，尝试 execCommand */ }
    const ta = document.createElement('textarea')
    ta.value = text
    ta.className = 'visually-hidden'
    document.body.appendChild(ta)
    ta.select()
    try { document.execCommand('copy'); notice.success(label) } catch { notice.error('复制失败') }
    finally { document.body.removeChild(ta) }
  }

  async function copyImage(url: string, fullURL: string) {
    try {
      const blob = await fetch(url).then(response => response.blob())
      await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })])
      notice.success('图片已复制到剪贴板')
      return
    } catch { /* ClipboardItem 不可用，降级复制链接 */ }
    void copyText(fullURL, '图片链接已复制（浏览器不支持复制图片文件）')
  }

  const pickFiles = () => input.current?.click()
  const uploadButton = (label: string) => <button type="button" className="button button--primary" disabled={uploading} onClick={pickFiles}>{uploading ? '上传中…' : label}</button>

  if (loading && !items.length) return <PageState kind="loading" title="正在读取媒体资源" />
  if (loadError && !items.length) return <PageState kind="error" title="无法读取媒体资源" description={loadError} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />

  return <div className="business-page">
    {canUpload && <div className="page-toolbar page-toolbar--end"><div className="toolbar-actions"><input ref={input} className="visually-hidden" type="file" accept={UPLOAD_ACCEPT} multiple tabIndex={-1} aria-hidden="true" onChange={event => void upload(event.target.files)} />{uploadButton('上传媒体')}</div></div>}
    {notice.element}
    {!items.length ? <PageState kind="empty" title="媒体库为空" description="上传图片、视频、音频或 PDF 后，图片与视频可用于首页和登录页背景。" action={canUpload ? uploadButton('上传第一个媒体文件') : undefined} /> : <div className="resource-grid">{items.map(item => {
      const url = mediaURL(item.file_path)
      const fullURL = new URL(url, window.location.origin).href
      return <article className="resource-card" key={item.id}>
        <div className="resource-preview">{item.media_type === 'video' ? <video src={url} controls preload="metadata" /> : item.media_type === 'audio' ? <audio src={url} controls preload="metadata" /> : item.media_type === 'document' ? <a className="resource-doc" href={url} target="_blank" rel="noreferrer">{item.mime_type}</a> : <img src={url} alt={item.original_name} loading="lazy" />}</div>
        <footer>
          <strong title={item.original_name}>{item.original_name}</strong>
          <button type="button" className="icon-button" title="复制链接" aria-label={`复制“${item.original_name}”的链接`} onClick={() => void copyText(fullURL, '链接已复制')}><CopyIcon /></button>
          <small>{item.mime_type} · {formatFileSize(item.file_size)}</small>
          <small>{item.width && item.height ? `${item.width} × ${item.height} · ` : ''}{formatDateTime(item.created_at)}</small>
          <div className="table-actions">
            {item.media_type === 'image' && <button type="button" className="button button--quiet" onClick={() => void copyImage(url, fullURL)}>复制图片</button>}
            {canDelete(item) && <DeleteButton label={`删除“${item.original_name}”`} onClick={() => setPendingDelete(item)} />}
          </div>
        </footer>
      </article>
    })}</div>}
    <ConfirmDialog open={pendingDelete !== null} title="删除媒体资源" description={`将永久删除“${pendingDelete?.original_name ?? ''}”及其文件。使用该资源的背景配置不会自动替换。`} confirmLabel="删除" danger busy={deleting} error={deleteError} onClose={closeDelete} onConfirm={() => void remove()} />
  </div>
}
