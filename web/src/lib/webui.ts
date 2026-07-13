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

export const getWebUIConfig = () => api.get<WebUIConfig>('/webui')
