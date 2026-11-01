import { api } from '@/lib/api'
import type { BackgroundSettings } from '@/lib/home'

// ============================================================
// 设置页的数据形状
//
// 刻意**不做**扁平化：gravatar / rss 的字段很多，扁平化后前端只能
// 提交自己认识的那几个，后端整体替换时会把没映射的字段（timeout、
// 图片缓存参数等）重置成零值。保留后端对象原样，保存时整段回传即可。
//
// 与后端的唯一差异是 extension_token：后端脱敏成空串，只用
// extension_token_set 告诉前端「有没有配过」。保存时留空 = 不改。
// ============================================================

export interface GravatarConfig {
  enabled: boolean
  default_style: string
  default_size: number
  rating: string
  proxy_names: string[]
  extension_url: string
  /** 后端永远返回空串；要修改就填新值。 */
  extension_token: string
  extension_token_set: boolean
  timeout_seconds: number
  max_bytes: number
}

export interface RSSConfig {
  proxy_names: string[]
  retention_count: number
  background_fetch_enabled: boolean
  background_fetch_interval: number
  user_agent: string
  fetch_concurrency: number
  fetch_timeout_seconds: number
  image_cache_enabled: boolean
  image_cache_concurrency: number
  image_max_bytes: number
  image_cleanup_enabled: boolean
  image_retention_days: number
  image_cleanup_interval_hours: number
  image_allow_private_hosts: boolean
  extension_url: string
  extension_token: string
  extension_token_set: boolean
}

export interface ExtensionHubConfig {
  enabled: boolean
  base_url: string
  private_key_path: string
  timeout_seconds: number
}

export interface SafeConfig {
  server: { host: string; port: number }
  database: { host: string; port: number; user: string; dbname: string; sslmode: string }
  auth: { username: string }
  log: { level: string; format: string; db_writer: boolean }
  background: BackgroundSettings
  proxy: { name: string; type: string; enabled: boolean }[]
  gravatar: GravatarConfig
  bookmark: { proxy_names: string[] }
  rss: RSSConfig
  extension_hub: ExtensionHubConfig
}

/** Secrets 是不从服务端回传的字段，单独维护，留空表示不改。 */
export interface Secrets {
  database_password: string
  auth_password: string
  session_secret: string
  gravatar_extension_token: string
  rss_extension_token: string
}

export const emptySecrets: Secrets = {
  database_password: '',
  auth_password: '',
  session_secret: '',
  gravatar_extension_token: '',
  rss_extension_token: '',
}

/** settingsSection 是设置页可独立保存的模块。 */
export type SettingsSection =
  | 'connection'
  | 'auth'
  | 'log'
  | 'appearance'
  | 'gravatar'
  | 'bookmark'
  | 'rss'
  | 'extension'

/** updatePayload 是一个模块的完整更新请求。 */
export type UpdatePayload = Record<string, unknown>

/**
 * buildPayload 把某个模块的表单状态组装成 PUT /config 的请求体。
 *
 * 每次只带一个顶层段：后端对指针为 nil 的段完全不动，所以
 * 「只保存这一块」不需要额外的接口。
 */
export function buildPayload(
  section: SettingsSection,
  draft: SafeConfig,
  secrets: Secrets,
): UpdatePayload {
  switch (section) {
    case 'connection':
      return {
        server: draft.server,
        database: {
          ...draft.database,
          ...(secrets.database_password ? { password: secrets.database_password } : {}),
        },
      }
    case 'auth':
      return {
        auth: {
          username: draft.auth.username,
          ...(secrets.auth_password ? { password: secrets.auth_password } : {}),
          ...(secrets.session_secret ? { session_secret: secrets.session_secret } : {}),
        },
      }
    case 'log':
      return { log: draft.log }
    case 'appearance':
      return { webui: { home: { background: draft.background } } }
    case 'gravatar':
      return {
        gravatar: {
          ...draft.gravatar,
          // 空串 = 后端保留原值；填了才覆盖。
          extension_token: secrets.gravatar_extension_token,
        },
      }
    case 'bookmark':
      return { bookmark: draft.bookmark }
    case 'rss':
      return {
        rss: {
          ...draft.rss,
          extension_token: secrets.rss_extension_token,
        },
      }
    case 'extension':
      return { extension_hub: draft.extension_hub }
  }
}

/** 各模块的中文名，用于保存成功的提示。 */
export const sectionLabels: Record<SettingsSection, string> = {
  connection: '服务与数据库',
  auth: '认证',
  log: '日志',
  appearance: '背景与外观',
  gravatar: 'Gravatar 头像',
  bookmark: '书签',
  rss: 'RSS',
  extension: 'Extension 对接',
}

// ============================================================
// 从后端嵌套结构转换为前端形状
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
  gravatar?: Partial<GravatarConfig>
  bookmark?: { proxy_names?: string[] }
  rss?: Partial<RSSConfig>
  extension_hub?: Partial<ExtensionHubConfig>
}

const defaultBackground: BackgroundSettings = {
  mode: 'single',
  interval_seconds: 30,
  transition_effect: 'fade',
  fade_duration_ms: 1000,
  selected_asset_ids: [],
}

function transformBackendToSafe(raw: BackendSafeConfig): SafeConfig {
  return {
    server: raw.server,
    database: raw.database,
    auth: raw.auth,
    log: raw.log,
    background: raw.webui?.home?.background ?? defaultBackground,
    proxy: raw.proxy ?? [],
    gravatar: {
      enabled: raw.gravatar?.enabled ?? false,
      default_style: raw.gravatar?.default_style ?? 'identicon',
      default_size: raw.gravatar?.default_size ?? 256,
      rating: raw.gravatar?.rating ?? 'g',
      proxy_names: raw.gravatar?.proxy_names ?? [],
      extension_url: raw.gravatar?.extension_url ?? '',
      // 后端永远返回空串，这里保持一致：留空即不改。
      extension_token: '',
      extension_token_set: raw.gravatar?.extension_token_set ?? false,
      timeout_seconds: raw.gravatar?.timeout_seconds ?? 10,
      max_bytes: raw.gravatar?.max_bytes ?? 2097152,
    },
    bookmark: { proxy_names: raw.bookmark?.proxy_names ?? [] },
    rss: {
      proxy_names: raw.rss?.proxy_names ?? [],
      retention_count: raw.rss?.retention_count ?? 5000,
      background_fetch_enabled: raw.rss?.background_fetch_enabled ?? true,
      background_fetch_interval: raw.rss?.background_fetch_interval ?? 300,
      user_agent: raw.rss?.user_agent ?? '',
      fetch_concurrency: raw.rss?.fetch_concurrency ?? 4,
      fetch_timeout_seconds: raw.rss?.fetch_timeout_seconds ?? 30,
      image_cache_enabled: raw.rss?.image_cache_enabled ?? true,
      image_cache_concurrency: raw.rss?.image_cache_concurrency ?? 6,
      image_max_bytes: raw.rss?.image_max_bytes ?? 134217728,
      image_cleanup_enabled: raw.rss?.image_cleanup_enabled ?? true,
      image_retention_days: raw.rss?.image_retention_days ?? 30,
      image_cleanup_interval_hours: raw.rss?.image_cleanup_interval_hours ?? 24,
      image_allow_private_hosts: raw.rss?.image_allow_private_hosts ?? false,
      extension_url: raw.rss?.extension_url ?? '',
      extension_token: '',
      extension_token_set: raw.rss?.extension_token_set ?? false,
    },
    extension_hub: {
      enabled: raw.extension_hub?.enabled ?? false,
      base_url: raw.extension_hub?.base_url ?? '',
      private_key_path: raw.extension_hub?.private_key_path ?? '',
      timeout_seconds: raw.extension_hub?.timeout_seconds ?? 30,
    },
  }
}

export const settingsApi = {
  get: async (): Promise<SafeConfig> => {
    const raw = await api.get<BackendSafeConfig>('/config')
    return transformBackendToSafe(raw)
  },
  update: (payload: UpdatePayload) => api.put<void>('/config', payload),
  getBackground: () => api.get<BackgroundSettings>('/background'),
  updateBackground: (body: BackgroundSettings) => api.put<void>('/background', body),
}
