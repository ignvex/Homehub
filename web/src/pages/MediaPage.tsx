import { useEffect, useRef, useState } from 'react'
import { ConfirmDialog, Notice, PageState } from '@/components/WorkspaceUI'
import { mediaURL } from '@/lib/media'
import { UPLOAD_ACCEPT, mediaAssetsApi, type MediaAsset } from '@/lib/mediaAssets'
import { formatDateTime, formatFileSize } from '@/lib/types'

export function MediaPage() {
  const [items, setItems] = useState<MediaAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [pendingDelete, setPendingDelete] = useState<MediaAsset | null>(null)
  const [deleting, setDeleting] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  async function load() {
    setLoading(true)
    try { setItems(await mediaAssetsApi.list()); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取媒体资源') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  async function upload(files: FileList | null) {
    if (!files?.length) return
    setUploading(true); setError('')
    let success = 0
    try {
      for (const file of Array.from(files)) { await mediaAssetsApi.upload(file); success++ }
      setMessage(success === 1 ? '媒体已上传' : `已上传 ${success} 个媒体文件`)
      if (input.current) input.current.value = ''
      await load()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '上传失败') }
    finally { setUploading(false) }
  }

  async function remove() {
    if (!pendingDelete) return
    setDeleting(true)
    try { await mediaAssetsApi.delete(pendingDelete.id); setMessage('媒体已删除'); setPendingDelete(null); await load() }
    catch (cause) { setError(cause instanceof Error ? cause.message : '删除失败') }
    finally { setDeleting(false) }
  }

  if (loading) return <PageState kind="loading" title="正在读取媒体资源" />
  if (error && !items.length) return <PageState kind="error" title="无法读取媒体资源" description={error} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />

  // 复制文本到剪贴板，优先 Clipboard API，降级 execCommand（兼容 HTTP）
  async function copyText(text: string, label: string) {
    try {
      await navigator.clipboard.writeText(text)
      setMessage(label)
      return
    } catch { /* Clipboard API 不可用，尝试 execCommand */ }
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'; ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try { document.execCommand('copy'); setMessage(label) } catch { setError('复制失败') }
    finally { document.body.removeChild(ta) }
  }

  return <div className="business-page">
    <div className="page-toolbar"><div><p>资源库</p><h2>媒体资源</h2></div><div className="toolbar-actions"><input ref={input} id="media-upload" className="visually-hidden" type="file" accept={UPLOAD_ACCEPT} multiple onChange={event => void upload(event.target.files)} /><label htmlFor="media-upload" className="button button--primary" aria-disabled={uploading}>{uploading ? '上传中...' : '上传媒体'}</label></div></div>
    <Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} onDismiss={() => setMessage('')} />
    {!items.length ? <PageState kind="empty" title="媒体库为空" description="上传图片、视频、音频或 PDF 后，图片与视频可用于首页和登录页背景。" action={<label htmlFor="media-upload" className="button button--primary">上传第一个媒体文件</label>} /> : <div className="resource-grid">{items.map(item => { const url = mediaURL(item.file_path); const fullURL = new URL(url, window.location.origin).href; return <article className="resource-card" key={item.id}><div className="resource-preview">{item.media_type === 'video' ? <video src={url} controls preload="metadata" /> : item.media_type === 'audio' ? <audio src={url} controls preload="metadata" /> : item.media_type === 'document' ? <a className="resource-doc" href={url} target="_blank" rel="noreferrer">{item.mime_type}</a> : <img src={url} alt={item.original_name} loading="lazy" />}</div><footer><strong title={item.original_name}>{item.original_name}</strong><button className="icon-button" title="复制链接" aria-label="复制链接" onClick={() => { void copyText(fullURL, '链接已复制') }}>📋</button><small>{item.mime_type} · {formatFileSize(item.file_size)}</small><small>{item.width && item.height ? `${item.width} × ${item.height} · ` : ''}{formatDateTime(item.created_at)}</small><div className="table-actions">{item.media_type === 'image' && <button className="button button--quiet" onClick={async () => { try { const blob = await fetch(url).then(r => r.blob()); await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]); setMessage('图片已复制到剪贴板'); return } catch { /* ClipboardItem 不可用，降级复制链接 */ } void copyText(fullURL, '图片链接已复制（图片文件复制不支持，已复制链接）') }}>复制图片</button>}<button className="icon-button" aria-label="删除" onClick={() => setPendingDelete(item)}>×</button></div></footer></article> })}</div>}
    <ConfirmDialog open={pendingDelete !== null} title="删除媒体资源" description={`将永久删除“${pendingDelete?.original_name ?? ''}”及其文件。使用该资源的背景配置不会自动替换。`} confirmLabel="删除" danger busy={deleting} onClose={() => setPendingDelete(null)} onConfirm={() => void remove()} />
  </div>
}
