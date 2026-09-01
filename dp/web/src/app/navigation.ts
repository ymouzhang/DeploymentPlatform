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

export function allowedPagePaths(user: User) {
  return pagePermissions.filter(([, permission]) => canAccess(user, permission)).map(([path]) => path)
}

export function firstAllowedPath(user: User) {
  return allowedPagePaths(user)[0] ?? '/forbidden'
}
