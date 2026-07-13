import { useEffect, useState } from 'react'
import { profileApi } from '@/lib/dashboard'

export function UserAvatar({ username, size = 40, revision, className = '' }: { username?: string; size?: number; revision?: string | number; className?: string }) {
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [revision, size])
  const initial = (username || 'U').trim().slice(0, 1).toUpperCase()
  return <span className={`user-avatar ${className}`} style={{ width: size, height: size }}>
    {failed ? <span aria-hidden="true">{initial}</span> : <img src={profileApi.avatarUrl(size, revision)} alt={`${username || '用户'}的头像`} width={size} height={size} onError={() => setFailed(true)} />}
  </span>
}
