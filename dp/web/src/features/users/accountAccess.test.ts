import { describe, expect, it } from 'vitest'
import type { RoleRef } from '../../types'
import { assignableRoles, canMutateAccount } from './accountAccess'

const role = (key: string): RoleRef => ({ id: key, key, name: key })
const user = (key: string) => ({ roles: [role(key)] })

describe('privileged account controls', () => {
  it('prevents a non-super administrator from mutating a super administrator', () => {
    expect(canMutateAccount(user('platform_admin'), user('super_admin'))).toBe(false)
    expect(canMutateAccount(user('platform_admin'), user('operator'))).toBe(true)
  })

  it('allows a super administrator to manage another super administrator', () => {
    expect(canMutateAccount(user('super_admin'), user('super_admin'))).toBe(true)
  })

  it('removes super_admin from role choices for non-super administrators', () => {
    const roles = [
      { id: 'super_admin', key: 'super_admin', name: '超级管理员', description: '', system: true, grants: [], member_count: 1 },
      { id: 'operator', key: 'operator', name: '运维人员', description: '', system: true, grants: [], member_count: 1 },
    ]
    expect(assignableRoles(user('platform_admin'), roles).map((item) => item.key)).toEqual(['operator'])
    expect(assignableRoles(user('super_admin'), roles).map((item) => item.key)).toEqual(['super_admin', 'operator'])
  })
})
