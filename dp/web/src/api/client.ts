import type {
  ServiceInstance,
  ServiceInstanceInput,
	Host,
	HostInput,
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
	PermissionDefinition,
	Role,
	RoleGrant,
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

type WireAuditEvent = Omit<AuditEvent, 'actor_roles'> & {
  actor_roles?: string[] | null
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

function uploadRequest<T>(
  url: string,
  body: FormData,
  onProgress: (loaded: number, total: number) => void,
  onUploaded: () => void,
  signal?: AbortSignal,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', url)
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(event.loaded, event.total)
    }
    xhr.upload.onload = onUploaded
    xhr.onerror = () => reject(new Error('网络连接中断'))
    xhr.onabort = () => reject(new DOMException('上传已取消', 'AbortError'))
    xhr.onload = () => {
      let payload: DataEnvelope<T> | ErrorEnvelope
      try {
        payload = (xhr.responseText ? JSON.parse(xhr.responseText) : {}) as DataEnvelope<T> | ErrorEnvelope
      } catch {
        reject(new Error(`服务器返回了无法解析的响应（HTTP ${xhr.status}）`))
        return
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new ApiError(xhr.status, payload as ErrorEnvelope))
        return
      }
      resolve((payload as DataEnvelope<T>).data)
    }
    const abort = () => xhr.abort()
    if (signal?.aborted) {
      abort()
      return
    }
    signal?.addEventListener('abort', abort, { once: true })
    xhr.onloadend = () => signal?.removeEventListener('abort', abort)
    xhr.send(body)
  })
}

async function validationRequest(url: string, input?: HostInput): Promise<ValidationResponse> {
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
  createUser: (input: { username: string; password: string; role_ids: string[] }) =>
    request<User>('/api/v1/users', { method: 'POST', body: JSON.stringify(input) }),
	listPermissions: () => request<PermissionDefinition[]>('/api/v1/permissions'),
	listRoles: () => request<Role[]>('/api/v1/roles'),
	createRole: (input: { key: string; name: string; description: string; grants: RoleGrant[] }) =>
		request<Role>('/api/v1/roles', { method: 'POST', body: JSON.stringify(input) }),
	updateRole: (id: string, input: { name: string; description: string; grants: RoleGrant[] }) =>
		request<Role>(`/api/v1/roles/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
	deleteRole: (id: string) => request(`/api/v1/roles/${id}`, { method: 'DELETE' }),
	replaceUserRoles: (id: string, role_ids: string[]) =>
		request(`/api/v1/users/${id}/roles`, { method: 'PUT', body: JSON.stringify({ role_ids }) }),
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
    request<{ source_user_id: string; target_user_id: string; packages: number; hosts: number; service_instances: number; models: number }>(`/api/v1/users/${id}/transfer`, { method: 'POST', body: JSON.stringify({ target_user_id }) }),
  listHosts: (ownerId?: string) => request<Host[]>(withOwner('/api/v1/hosts', ownerId)),
  createHost: (input: HostInput, ownerId?: string) =>
    request<Host>(withOwner('/api/v1/hosts', ownerId), { method: 'POST', body: JSON.stringify(input) }),
  updateHost: (id: string, input: HostInput) =>
    request<Host>(`/api/v1/hosts/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteHost: (id: string) => request<null>(`/api/v1/hosts/${id}`, { method: 'DELETE' }),
  validateDraftHost: (input: HostInput) => validationRequest('/api/v1/hosts/validate', input),
  validateHost: (id: string) => validationRequest(`/api/v1/hosts/${id}/validate`),
  importHosts: (document: unknown, ownerId?: string) =>
    request<{ created: number; updated: number; total: number }>(withOwner('/api/v1/hosts/import', ownerId), { method: 'POST', body: JSON.stringify(document) }),
  createServiceInstance: (input: ServiceInstanceInput, ownerId?: string) =>
    request<ServiceInstance>(withOwner('/api/v1/services', ownerId), {
      method: 'POST',
      body: JSON.stringify(input),
    }),
  updateServiceInstance: (id: string, input: ServiceInstanceInput) =>
    request<ServiceInstance>(`/api/v1/services/${id}`, {
      method: 'PUT',
      body: JSON.stringify(input),
    }),
  deleteServiceInstance: (id: string) =>
    request<null>(`/api/v1/services/${id}`, { method: 'DELETE' }),
  replaceServiceInstanceTags: (id: string, tag_ids: string[]) =>
    request<ServiceInstance>(`/api/v1/services/${id}/tags`, { method: 'PUT', body: JSON.stringify({ tag_ids }) }),
  listTags: (ownerId?: string) => request<ResourceTag[]>(withOwner('/api/v1/tags', ownerId)),
  createTag: (input: { group_name: string; value: string }, ownerId?: string) =>
    request<ResourceTag>(withOwner('/api/v1/tags', ownerId), { method: 'POST', body: JSON.stringify(input) }),
  updateTag: (id: string, input: { group_name: string; value: string }) =>
    request<ResourceTag>(`/api/v1/tags/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteTag: (id: string) => request(`/api/v1/tags/${id}`, { method: 'DELETE' }),
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
  uploadPackageWithProgress: ({
    serviceType,
    file,
    note,
    ownerId,
    onProgress,
    onUploaded,
    signal,
  }: {
    serviceType: string
    file: File
    note?: string
    ownerId?: string
    onProgress: (loaded: number, total: number) => void
    onUploaded: () => void
    signal?: AbortSignal
  }) => {
    const body = new FormData()
    if (note !== undefined) body.append('note', note)
    body.append('file', file)
    return uploadRequest<PackageInfo>(
      withOwner(`/api/v1/service-types/${encodeURIComponent(serviceType)}/package`, ownerId),
      body,
      onProgress,
      onUploaded,
      signal,
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
	createModelUpload: (input: { name: string; host_id: string; target_dir: string; original_filename: string; total_bytes: number }, ownerId?: string) =>
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
  getServiceConfig: (serviceInstanceId: string) =>
    request<ServiceConfig>(`/api/v1/services/${serviceInstanceId}/config`),
  updateServiceConfig: ({ serviceInstanceId, content }: { serviceInstanceId: string; content: string }) =>
    request<ServiceConfig>(`/api/v1/services/${serviceInstanceId}/config`, {
      method: 'PUT',
      body: JSON.stringify({ content }),
    }),
  previewServiceConfig: ({ serviceInstanceId, content }: { serviceInstanceId: string; content: string }) =>
    request<ServiceConfigPreview>(`/api/v1/services/${serviceInstanceId}/config/preview`, { method: 'POST', body: JSON.stringify({ content }) }),
  listServiceConfigRevisions: (serviceInstanceId: string) =>
    request<ServiceConfigRevision[]>(`/api/v1/services/${serviceInstanceId}/config/revisions`),
  getServiceConfigRevision: (serviceInstanceId: string, revisionId: string) =>
    request<ServiceConfigRevision>(`/api/v1/services/${serviceInstanceId}/config/revisions/${revisionId}`),
  rollbackServiceConfigRevision: (serviceInstanceId: string, revisionId: string) =>
    request<ServiceConfigRevision>(`/api/v1/services/${serviceInstanceId}/config/revisions/${revisionId}/rollback`, { method: 'POST', body: '{}' }),
  startOperation: (serviceInstanceId: string, action: 'install' | 'start' | 'stop' | 'reset') =>
    request<{ operation_id: string; status: string }>(
      `/api/v1/services/${serviceInstanceId}/${action}`,
      { method: 'POST', body: '{}' },
    ),
  checkHealth: (serviceInstanceId: string) =>
    request(`/api/v1/services/${serviceInstanceId}/health-check`, {
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
  listAuditEvents: async (filter: AuditFilter) => {
    const page = await request<{ items: WireAuditEvent[]; next_cursor: string }>(`/api/v1/audit-events?${auditParams(filter)}`)
    return { ...page, items: page.items.map(normalizeAuditEvent) }
  },
  auditSummary: (filter: AuditFilter) =>
    request<AuditSummary>(`/api/v1/audit-events/summary?${auditParams(filter, false)}`),
  getAuditEvent: async (id: string) => normalizeAuditEvent(await request<WireAuditEvent>(`/api/v1/audit-events/${id}`)),
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

function normalizeAuditEvent(event: WireAuditEvent): AuditEvent {
  return { ...event, actor_roles: event.actor_roles ?? [] }
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
