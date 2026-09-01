import type { Role, User } from '../../types'

export function isSuperAdmin(user: Pick<User, 'roles'>) {
  return user.roles.some((role) => role.key === 'super_admin')
}

export function canMutateAccount(actor: Pick<User, 'roles'>, target: Pick<User, 'roles'>) {
  return !isSuperAdmin(target) || isSuperAdmin(actor)
}

export function assignableRoles(actor: Pick<User, 'roles'>, roles: Role[]) {
  if (isSuperAdmin(actor)) return roles
  return roles.filter((role) => role.key !== 'super_admin')
}
