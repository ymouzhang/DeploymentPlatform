import type { CommunicationFilter } from '../../types'

export const communicationKeys = {
  summary: ['communication-summary'] as const,
  lists: ['communications'] as const,
  list: (filter: CommunicationFilter) => ['communications', filter] as const,
  details: ['communication'] as const,
  detail: (id: string | undefined) => ['communication', id] as const,
}
