/**
 * 五级权限模型：数值越小权限越高。
 *
 * 与后端保持一致：1 admin, 2 operator, 3 editor, 4 member, 5 viewer。
 * 前端只用它来隐藏或禁用不可用的操作，真正的校验始终在后端。
 */
export const LEVEL_ADMIN = 1
export const LEVEL_OPERATOR = 2
export const LEVEL_EDITOR = 3
export const LEVEL_MEMBER = 4
export const LEVEL_VIEWER = 5

export const MIN_LEVEL = LEVEL_ADMIN
export const MAX_LEVEL = LEVEL_VIEWER

/** 内容写（书签、RSS、待办、余额、背景、Extension 推送）所需等级。 */
export const CONTENT_WRITE_LEVEL = LEVEL_EDITOR
/** 媒体上传与删除（仅自己的）所需等级。 */
export const MEDIA_WRITE_LEVEL = LEVEL_MEMBER

export interface LevelInfo { level: number; role: string; label: string; description: string }

export const LEVELS: readonly LevelInfo[] = [
  { level: LEVEL_ADMIN, role: 'admin', label: '管理员', description: '完全控制' },
  { level: LEVEL_OPERATOR, role: 'operator', label: '运维', description: '用户管理与日志' },
  { level: LEVEL_EDITOR, role: 'editor', label: '编辑', description: '内容管理' },
  { level: LEVEL_MEMBER, role: 'member', label: '成员', description: '只能改自己创建的内容' },
  { level: LEVEL_VIEWER, role: 'viewer', label: '访客', description: '只读' },
]

export function isValidLevel(level: unknown): level is number {
  return typeof level === 'number' && Number.isInteger(level) && level >= MIN_LEVEL && level <= MAX_LEVEL
}

export function levelInfo(level: unknown): LevelInfo | undefined {
  return isValidLevel(level) ? LEVELS[level - 1] : undefined
}

/** 中文等级名；未知等级显示「未知等级」。 */
export function levelLabel(level: unknown): string {
  return levelInfo(level)?.label ?? '未知等级'
}

/**
 * 当前用户是否满足 required 等级（数值 ≤ required）。
 * level 缺失或越界时一律返回 false（fail closed）。
 */
export function hasLevel(auth: { level?: number } | null | undefined, required: number): boolean {
  const level = auth?.level
  return isValidLevel(level) && level <= required
}

/** 操作者可以分配的等级：严格低于自己（数值更大）。 */
export function assignableLevels(actorLevel: unknown): LevelInfo[] {
  if (!isValidLevel(actorLevel)) return []
  return LEVELS.filter(item => item.level > actorLevel)
}

/** 操作者能否管理目标账号：目标等级严格低于自己，且不是自己。 */
export function canManage(actor: { id?: number; level?: number } | null | undefined, target: { id: number; level: number }): boolean {
  if (!actor || !isValidLevel(actor.level) || actor.id === target.id) return false
  return isValidLevel(target.level) && target.level > actor.level
}
