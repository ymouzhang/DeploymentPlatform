import { describe, expect, it } from 'vitest'
import type { Permission, PermissionScope, User } from '../types'
import { allowedPagePaths, documentTitleForPath, pageNameForPath } from './navigation'

const businessPaths = ['/packages', '/environments', '/models', '/services', '/communications', '/operations']
const managementPaths = ['/dashboard', '/roles', '/users', '/audit', '/notifications']

function roleUser(key: string, permissions: Permission[]) {
  return {
    id: key,
    username: key,
    roles: [{ id: key, key, name: key }],
    permissions: Object.fromEntries(permissions.map((permission) => [permission, 'all'])) as Partial<Record<Permission, PermissionScope>>,
    enabled: true,
    must_change_password: false,
    is_initial_admin: false,
    created_at: '2026-09-02T00:00:00Z',
    updated_at: '2026-09-02T00:00:00Z',
  } satisfies User
}

describe('built-in role navigation', () => {
  it.each(['super_admin', 'platform_admin'])('%s sees management and business pages', (role) => {
    const permissions: Permission[] = [
      'dashboard.read', 'role.read', 'account.read', 'audit.read', 'notification.read',
      'package.read', 'environment.read', 'model.read', 'service.read', 'communication.read', 'operation.read',
    ]
    expect(allowedPagePaths(roleUser(role, permissions))).toEqual([
      '/dashboard', '/packages', '/environments', '/models', '/services', '/communications',
      '/roles', '/users', '/operations', '/audit', '/notifications',
    ])
  })

  it.each(['operator', 'viewer'])('%s only sees own-scope business pages', (role) => {
    const permissions: Permission[] = [
      'package.read', 'environment.read', 'model.read', 'service.read', 'communication.read', 'operation.read',
    ]
    const paths = allowedPagePaths(roleUser(role, permissions))
    expect(paths).toEqual(businessPaths)
    expect(paths.some((path) => managementPaths.includes(path))).toBe(false)
  })
})

describe('page titles', () => {
  it('maps known routes and uses a neutral fallback', () => {
    expect(pageNameForPath('/audit')).toBe('审计日志')
    expect(documentTitleForPath('/audit')).toBe('DP · 审计日志')
    expect(documentTitleForPath('/unknown')).toBe('DP · 部署平台')
  })
})
