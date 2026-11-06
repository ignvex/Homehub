import { api } from '@/lib/api'
import type { BackgroundSettings, MediaAsset } from '@/lib/home'

export type ContentLayout = 'left' | 'center' | 'right'

export interface HomeWebUIConfig {
  background: BackgroundSettings
  assets: MediaAsset[]
}

export interface LoginWebUIConfig extends HomeWebUIConfig {
  layout: ContentLayout
}

export interface WebUIConfig {
  home: HomeWebUIConfig
  login: LoginWebUIConfig
}

/** 旧后端在 config.json 缺字段时会下发 null；首页与登录页都按数组用它。 */
function normalizePage<T extends HomeWebUIConfig>(page: T): T {
  return {
    ...page,
    assets: page.assets ?? [],
    background: { ...page.background, selected_asset_ids: page.background?.selected_asset_ids ?? [] },
  }
}

export const getWebUIConfig = () => api.get<WebUIConfig>('/webui')
  .then(config => ({ home: normalizePage(config.home), login: normalizePage(config.login) }))
