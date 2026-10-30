import { useEffect, useState } from 'react'
import { profileApi } from '@/lib/dashboard'

const BASE_URL = '/api/v1'

export function UserAvatar({ username, size = 40, revision, userId, className = '' }: { username?: string; size?: number; revision?: string | number; userId?: number; className?: string }) {
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [revision, size, userId])
  const initial = (username || 'U').trim().slice(0, 1).toUpperCase()
  // 优先用 ID 定位头像：URL 与用户一一对应，切换账号时不会命中别人的缓存。
  // 没有 ID 时回落到 /avatar/me（后端对该路径返回 no-store）。
  const src = userId
    ? `${BASE_URL}/avatar/${userId}${revision ? `?v=${revision}` : ''}`
    : profileApi.avatarUrl(revision)
  return <span className={`user-avatar ${className}`} style={{ width: size, height: size }}>
    {failed ? <span aria-hidden="true">{initial}</span> : <img src={src} alt={`${username || '用户'}的头像`} width={size} height={size} onError={() => setFailed(true)} />}
  </span>
}
