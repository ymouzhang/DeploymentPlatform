import type {
  Environment,
  EnvironmentInput,
  Operation,
  PackageInfo,
  Service,
  ServiceConfig,
  ServiceTypeInfo,
  ValidationResponse,
  User,
  AuditEvent,
  AuditFilter,
  AuditSummary,
  AdminDashboard,
  Notification,
  OperationFilter,
  UserDetail,
  PackageVersion,
  ServiceConfigPreview,
  ServiceConfigRevision,
  ResourceTag,
  Session,
  Communication,
  CommunicationFilter,
  CommunicationMessage,
  CommunicationResourceInput,
	Model,
	ModelTask,
	ModelUploadCreated,
} from '../types'

interface DataEnvelope<T> {
  data: T
}

interface ErrorEnvelope {
  error: {
    code: string
    message: string
    details?: { field?: string }
  }
  request_id?: string
}

export class ApiError extends Error {
  status: number
  code: string
  field?: string

  constructor(status: number, payload: ErrorEnvelope) {
    super(payload.error.message)
    this.name = 'ApiError'
    this.status = status
    this.code = payload.error.code
    this.field = payload.error.details?.field
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: {
      ...(init?.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
      ...init?.headers,
    },
  })
  const text = await response.text()
  const payload = (text ? JSON.parse(text) : {}) as DataEnvelope<T> | ErrorEnvelope
  if (!response.ok) {
    throw new ApiError(response.status, payload as ErrorEnvelope)
  }
  return (payload as DataEnvelope<T>).data
}

async function validationRequest(url: string, input?: EnvironmentInput): Promise<ValidationResponse> {
  const response = await fetch(url, {
    method: 'POST',
    headers: input ? { 'Content-Type': 'application/json' } : undefined,
    body: input ? JSON.stringify(input) : undefined,
  })
  const payload = (await response.json()) as ValidationResponse | ErrorEnvelope
  if (!response.ok) {
    throw new ApiError(response.status, payload as ErrorEnvelope)
  }
  return payload as ValidationResponse
}

export const api = {
  login: (input: { username: string; password: string }) =>
    request<User>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify(input) }),
  logout: () => request('/api/v1/auth/logout', { method: 'POST', body: '{}' }),
  me: () => request<User>('/api/v1/auth/me'),
  changePassword: ({ current_password, new_password }: { current_password: string; new_password: string }) =>
    request('/api/v1/auth/password', { method: 'PUT', body: JSON.stringify({ current_password, new_password }) }),
  listOwnSessions: () => request<Session[]>('/api/v1/auth/sessions'),
  revokeOwnSession: (sessionId: string) => request<{ session_revoked: boolean; current: boolean }>(`/api/v1/auth/sessions/${sessionId}`, { method: 'DELETE' }),
  listUsers: () => request<User[]>('/api/v1/users'),
  createUser: (input: { username: string; password: string; role: 'admin' | 'user' }) =>
    request<User>('/api/v1/users', { method: 'POST', body: JSON.stringify(input) }),
  resetUserPassword: (id: string, new_password: string, require_password_change: boolean) =>
    request(`/api/v1/users/${id}/password`, { method: 'PUT', body: JSON.stringify({ new_password, require_password_change }) }),
  updateUserStatus: (id: string, enabled: boolean) =>
    request<User>(`/api/v1/users/${id}/status`, { method: 'PUT', body: JSON.stringify({ enabled }) }),
  deleteUser: (id: string) => request(`/api/v1/users/${id}`, { method: 'DELETE' }),
  getUserDetail: (id: string) => request<UserDetail>(`/api/v1/users/${id}`),
  revokeUserSessions: (id: string) => request(`/api/v1/users/${id}/sessions/revoke`, { method: 'POST', body: '{}' }),
  listUserSessions: (id: string) => request<Session[]>(`/api/v1/users/${id}/sessions`),
  revokeUserSession: (id: string, sessionId: string) => request(`/api/v1/users/${id}/sessions/${sessionId}`, { method: 'DELETE' }),
  transferUserResources: (id: string, target_user_id: string) =>
    request<{ source_user_id: string; target_user_id: string; packages: number; environments: number; models: number }>(`/api/v1/users/${id}/transfer`, { method: 'POST', body: JSON.stringify({ target_user_id }) }),
  listEnvironments: (ownerId?: string, tagIds?: string[]) =>
    request<Environment[]>(withScope('/api/v1/environments', ownerId, tagIds)),
  createEnvironment: (input: EnvironmentInput, ownerId?: string) =>
    request<Environment>(withOwner('/api/v1/environments', ownerId), {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  updateEnvironment: (id: string, input: EnvironmentInput) =>
    request<Environment>(`/api/v1/environments/${id}`, {
      method: 'PUT',
      body: JSON.stringify(input),
    }),
  deleteEnvironment: (id: string) =>
    request<null>(`/api/v1/environments/${id}`, { method: 'DELETE' }),
  replaceEnvironmentTags: (id: string, tag_ids: string[]) =>
    request<Environment>(`/api/v1/environments/${id}/tags`, { method: 'PUT', body: JSON.stringify({ tag_ids }) }),
  listTags: (ownerId?: string) => request<ResourceTag[]>(withOwner('/api/v1/tags', ownerId)),
  createTag: (input: { group_name: string; value: string }, ownerId?: string) =>
    request<ResourceTag>(withOwner('/api/v1/tags', ownerId), { method: 'POST', body: JSON.stringify(input) }),
  updateTag: (id: string, input: { group_name: string; value: string }) =>
    request<ResourceTag>(`/api/v1/tags/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteTag: (id: string) => request(`/api/v1/tags/${id}`, { method: 'DELETE' }),
  validateDraft: (input: EnvironmentInput) =>
    validationRequest('/api/v1/environments/validate', input),
  validateEnvironment: (id: string) =>
    validationRequest(`/api/v1/environments/${id}/validate`),
  importEnvironments: (document: unknown) =>
    request<{ created: number; overwritten: number; total: number }>(
      '/api/v1/environments/import',
      { method: 'POST', body: JSON.stringify(document) },
    ),
  listServiceTypes: (ownerId?: string) => request<ServiceTypeInfo[]>(withOwner('/api/v1/service-types', ownerId)),
  listPackages: (ownerId?: string) => request<PackageInfo[]>(withOwner('/api/v1/packages', ownerId)),
  uploadPackage: ({
    serviceType,
    file,
    note,
    ownerId,
  }: {
    serviceType: string
    file?: File
    note?: string
    ownerId?: string
  }) => {
    const body = new FormData()
    if (note !== undefined) {
      body.append('note', note)
    }
    if (file) {
      body.append('file', file)
    }
    return request<PackageInfo>(
      withOwner(`/api/v1/service-types/${encodeURIComponent(serviceType)}/package`, ownerId),
      { method: 'PUT', body },
    )
  },
  deletePackage: ({ serviceType, ownerId }: { serviceType: string; ownerId?: string }) =>
    request<null>(
      withOwner(`/api/v1/service-types/${encodeURIComponent(serviceType)}/package`, ownerId),
      { method: 'DELETE' },
    ),
  listPackageVersions: ({ serviceType, ownerId }: { serviceType: string; ownerId?: string }) =>
    request<PackageVersion[]>(withOwner(`/api/v1/service-types/${encodeURIComponent(serviceType)}/package/versions`, ownerId)),
  activatePackageVersion: ({ serviceType, versionId, ownerId }: { serviceType: string; versionId: string; ownerId?: string }) =>
    request<PackageInfo>(withOwner(`/api/v1/service-types/${encodeURIComponent(serviceType)}/package/versions/${versionId}/current`, ownerId), { method: 'PUT', body: '{}' }),
  deletePackageVersion: ({ serviceType, versionId, ownerId }: { serviceType: string; versionId: string; ownerId?: string }) =>
    request(withOwner(`/api/v1/service-types/${encodeURIComponent(serviceType)}/package/versions/${versionId}`, ownerId), { method: 'DELETE' }),
  listServices: (ownerId?: string, tagIds?: string[]) => request<Service[]>(withScope('/api/v1/services', ownerId, tagIds)),
	listModels: (ownerId?: string) => request<Model[]>(withOwner('/api/v1/models', ownerId)),
	createModelUpload: (input: { name: string; environment_id: string; target_dir: string; original_filename: string; total_bytes: number }, ownerId?: string) =>
		request<ModelUploadCreated>(withOwner('/api/v1/model-uploads', ownerId), { method: 'POST', body: JSON.stringify(input) }),
	modelUploadOffset: async (id: string) => {
		const response = await fetch(`/api/v1/model-uploads/${id}`, { method: 'HEAD' })
		if (!response.ok) throw await responseError(response)
		return Number(response.headers.get('Upload-Offset') ?? '0')
	},
	uploadModelChunk: async (id: string, offset: number, chunk: Blob) => {
		const response = await fetch(`/api/v1/model-uploads/${id}`, {
			method: 'PATCH', headers: { 'Content-Type': 'application/offset+octet-stream', 'Upload-Offset': String(offset) }, body: chunk,
		})
		const next = Number(response.headers.get('Upload-Offset') ?? offset)
		if (!response.ok) throw await responseError(response)
		return next
	},
	completeModelUpload: (id: string) => request<ModelTask>(`/api/v1/model-uploads/${id}/complete`, { method: 'POST', body: '{}' }),
	cancelModelUpload: (id: string) => request(`/api/v1/model-uploads/${id}`, { method: 'DELETE' }),
	retryModel: (id: string) => request<ModelTask>(`/api/v1/models/${id}/retry`, { method: 'POST', body: '{}' }),
	deleteModel: (id: string, confirm_name: string) => request<ModelTask | { deleted: string }>(`/api/v1/models/${id}`, { method: 'DELETE', body: JSON.stringify({ confirm_name }) }),
	getModelTask: (id: string) => request<ModelTask>(`/api/v1/model-tasks/${id}`),
  getServiceConfig: (environmentId: string) =>
    request<ServiceConfig>(`/api/v1/services/${environmentId}/config`),
  updateServiceConfig: ({ environmentId, content }: { environmentId: string; content: string }) =>
    request<ServiceConfig>(`/api/v1/services/${environmentId}/config`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    }),
  previewServiceConfig: ({ environmentId, content }: { environmentId: string; content: string }) =>
    request<ServiceConfigPreview>(`/api/v1/services/${environmentId}/config/preview`, { method: 'POST', body: JSON.stringify({ content }) }),
  listServiceConfigRevisions: (environmentId: string) =>
    request<ServiceConfigRevision[]>(`/api/v1/services/${environmentId}/config/revisions`),
  getServiceConfigRevision: (environmentId: string, revisionId: string) =>
    request<ServiceConfigRevision>(`/api/v1/services/${environmentId}/config/revisions/${revisionId}`),
  rollbackServiceConfigRevision: (environmentId: string, revisionId: string) =>
    request<ServiceConfigRevision>(`/api/v1/services/${environmentId}/config/revisions/${revisionId}/rollback`, { method: 'POST', body: '{}' }),
  startOperation: (environmentId: string, action: 'install' | 'start' | 'stop' | 'reset') =>
    request<{ operation_id: string; status: string }>(
      `/api/v1/services/${environmentId}/${action}`,
      { method: 'POST', body: '{}' },
    ),
  checkHealth: (environmentId: string) =>
    request(`/api/v1/services/${environmentId}/health-check`, {
      method: 'POST',
      body: '{}',
    }),
  getOperation: (id: string) => request<Operation>(`/api/v1/operations/${id}`),
  listOperations: (filter: OperationFilter) =>
    request<{ items: Operation[]; next_cursor: string }>(`/api/v1/operations?${params(filter)}`),
  adminDashboard: (tagIds?: string[]) => request<AdminDashboard>(withScope('/api/v1/admin/dashboard', undefined, tagIds)),
  notificationSummary: () => request<{ unread: number; unresolved: number }>('/api/v1/notifications/summary'),
  listNotifications: (filter: { unread?: boolean; risk_level?: string; cursor?: string; limit?: number }) =>
    request<{ items: Notification[]; next_cursor: string }>(`/api/v1/notifications?${params(filter)}`),
  markNotificationRead: (id: string) => request<Notification>(`/api/v1/notifications/${id}/read`, { method: 'PUT', body: '{}' }),
  resolveNotification: (id: string) => request<Notification>(`/api/v1/notifications/${id}/resolve`, { method: 'PUT', body: '{}' }),
  communicationSummary: () => request<{ unread: number }>('/api/v1/communications/summary'),
  listCommunications: (filter: CommunicationFilter = {}) => request<{ items: Communication[]; next_cursor: string }>(`/api/v1/communications?${params(filter)}`),
  createCommunication: (input: { target_user_id: string; title: string; content: string; resources: CommunicationResourceInput[] }) =>
    request<Communication>('/api/v1/communications', { method: 'POST', body: JSON.stringify(input) }),
  getCommunication: (id: string) => request<Communication>(`/api/v1/communications/${id}`),
  markCommunicationRead: (id: string) => request<Communication>(`/api/v1/communications/${id}/read`, { method: 'PUT', body: '{}' }),
  sendCommunicationMessage: (id: string, content: string) => request<CommunicationMessage>(`/api/v1/communications/${id}/messages`, { method: 'POST', body: JSON.stringify({ content }) }),
  closeCommunication: (id: string, content = '') => request<Communication>(`/api/v1/communications/${id}/close`, { method: 'POST', body: JSON.stringify({ content }) }),
  reopenCommunication: (id: string, content = '') => request<Communication>(`/api/v1/communications/${id}/reopen`, { method: 'POST', body: JSON.stringify({ content }) }),
  listAuditEvents: (filter: AuditFilter) =>
    request<{ items: AuditEvent[]; next_cursor: string }>(`/api/v1/audit-events?${auditParams(filter)}`),
  auditSummary: (filter: AuditFilter) =>
    request<AuditSummary>(`/api/v1/audit-events/summary?${auditParams(filter, false)}`),
  getAuditEvent: (id: string) => request<AuditEvent>(`/api/v1/audit-events/${id}`),
  exportAuditEvents: async (filter: AuditFilter) => {
    const response = await fetch(`/api/v1/audit-events/export?${auditParams(filter, false)}`)
    if (!response.ok) {
      const payload = (await response.json()) as ErrorEnvelope
      throw new ApiError(response.status, payload)
    }
    const disposition = response.headers.get('Content-Disposition') ?? ''
    const filename = disposition.match(/filename="?([^";]+)"?/)?.[1] ?? 'dp-audit.csv'
    return { blob: await response.blob(), filename }
  },
}

async function responseError(response: Response) {
	let payload: ErrorEnvelope = { error: { code: 'HTTP_ERROR', message: `请求失败 (${response.status})` } }
	try { payload = await response.json() as ErrorEnvelope } catch { /* use fallback */ }
	return new ApiError(response.status, payload)
}

function withOwner(path: string, ownerId?: string) {
  return ownerId ? `${path}?owner_id=${encodeURIComponent(ownerId)}` : path
}

function withScope(path: string, ownerId?: string, tagIds?: string[]) {
  const values = new URLSearchParams()
  if (ownerId) values.set('owner_id', ownerId)
  for (const id of tagIds ?? []) values.append('tag_id', id)
  const query = values.toString()
  return query ? `${path}?${query}` : path
}

function auditParams(filter: AuditFilter, includeCursor = true) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filter)) {
    if (value !== undefined && value !== '' && (includeCursor || key !== 'cursor')) {
      params.set(key, String(value))
    }
  }
  return params.toString()
}

function params(values: object) {
  const result = new URLSearchParams()
  for (const [key, value] of Object.entries(values)) {
    if (Array.isArray(value)) {
      for (const item of value) result.append(key, String(item))
    } else if (value !== undefined && value !== '') result.set(key, String(value))
  }
  return result.toString()
}
