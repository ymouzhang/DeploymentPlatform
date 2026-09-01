import { createContext, useContext } from 'react'
import type { Permission, User } from '../types'

export interface AuthState {
  user: User
  ownerId?: string
  setOwnerId: (value?: string) => void
  users: User[]
  can: (permission: Permission, ownerId?: string) => boolean
  hasAll: (permission: Permission) => boolean
  logout: () => void
}

export function canAccess(user: User | undefined, permission: Permission, ownerId?: string) {
  const scope = user?.permissions?.[permission]
  if (!scope) return false
  return !ownerId || scope === 'all' || ownerId === user?.id
}

export function hasAllAccess(user: User | undefined, permission: Permission) {
  return user?.permissions?.[permission] === 'all'
}

export const AuthContext = createContext<AuthState | null>(null)

export function useAuth() {
  const value = useContext(AuthContext)
  if (!value) throw new Error('AuthContext is unavailable')
  return value
}
