export type ServiceType = string

export type PermissionScope = 'own' | 'all'

export type Permission =
  | 'dashboard.read'
  | 'account.read'
  | 'account.create'
  | 'account.update'
  | 'account.delete'
  | 'account.assign_roles'
  | 'account.transfer'
  | 'role.read'
  | 'role.create'
  | 'role.update'
  | 'role.delete'
  | 'package.read'
  | 'package.write'
  | 'package.delete'
  | 'environment.read'
  | 'environment.write'
  | 'environment.delete'
  | 'environment.validate'
  | 'environment.import'
  | 'environment.export'
  | 'tag.read'
  | 'tag.write'
  | 'model.read'
  | 'model.upload'
  | 'model.delete'
  | 'service.read'
  | 'service.config.read'
  | 'service.config.write'
  | 'service.install'
  | 'service.start'
  | 'service.stop'
  | 'service.reset'
  | 'service.health'
  | 'service.log.read'
  | 'operation.read'
  | 'audit.read'
  | 'audit.export'
  | 'notification.read'
  | 'notification.update'
  | 'communication.read'
  | 'communication.create'
  | 'communication.reply'
  | 'communication.manage'

export interface RoleRef {
  id: string
  key: string
  name: string
}

export interface PermissionDefinition {
  key: Permission
  resource: string
  action: string
  description: string
  scoped: boolean
}

export interface RoleGrant {
  permission: Permission
  scope: PermissionScope
}

export interface Role {
  id: string
  key: string
  name: string
  description: string
  system: boolean
  grants: RoleGrant[]
  member_count: number
}

export interface User {
  id: string
  username: string
  roles: RoleRef[]
  permissions: Partial<Record<Permission, PermissionScope>>
  enabled: boolean
  must_change_password: boolean
  is_initial_admin: boolean
  created_by?: string
  created_by_username?: string
  created_at: string
  updated_at: string
}

export interface Session {
  id: string
  source_ip: string
  user_agent: string
  created_at: string
  last_seen_at: string
  expires_at: string
  current: boolean
}

export interface ServiceTypeInfo {
  name: string
  display_name: string
  package_format: string
}

export interface Environment {
  id: string
  owner_id: string
  owner_username?: string
  name: string
  ip: string
  arch: string
  ssh_user: string
  ssh_port: number
  install_dir: string
  service_type: ServiceType
  note: string
  installed: boolean
  installed_at?: string
  installed_package_sha256?: string
  health_port?: number
  last_validation_at?: string
  created_at: string
  updated_at: string
  has_password: boolean
  tags?: ResourceTagRef[]
}

export interface EnvironmentInput {
  name: string
  ip: string
  ssh_user: string
  ssh_port: number
  ssh_password?: string
  install_dir: string
  service_type: ServiceType
  note?: string
  tag_ids?: string[]
}

export interface ResourceTagRef {
  id?: string
  group_name: string
  value: string
}

export interface ResourceTag extends ResourceTagRef {
  id: string
  owner_id: string
  owner_username?: string
  environment_count: number
	model_count: number
  created_at: string
  updated_at: string
}

export interface ValidationStage {
  name: 'connect' | 'directory' | 'upload'
  success: boolean
  message: string
}

export interface ValidationResult {
  fingerprint?: string
  stages: ValidationStage[] | null
}

export interface ValidationResponse {
  data: ValidationResult
  validation_error?: string
}

export interface PackageInfo {
  owner_id: string
  owner_username?: string
  service_type: ServiceType
  current_version_id: string
  version_count: number
  referenced_environment_count: number
  original_filename: string
  sha256: string
  size_bytes: number
  config_port: number
  note: string
  uploaded_at: string
  updated_at: string
}

export interface PackageVersion {
  id: string
  owner_id: string
  service_type: ServiceType
  original_filename: string
  sha256: string
  size_bytes: number
  config_port: number
  config_format: 'json' | 'yaml'
  config_path: string
  validation_status: 'passed'
  note: string
  uploaded_by?: string
  uploaded_by_username: string
  uploaded_at: string
  current: boolean
  referenced_environment_count: number
}

export interface HealthResult {
  status: 'ok' | 'error' | 'unknown'
  checked_at?: string | null
}

export interface OperationSummary {
  action: 'install' | 'start' | 'stop' | 'reset'
  status: OperationStatus
  error_message?: string
  finished_at?: string
}

export interface Service {
  environment: Environment
  health: HealthResult
  service_port?: number
  busy: boolean
  last_operation?: OperationSummary
}

export interface ServiceConfig {
  environment_id: string
  content: string
  format: 'json' | 'yaml'
  path: string
  port: number
  inherited: boolean
  current_revision_id?: string
  updated_at?: string
  package_content: string
  package_version_id: string
  package_filename: string
  package_changed: boolean
  package_updated: boolean
}

export interface ServiceConfigPreview {
  current_content: string
  proposed_content: string
  changed: boolean
  format: 'json' | 'yaml'
  path: string
  port: number
}

export interface ServiceConfigRevision {
  id: string
  environment_id: string
  content?: string
  format: 'json' | 'yaml'
  path: string
  port: number
  source: 'manual' | 'rollback'
  restored_from_id?: string
  created_by?: string
  created_by_username: string
  created_at: string
  current: boolean
}

export type OperationStatus =
  | 'queued'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'timed_out'
  | 'interrupted'

export interface Operation {
  id: string
  environment_id: string
  request_id?: string
  actor_user_id?: string
  actor_username?: string
  owner_id?: string
  owner_username?: string
  environment_name?: string
  environment_ip?: string
  service_type?: string
  action: 'install' | 'start' | 'stop' | 'reset'
  status: OperationStatus
  stage: string
  exit_code?: number
  error_code?: string
  error_message?: string
  created_at: string
  started_at?: string
  finished_at?: string
  tags?: ResourceTagRef[]
}

export type ModelStatus = 'uploading' | 'deploying' | 'ready' | 'failed' | 'deleting' | 'deleted'

export interface ModelTask {
  id: string
  model_id: string
  owner_id: string
	actor_user_id?: string
	actor_username?: string
  action: 'deploy' | 'delete'
  status: OperationStatus
  stage: string
  progress: number
  error_code?: string
  error_message?: string
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface Model {
  id: string
  owner_id: string
  owner_username?: string
  environment_id: string
  environment_name: string
  environment_ip: string
  name: string
  source: 'offline' | 'modelscope' | 'huggingface'
  target_dir: string
  original_filename: string
  size_bytes: number
  expanded_size_bytes: number
  file_count: number
  sha256?: string
  status: ModelStatus
  error_message?: string
  created_by_username?: string
  created_at: string
  updated_at: string
  ready_at?: string
  latest_task?: ModelTask
}

export interface ModelUploadCreated {
  model: Model
  upload_id: string
  offset: number
  chunk_bytes: number
  expires_at: string
}

export interface UserDetail extends User {
  package_count: number
  environment_count: number
  installed_service_count: number
  recent_operation_count: number
  active_session_count: number
  login_failure_count: number
  high_risk_count: number
  last_login_at?: string
  last_activity_at?: string
  last_source_ip?: string
}

export interface Notification {
  id: string
  created_at: string
  risk_level: 'normal' | 'high'
  category: 'security' | 'account' | 'resource' | 'operation' | 'system'
  title: string
  message: string
  target_type?: string
  target_id?: string
  target_label?: string
  owner_id?: string
  owner_username?: string
  operation_id?: string
  link: string
  read: boolean
  resolved: boolean
  read_at?: string
  resolved_at?: string
}

export type CommunicationStatus = 'open' | 'closed'
export type CommunicationMessageType = 'admin_message' | 'user_receipt' | 'system_closed' | 'system_reopened'

export interface CommunicationRecipient {
  user_id: string
  username: string
  roles: string[]
  read_at?: string
}

export interface CommunicationMessage {
  id: string
  type: CommunicationMessageType
  sender_user_id?: string
  sender_username: string
  sender_roles: string[]
  content: string
  created_at: string
  recipients: CommunicationRecipient[]
}

export interface CommunicationResource {
  id: string
  resource_type: 'package' | 'environment' | 'service'
  resource_id?: string
  resource_key?: string
  owner_id?: string
  owner_username: string
  resource_label: string
  service_type: string
  environment_name: string
  environment_ip: string
  available: boolean
  link?: string
}

export interface Communication {
  id: string
  target_user_id?: string
  target_username: string
  title: string
  status: CommunicationStatus
  reopen_count: number
  created_by?: string
  created_by_username: string
  closed_by_username: string
  closed_at?: string
  last_reopened_by_username: string
  last_reopened_at?: string
  created_at: string
  updated_at: string
  unread_count: number
  last_message?: string
  user_read_at?: string
  resources: CommunicationResource[]
  messages?: CommunicationMessage[]
}

export interface CommunicationResourceInput {
  resource_type: 'package' | 'environment' | 'service'
  resource_id?: string
  resource_key?: string
}

export interface CommunicationFilter {
  target_user_id?: string
  status?: CommunicationStatus
  unread?: boolean
  keyword?: string
  cursor?: string
  limit?: number
}

export interface DashboardMetrics {
  users: number
  enabled_users: number
  disabled_users: number
  packages: number
  environments: number
  installed_services: number
  running_services: number
  active_operations: number
  failed_operations_24h: number
  login_failures_24h: number
  unvalidated_environments: number
  stale_validation_environments: number
  unhealthy_installed_services: number
  high_risk_audits_24h: number
  unread_notifications: number
  unread_communications: number
}

export interface AdminDashboard { metrics: DashboardMetrics; communications: Communication[]; notifications: Notification[] }

export interface OperationFilter {
  actor_id?: string
  owner_id?: string
  action?: string
  status?: string
  from?: string
  to?: string
  keyword?: string
  cursor?: string
  limit?: number
  tag_id?: string[]
}

export interface OperationEvent {
  seq: number
  time: string
  type: 'log' | 'state'
  stream?: 'stdout' | 'stderr' | 'system'
  message?: string
  status?: OperationStatus
  stage?: string
}

export interface AuditEvent {
  id: string
  occurred_at: string
  category: 'authentication' | 'authorization' | 'account' | 'package' | 'environment' | 'service' | 'model' | 'communication' | 'audit'
  action: string
  outcome: 'success' | 'failure' | 'denied'
  risk_level: 'normal' | 'high'
  actor_user_id?: string
  actor_username: string
  actor_roles: string[]
  owner_id?: string
  owner_username?: string
  target_type?: string
  target_id?: string
  target_label?: string
  request_id: string
  operation_id?: string
  source_ip?: string
  user_agent?: string
  error_code?: string
  changes?: Record<string, unknown>
}

export interface AuditSummary {
  total: number
  failures: number
  login_failures: number
  high_risk: number
}

export interface AuditFilter {
  from: string
  to: string
  actor_id?: string
  owner_id?: string
  category?: string
  action?: string
  outcome?: string
  source_ip?: string
  keyword?: string
  cursor?: string
  limit?: number
}
