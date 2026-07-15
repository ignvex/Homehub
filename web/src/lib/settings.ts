import { api } from '@/lib/api'
import type { BackgroundSettings } from '@/lib/home'

// ============================================================
// 前端消费的 SafeConfig 类型（扁平化，匹配 SettingsPage 表单视图）
// ============================================================
export interface SafeConfig {
  server: { host: string; port: number }
  database: { host: string; port: number; user: string; dbname: string; sslmode: string }
  auth: { username: string }
  log: { level: string; format: string; db_writer: boolean }
  background: BackgroundSettings
  proxy_enabled: boolean
  proxy: { name: string; type: string; enabled: boolean }[]
  gravatar: { enabled: boolean; default_style: string; default_size: number; rating: string; use_proxy: boolean; proxy_name: string; custom_proxy_url: string }
  bookmark: { use_proxy: boolean; proxy_names: string[] }
  rss: { use_proxy: boolean; proxy_names: string[]; retention_count: number; background_fetch_enabled: boolean; background_fetch_interval: number }
  bilibili: { enabled: boolean; video_enabled: boolean; article_enabled: boolean; user_enabled: boolean; user_agent: string }
}

// ============================================================
// 后端实际返回的结构（嵌套 webui，proxy_names 数组等）
// ============================================================
interface BackendSafeConfig {
  server: SafeConfig['server']
  database: SafeConfig['database']
  auth: SafeConfig['auth']
  log: SafeConfig['log']
  webui?: {
    home?: { background?: BackgroundSettings }
    login?: { background?: BackgroundSettings; layout?: string }
  }
  proxy?: { name: string; type: string; enabled: boolean }[]
  gravatar?: { enabled?: boolean; default_style?: string; default_size?: number; rating?: string; proxy_names?: string[] }
  bookmark?: { proxy_names?: string[] }
  rss?: { proxy_names?: string[]; retention_count?: number; background_fetch_enabled?: boolean; background_fetch_interval?: number }
  bilibili?: SafeConfig['bilibili']
}

// ============================================================
// 从后端嵌套结构转换为前端扁平结构
// ============================================================
function transformBackendToSafe(raw: BackendSafeConfig): SafeConfig {
  const bg = raw.webui?.home?.background ?? { mode: 'single', interval_seconds: 30, transition_effect: 'fade', fade_duration_ms: 1000, selected_asset_ids: [] }

  const proxyList = raw.proxy ?? []
  const hasEnabledProxy = proxyList.some(p => p.enabled)

  const gravatarProxyNames = raw.gravatar?.proxy_names ?? []
  const bookmarkProxyNames = raw.bookmark?.proxy_names ?? []
  const rssProxyNames = raw.rss?.proxy_names ?? []

  return {
    server: raw.server,
    database: raw.database,
    auth: raw.auth,
    log: raw.log,
    background: bg,
    proxy_enabled: hasEnabledProxy,
    proxy: proxyList,
    gravatar: {
      enabled: raw.gravatar?.enabled ?? false,
      default_style: raw.gravatar?.default_style ?? 'identicon',
      default_size: raw.gravatar?.default_size ?? 200,
      rating: raw.gravatar?.rating ?? 'g',
      use_proxy: gravatarProxyNames.length > 0,
      proxy_name: gravatarProxyNames[0] ?? '',
      custom_proxy_url: '',
    },
    bookmark: {
      use_proxy: bookmarkProxyNames.length > 0,
      proxy_names: bookmarkProxyNames,
    },
    rss: {
      use_proxy: rssProxyNames.length > 0,
      proxy_names: rssProxyNames,
      retention_count: raw.rss?.retention_count ?? 5000,
      background_fetch_enabled: raw.rss?.background_fetch_enabled ?? true,
      background_fetch_interval: raw.rss?.background_fetch_interval ?? 300,
    },
    bilibili: raw.bilibili ?? {
      enabled: false, video_enabled: false, article_enabled: false, user_enabled: false, user_agent: '',
    },
  }
}

// ============================================================
// 前端更新请求 → 后端 UpdateConfigRequest 的字段映射
// ============================================================
export interface ConfigUpdate {
  server?: Partial<SafeConfig['server']>
  database?: Partial<SafeConfig['database']> & { password?: string }
  auth?: Partial<SafeConfig['auth']> & { password?: string; session_secret?: string }
  log?: Partial<SafeConfig['log']>
  background?: SafeConfig['background']
  webui?: { home?: { background?: Partial<BackgroundSettings> } }
  proxy_enabled?: boolean
  proxy?: SafeConfig['proxy']
  gravatar?: Partial<SafeConfig['gravatar']> & { proxy_names?: string[] }
  bookmark?: Partial<SafeConfig['bookmark']>
  rss?: Partial<SafeConfig['rss']>
}

// ============================================================
// SettingsPage 专用的 settingsApi — get 自动扁平化，update 自动反扁平化
// ============================================================
export const settingsApi = {
  get: async (): Promise<SafeConfig> => {
    const raw = await api.get<BackendSafeConfig>('/config')
    return transformBackendToSafe(raw)
  },
  update: async (body: ConfigUpdate): Promise<void> => {
    // 将前端的 background 拍回后端期望的 webui 嵌套结构
    const payload: Record<string, unknown> = { ...body }
    if (body.background) {
      payload.webui = { home: { background: body.background } }
      delete payload.background
    }
    // gravatar proxy_name → proxy_names 数组
    if (body.gravatar && body.gravatar.proxy_name !== undefined && !body.gravatar.proxy_names) {
      payload.gravatar = {
        ...body.gravatar,
        proxy_names: body.gravatar.proxy_name ? [body.gravatar.proxy_name] : [],
      }
    }
    return api.put<void>('/config', payload)
  },
  getBackground: () => api.get<BackgroundSettings>('/background'),
  updateBackground: (body: BackgroundSettings) => api.put<void>('/background', body),
}
