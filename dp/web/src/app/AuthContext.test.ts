import { describe, expect, it } from 'vitest'
import type { User } from '../types'
import { canAccess, hasAllAccess } from './AuthContext'

const user: User = {
  id: 'user-1',
  username: 'operator',
  roles: [{ id: 'role-1', key: 'operator', name: '运维人员' }],
  permissions: {
    'environment.read': 'own',
    'package.read': 'all',
  },
  enabled: true,
  must_change_password: false,
  is_initial_admin: false,
  created_at: '2026-09-02T00:00:00Z',
  updated_at: '2026-09-02T00:00:00Z',
}

describe('RBAC access helpers', () => {
  it('denies missing permissions by default', () => {
    expect(canAccess(user, 'role.read')).toBe(false)
    expect(canAccess(undefined, 'environment.read')).toBe(false)
  })

  it('enforces own scope against the resource owner', () => {
    expect(canAccess(user, 'environment.read', user.id)).toBe(true)
    expect(canAccess(user, 'environment.read', 'user-2')).toBe(false)
    expect(hasAllAccess(user, 'environment.read')).toBe(false)
  })

  it('allows all scope for every owner', () => {
    expect(canAccess(user, 'package.read', 'user-2')).toBe(true)
    expect(hasAllAccess(user, 'package.read')).toBe(true)
  })
})
