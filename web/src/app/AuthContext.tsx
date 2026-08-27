import { createContext, useContext } from 'react'
import type { User } from '../types'

export interface AuthState {
  user: User
  ownerId?: string
  setOwnerId: (value?: string) => void
  users: User[]
  logout: () => void
}

export const AuthContext = createContext<AuthState | null>(null)

export function useAuth() {
  const value = useContext(AuthContext)
  if (!value) throw new Error('AuthContext is unavailable')
  return value
}
