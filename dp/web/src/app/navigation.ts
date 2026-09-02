import type { Permission, User } from '../types'
import { canAccess } from './AuthContext'

export const pagePermissions: ReadonlyArray<readonly [string, Permission]> = [
  ['/dashboard', 'dashboard.read'],
  ['/packages', 'package.read'],
  ['/environments', 'environment.read'],
  ['/models', 'model.read'],
  ['/services', 'service.read'],
  ['/communications', 'communication.read'],
  ['/roles', 'role.read'],
  ['/users', 'account.read'],
  ['/operations', 'operation.read'],
  ['/audit', 'audit.read'],
  ['/notifications', 'notification.read'],
]

const pageNames: Readonly<Record<string, string>> = {
  '/login': '登录',
  '/dashboard': '管理总览',
  '/packages': '安装包管理',
  '/environments': '环境管理',
  '/models': '模型管理',
  '/services': '服务管理',
  '/communications': '消息中心',
  '/roles': '角色与权限',
  '/users': '账号管理',
  '/operations': '操作中心',
  '/audit': '审计日志',
  '/notifications': '通知中心',
  '/forbidden': '无权访问',
}

export function pageNameForPath(path: string) {
  return pageNames[path] ?? '部署平台'
}

export function documentTitleForPath(path: string) {
  return `DP · ${pageNameForPath(path)}`
}

export function allowedPagePaths(user: User) {
  return pagePermissions.filter(([, permission]) => canAccess(user, permission)).map(([path]) => path)
}

export function firstAllowedPath(user: User) {
  return allowedPagePaths(user)[0] ?? '/forbidden'
}
