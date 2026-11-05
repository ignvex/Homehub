import { api } from '@/lib/api'
import type { BackgroundSettings } from '@/lib/home'

// ============================================================
// 设置页的数据形状
//
// 刻意**不做**扁平化：gravatar / rss 的字段很多，扁平化后前端只能
// 提交自己认识的那几个，后端整体替换时会把没映射的字段（timeout、
// 图片缓存参数等）重置成零值。保留后端对象原样，保存时整段回传即可。
//
// 私钥不在这套结构里：后端只回 private_key_set 一位，写入走单独的
// setKey 端点。这样「读配置 → 改一处 → 提交整段」的流程永远碰不到它。
// ============================================================

export interface GravatarConfig {
  enabled: boolean
  default_style: string
  default_size: number
  rating: string
  timeout_seconds: number
  max_bytes: number
}

export interface RSSConfig {
  /** 关闭条目数量上限。默认开启，开启时 retention_count 不生效。 */
  retention_unlimited: boolean
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
}

/** ExtensionConfig 是唯一的 Extension 连接配置。 */
export interface ExtensionFeatures {
  hub: boolean
  fetch: boolean
  avatar: boolean
}

export interface ExtensionConfig {
  enabled: boolean
  base_url: string
  /** 哪些能力交给 Extension。缺省全 false，升级不会改变现有行为。 */
  features: ExtensionFeatures
  /** 留空 = 直连（默认）。 */
  proxy_names: string[]
  timeout_seconds: number
  /** 后端只回这一位，私钥内容永不下发。 */
  private_key_set: boolean
}

/** ProxyEntry 是脱敏后的代理条目：不含 http_url / https_url。 */
export interface ProxyEntry {
  name: string
  type: string
  enabled: boolean
}

export interface ProxyConfig {
  entries: ProxyEntry[]
  /** 全局默认代理链，所有出网模块共用。 */
  default: string[]
}

export interface SafeConfig {
  server: { host: string; port: number }
  database: { host: string; port: number; user: string; dbname: string; sslmode: string }
  auth: { username: string }
  /** retention_days：数据库日志保留天数，0 表示永久保留，范围 0–3650。 */
  log: { level: string; format: string; db_writer: boolean; retention_days: number }
  background: BackgroundSettings
  proxy: ProxyConfig
  gravatar: GravatarConfig
  bookmark: Record<string, never>
  rss: RSSConfig
  extension: ExtensionConfig
}

/** Secrets 是不从服务端回传的字段，单独维护，留空表示不改。 */
export interface Secrets {
  database_password: string
  auth_password: string
  session_secret: string
}

export const emptySecrets: Secrets = {
  database_password: '',
  auth_password: '',
  session_secret: '',
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
      return { gravatar: draft.gravatar }
    case 'bookmark':
      return { bookmark: draft.bookmark }
    case 'rss':
      return { rss: draft.rss }
    case 'extension':
      // 私钥不在这里：它走 setKey，避免整段覆盖时被清空。
      return {
        extension: {
          enabled: draft.extension.enabled,
          base_url: draft.extension.base_url,
          features: draft.extension.features,
          proxy_names: draft.extension.proxy_names,
          timeout_seconds: draft.extension.timeout_seconds,
        },
      }
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
  log: Omit<SafeConfig['log'], 'retention_days'> & { retention_days?: number }
  webui?: {
    home?: { background?: BackgroundSettings }
    login?: { background?: BackgroundSettings; layout?: string }
  }
  proxy?: Partial<ProxyConfig>
  gravatar?: Partial<GravatarConfig>
  bookmark?: Record<string, never>
  rss?: Partial<RSSConfig>
  extension?: Partial<ExtensionConfig>
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
    log: { ...raw.log, retention_days: raw.log.retention_days ?? 30 },
    background: raw.webui?.home?.background ?? defaultBackground,
    proxy: {
      entries: raw.proxy?.entries ?? [],
      default: raw.proxy?.default ?? [],
    },
    gravatar: {
      enabled: raw.gravatar?.enabled ?? false,
      default_style: raw.gravatar?.default_style ?? 'identicon',
      default_size: raw.gravatar?.default_size ?? 256,
      rating: raw.gravatar?.rating ?? 'g',
      timeout_seconds: raw.gravatar?.timeout_seconds ?? 10,
      max_bytes: raw.gravatar?.max_bytes ?? 2097152,
    },
    bookmark: {},
    rss: {
      retention_unlimited: raw.rss?.retention_unlimited ?? true,
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
    },
    extension: {
      enabled: raw.extension?.enabled ?? false,
      base_url: raw.extension?.base_url ?? '',
      features: {
        hub: raw.extension?.features?.hub ?? false,
        fetch: raw.extension?.features?.fetch ?? false,
        avatar: raw.extension?.features?.avatar ?? false,
      },
      proxy_names: raw.extension?.proxy_names ?? [],
      timeout_seconds: raw.extension?.timeout_seconds ?? 30,
      private_key_set: raw.extension?.private_key_set ?? false,
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
