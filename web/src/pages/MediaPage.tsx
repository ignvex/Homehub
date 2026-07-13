import { useEffect, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { mediaURL } from '@/lib/media'

interface MediaAsset { id: number; original_name: string; file_path: string; media_type: string; mime_type: string; file_size: number; created_at: string }
export function MediaPage() {
  const [items, setItems] = useState<MediaAsset[]>([]); const [message, setMessage] = useState(''); const input = useRef<HTMLInputElement>(null)
  const load = () => api.get<MediaAsset[]>('/media-assets').then(setItems).catch(e => setMessage(e instanceof Error ? e.message : '载入失败'))
  useEffect(() => { void load() }, [])
  async function upload() { const file = input.current?.files?.[0]; if (!file) return; const body = new FormData(); body.append('file', file); try { await api.post<MediaAsset>('/media-assets', body); setMessage('上传完成'); if (input.current) input.current.value = ''; load() } catch(e) { setMessage(e instanceof Error ? e.message : '上传失败') } }
  async function remove(id: number) { if (!confirm('确定删除这个媒体资源？')) return; await api.delete(`/media-assets/${id}`); load() }
  return <div className="business-page"><section className="work-panel toolbar-panel"><div><strong>{items.length}</strong><span>个媒体资源</span></div><label className="upload-control"><input ref={input} type="file" accept="image/*,video/*" onChange={upload}/><span>上传媒体</span></label></section>{message && <div className="form-message">{message}</div>}<section className="media-grid">{items.map(item => <article key={item.id} className="media-item"><div>{item.media_type === 'video' ? <video src={mediaURL(item.file_path)} controls preload="metadata"/> : <img src={mediaURL(item.file_path)} alt={item.original_name} loading="lazy"/>}</div><footer><strong title={item.original_name}>{item.original_name}</strong><span>{(item.file_size / 1024 / 1024).toFixed(2)} MB</span><button onClick={() => remove(item.id)}>删除</button></footer></article>)}{!items.length && <div className="empty-state">尚未上传媒体资源</div>}</section></div>
}
