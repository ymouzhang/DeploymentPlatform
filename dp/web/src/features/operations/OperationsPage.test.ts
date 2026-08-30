import { describe, expect, it } from 'vitest'
import type { Operation } from '../../types'
import { remediationLink } from './OperationsPage'

const operation = { id: 'op', environment_id: 'env', owner_id: 'owner', action: 'install', status: 'failed', stage: 'package', created_at: '2026-01-01T00:00:00Z' } satisfies Operation

describe('remediationLink', () => {
  it('routes package failures to the owning package scope', () => {
    expect(remediationLink({ ...operation, error_code: 'PACKAGE_NOT_FOUND' })).toBe('/packages?owner_id=owner')
  })

  it('routes install failures to the exact service instance', () => {
    expect(remediationLink({ ...operation, error_code: 'SCRIPT_FAILED' })).toBe('/services?owner_id=owner&environment_id=env')
  })
})
