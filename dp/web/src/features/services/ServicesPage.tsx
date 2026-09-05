import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import {
  CheckCircleFilled,
  ClockCircleOutlined,
  CloseCircleFilled,
  CodeOutlined,
  DeploymentUnitOutlined,
  FileTextOutlined,
  HistoryOutlined,
	LoadingOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  RollbackOutlined,
	PlusOutlined,
	EditOutlined,
	DeleteOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { mainTablePagination, modalTablePagination } from '../../components/ListPagination'
import {
  App,
  Button,
  Card,
	Drawer,
  Empty,
	Form,
  Input,
  Modal,
	Select,
  Space,
  Spin,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { api } from '../../api/client'
import type { Permission, Service, ServiceConfigPreview, ServiceConfigRevision, ServiceInstance, ServiceInstanceInput } from '../../types'
import { OperationModal } from '../operations/OperationModal'
import { ServiceLogModal } from './ServiceLogModal'
import { useAuth } from '../../app/AuthContext'
import { useSearchParams } from 'react-router-dom'
import { TagFilter, TagList } from '../tags/TagControls'

const JsonEditor = lazy(() =>
  import('./JsonEditor').then((module) => ({ default: module.JsonEditor })),
)
const ConfigDiffEditor = lazy(() => import('./JsonEditor').then((module) => ({ default: module.ConfigDiffEditor })))

export function ServicesPage() {
  const { message, modal } = App.useApp()
  const queryClient = useQueryClient()
  const { ownerId, user, users, can, hasAll } = useAuth()
  const [search] = useSearchParams()
  const highlightedServiceInstanceId = search.get('service_instance_id')
  const [operationId, setOperationId] = useState<string | null>(null)
  const [operationOpen, setOperationOpen] = useState(false)
  const [configOpen, setConfigOpen] = useState(false)
  const [configServiceInstance, setConfigServiceInstance] = useState<Service['service_instance'] | null>(null)
  const [configContent, setConfigContent] = useState('')
  const [configFormat, setConfigFormat] = useState<'json' | 'yaml'>('json')
  const [configPath, setConfigPath] = useState('')
  const [configInherited, setConfigInherited] = useState(false)
  const [configOriginalContent, setConfigOriginalContent] = useState('')
  const [configPackageContent, setConfigPackageContent] = useState('')
  const [configPackageFilename, setConfigPackageFilename] = useState('')
  const [configPackageChanged, setConfigPackageChanged] = useState(false)
  const [configPackageUpdated, setConfigPackageUpdated] = useState(false)
  const [configView, setConfigView] = useState<'compare' | 'edit'>('edit')
  const [configLoading, setConfigLoading] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [logService, setLogService] = useState<Service | null>(null)
  const [configPreview, setConfigPreview] = useState<ServiceConfigPreview>()
  const [historyOpen, setHistoryOpen] = useState(false)
  const [revisionDetail, setRevisionDetail] = useState<ServiceConfigRevision>()
  const [tagFilter, setTagFilter] = useState<string[]>(() => search.getAll('tag_id'))
	const [instanceOpen, setInstanceOpen] = useState(false)
	const [editingInstance, setEditingInstance] = useState<ServiceInstance>()
	const [instanceForm] = Form.useForm<ServiceInstanceInput>()

  const packagesQuery = useQuery({
    queryKey: ['packages', ownerId],
    queryFn: () => api.listPackages(ownerId),
  })
  const servicesQuery = useQuery({
    queryKey: ['services', ownerId, tagFilter],
    queryFn: () => api.listServices(ownerId, tagFilter),
    refetchInterval: 10_000,
  })
  const tagsQuery = useQuery({ queryKey: ['tags', ownerId], queryFn: () => api.listTags(ownerId) })
	const hostsQuery = useQuery({ queryKey: ['hosts', ownerId], queryFn: () => api.listHosts(ownerId) })
	const serviceTypesQuery = useQuery({ queryKey: ['service-types', ownerId], queryFn: () => api.listServiceTypes(ownerId) })
	const saveInstance = useMutation({
		mutationFn: (input: ServiceInstanceInput) => editingInstance ? api.updateServiceInstance(editingInstance.id, input) : api.createServiceInstance(input, ownerId),
		onSuccess: () => { message.success(editingInstance ? '服务实例已更新' : '服务实例已创建'); setInstanceOpen(false); void queryClient.invalidateQueries({ queryKey: ['services'] }) },
		onError: (error: Error) => message.error(error.message),
	})
	const deleteInstance = useMutation({
		mutationFn: api.deleteServiceInstance,
		onSuccess: () => { message.success('服务实例已删除'); void queryClient.invalidateQueries({ queryKey: ['services'] }) },
		onError: (error: Error) => message.error(error.message),
	})
	const openInstance = (instance?: ServiceInstance) => {
		setEditingInstance(instance)
		instanceForm.setFieldsValue(instance ? { host_id: instance.host_id, name: instance.name, install_dir: instance.install_dir, service_type: instance.service_type, note: instance.note, tag_ids: instance.tags?.map((tag) => tag.id!).filter(Boolean) } : { host_id: undefined!, name: '', install_dir: '/opt/services/service-name', service_type: undefined!, note: '', tag_ids: [] })
		setInstanceOpen(true)
	}
  useEffect(() => {
    if (!tagsQuery.data) return
    const allowed = new Set(tagsQuery.data.map((tag) => tag.id))
    setTagFilter((current) => current.filter((id) => allowed.has(id)))
  }, [ownerId, tagsQuery.data])
  const packageTypes = useMemo(
    () => new Set((packagesQuery.data ?? []).map((item) => `${item.owner_id}:${item.service_type}`)),
    [packagesQuery.data],
  )

  const actionMutation = useMutation({
    mutationFn: ({
      id,
      action,
    }: {
      id: string
      action: 'install' | 'start' | 'stop' | 'reset'
    }) =>
      api.startOperation(id, action),
    onSuccess: (result) => {
      setOperationId(result.operation_id)
      setOperationOpen(true)
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const healthMutation = useMutation({
    mutationFn: api.checkHealth,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['services'] }),
    onError: (error: Error) => message.error(error.message),
  })

  const saveConfigMutation = useMutation({
    mutationFn: api.updateServiceConfig,
    onSuccess: (config) => {
      setConfigInherited(false)
      setConfigContent(config.content)
      message.success(configServiceInstance?.installed ? '配置已保存并同步到服务器' : '配置已保存')
      setConfigOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })
  const previewConfigMutation = useMutation({
    mutationFn: api.previewServiceConfig,
    onSuccess: (preview) => preview.changed ? setConfigPreview(preview) : message.info('配置内容没有变化，无需保存'),
    onError: (error: Error) => message.error(error.message),
  })
  const revisionsQuery = useQuery({
    queryKey: ['service-config-revisions', configServiceInstance?.id],
    queryFn: () => api.listServiceConfigRevisions(configServiceInstance!.id),
    enabled: historyOpen && Boolean(configServiceInstance),
  })
  const rollbackMutation = useMutation({
    mutationFn: ({ serviceInstanceId, revisionId }: { serviceInstanceId: string; revisionId: string }) => api.rollbackServiceConfigRevision(serviceInstanceId, revisionId),
    onSuccess: async () => {
      message.success('配置已回滚，并创建了新的修订')
      setRevisionDetail(undefined)
      setHistoryOpen(false)
      if (configServiceInstance) {
        const config = await api.getServiceConfig(configServiceInstance.id)
        setConfigContent(config.content); setConfigOriginalContent(config.content); setConfigFormat(config.format); setConfigPath(config.path); setConfigInherited(false)
        setConfigPackageContent(config.package_content); setConfigPackageFilename(config.package_filename)
        setConfigPackageChanged(config.package_changed); setConfigPackageUpdated(config.package_updated)
        setConfigView(config.package_changed || config.package_updated ? 'compare' : 'edit')
      }
      void queryClient.invalidateQueries({ queryKey: ['service-config-revisions'] }); void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const openConfig = async (service: Service) => {
    setConfigServiceInstance(service.service_instance)
    setConfigOpen(true)
    setConfigLoading(true)
    try {
      const config = await queryClient.fetchQuery({
        queryKey: ['service-config', service.service_instance.id],
        queryFn: () => api.getServiceConfig(service.service_instance.id),
        staleTime: 0,
      })
      setConfigContent(config.content)
      setConfigOriginalContent(config.content)
      setConfigFormat(config.format)
      setConfigPath(config.path)
      setConfigInherited(config.inherited)
      setConfigPackageContent(config.package_content)
      setConfigPackageFilename(config.package_filename)
      setConfigPackageChanged(config.package_changed)
      setConfigPackageUpdated(config.package_updated)
      setConfigView(config.package_changed || config.package_updated ? 'compare' : 'edit')
    } catch (error) {
      message.error((error as Error).message)
      setConfigOpen(false)
    } finally {
      setConfigLoading(false)
    }
  }

  const saveConfig = () => {
    if (!configServiceInstance) return
    if (configFormat === 'json') {
      try {
        JSON.parse(configContent)
      } catch (error) {
        message.error(`JSON 格式错误：${(error as Error).message}`)
        return
      }
    }
    previewConfigMutation.mutate({ serviceInstanceId: configServiceInstance.id, content: configContent })
  }

  const commitConfig = () => {
    if (!configServiceInstance || !configPreview) return
    const save = () => saveConfigMutation.mutate({ serviceInstanceId: configServiceInstance.id, content: configPreview.proposed_content }, { onSuccess: () => setConfigPreview(undefined) })
    if (hasAll('service.config.write') && configServiceInstance.owner_id !== user.id) {
      const owner = users.find((item) => item.id === configServiceInstance.owner_id)?.username ?? configServiceInstance.owner_id
      modal.confirm({ title: `修改 ${owner} 的服务配置？`, content: `目标服务实例：${configServiceInstance.name}（${configServiceInstance.host.ip}）。保存后配置归属不变，并记录高风险审计。`, okText: '确认保存', onOk: save })
      return
    }
    save()
  }

  const completeOperation = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ['services'] })
    void queryClient.invalidateQueries({ queryKey: ['services'] })
  }, [queryClient])

  const columns = useMemo<ColumnsType<Service>>(
    () => [
      {
        title: '服务实例',
        key: 'service',
        width: 210,
        render: (_, record) => (
          <div className="service-cell">
            <div className="service-cell-icon"><DeploymentUnitOutlined /></div>
            <div>
              <Typography.Text strong>{record.service_instance.name}</Typography.Text>
              <div className="cell-caption">{record.service_instance.service_type}</div>
            </div>
          </div>
        ),
      },
      {
        title: '所属账号',
        key: 'owner',
        width: 150,
        render: (_, record) => (
          <Tag>{users.find((item) => item.id === record.service_instance.owner_id)?.username ?? (record.service_instance.owner_id === user.id ? user.username : record.service_instance.owner_id)}</Tag>
        ),
      },
      {
        title: 'API 端口',
        dataIndex: 'api_port',
        width: 110,
        render: (value?: number) => (
          <Typography.Text className="server-address">{value ?? '-'}</Typography.Text>
        ),
      },
      { title: '标签', key: 'tags', width: 220, render: (_, record) => <TagList tags={record.service_instance.tags} /> },
      {
        title: '服务器与安装目录',
        key: 'server',
        width: 280,
        render: (_, record) => (
          <div className="table-stacked-cell">
            <Typography.Text className="server-address">
              {record.service_instance.host.ip}
            </Typography.Text>
            <Typography.Text type="secondary" className="cell-caption table-ellipsis-line" ellipsis={{ tooltip: record.service_instance.install_dir }}>
              {record.service_instance.install_dir}
            </Typography.Text>
          </div>
        ),
      },
      {
        title: '部署状态',
        key: 'installed',
        width: 118,
        render: (_, record) => {
          const lastOperation = record.last_operation
          const installFailed =
            !record.service_instance.installed &&
            lastOperation?.action === 'install' &&
            (lastOperation.status === 'failed' ||
              lastOperation.status === 'timed_out' ||
              lastOperation.status === 'interrupted')
          if (installFailed) {
            const detail = lastOperation.error_message ||
              (lastOperation.finished_at ? formatTime(lastOperation.finished_at) : undefined)
            return (
              <Tooltip title={detail}>
                <Tag color="error" icon={<CloseCircleFilled />}>安装失败</Tag>
              </Tooltip>
            )
          }
          return (
            <Tooltip title={record.service_instance.installed_at ? formatTime(record.service_instance.installed_at) : undefined}>
              {record.service_instance.installed ? (
                <Tag color="processing" icon={<CheckCircleFilled />}>已安装</Tag>
              ) : (
                <Tag>未安装</Tag>
              )}
            </Tooltip>
          )
        },
      },
      {
        title: '健康状态',
        key: 'health',
        width: 150,
        render: (_, record) => (
          <Space size={4}>
            <Tooltip title={healthTooltip(record.health)}>
              {healthTag(record)}
            </Tooltip>
            {record.service_instance.installed && can('service.health', record.service_instance.owner_id) && (
              <Tooltip title="立即检查">
                <Button
                  type="text"
                  size="small"
                  icon={<ReloadOutlined />}
                  loading={healthMutation.isPending && healthMutation.variables === record.service_instance.id}
                  onClick={() => healthMutation.mutate(record.service_instance.id)}
                />
              </Tooltip>
            )}
          </Space>
        ),
      },
      {
        title: '操作',
        key: 'actions',
        width: 390,
        fixed: 'right',
        render: (_, record) => {
          const { service_instance: serviceInstance, busy } = record
		  const lastOperation = record.last_operation
          const pending = actionMutation.isPending && actionMutation.variables?.id === serviceInstance.id
          const execute = (action: 'install' | 'start' | 'stop') => {
            const run = () => actionMutation.mutate({ id: serviceInstance.id, action })
            if (hasAll(serviceActionPermission(action)) && serviceInstance.owner_id !== user.id) {
              const owner = users.find((item) => item.id === serviceInstance.owner_id)?.username ?? serviceInstance.owner_id
              modal.confirm({ title: `${action === 'install' ? '安装' : action === 'start' ? '启动' : '停止'}其他账号的服务？`, content: `目标：${serviceInstance.name}（所属账号：${owner}）。操作将记录高风险审计。`, okText: '确认执行', onOk: run })
            } else run()
          }
		  if (busy && lastOperation?.id && ['queued', 'running'].includes(lastOperation.status) && can('operation.read', serviceInstance.owner_id)) {
			return <Button size="small" type="primary" ghost icon={<LoadingOutlined />} onClick={() => { setOperationId(lastOperation.id); setOperationOpen(true) }}>查看进度</Button>
		  }
          return (
            <div className="row-actions">
			  {can('service.write', serviceInstance.owner_id) && <Button size="small" icon={<EditOutlined />} disabled={busy} onClick={() => openInstance(serviceInstance)}>编辑</Button>}
			  {can('service.delete', serviceInstance.owner_id) && <Button danger size="small" icon={<DeleteOutlined />} disabled={busy || serviceInstance.installed} onClick={() => modal.confirm({ title: `删除服务实例“${serviceInstance.name}”？`, content: '已安装实例需要先执行重置。', okButtonProps: { danger: true }, onOk: () => deleteInstance.mutateAsync(serviceInstance.id) })}>删除</Button>}
              {can('service.config.read', serviceInstance.owner_id) && <Button
                size="small"
                icon={<CodeOutlined />}
                disabled={busy || !packageTypes.has(`${serviceInstance.owner_id}:${serviceInstance.service_type}`)}
                onClick={() => void openConfig(record)}
              >
                配置
              </Button>}
              {serviceInstance.installed ? (
                <>
                  {can('service.start', serviceInstance.owner_id) && <Button
                    size="small"
                    type="primary"
                    ghost
                    icon={<PlayCircleOutlined />}
                    disabled={busy}
                    loading={pending && actionMutation.variables?.action === 'start'}
                    onClick={() => execute('start')}
                  >
                    启动
                  </Button>}
                  {can('service.stop', serviceInstance.owner_id) && <Button
                    size="small"
                    danger
                    type="text"
                    icon={<PauseCircleOutlined />}
                    disabled={busy}
                    loading={pending && actionMutation.variables?.action === 'stop'}
                    onClick={() => execute('stop')}
                  >
                    停止
                  </Button>}
                  {can('service.log.read', serviceInstance.owner_id) && <Tooltip title={record.health.status === 'ok' ? undefined : '服务未运行'}>
                    <span>
                      <Button
                        size="small"
                        type="text"
                        icon={<FileTextOutlined />}
                        disabled={busy || record.health.status !== 'ok'}
                        onClick={() => setLogService(record)}
                      >
                        日志
                      </Button>
                    </span>
                  </Tooltip>}
                </>
              ) : (
                can('service.install', serviceInstance.owner_id) && <Button
                  size="small"
                  type="primary"
                  disabled={busy || !packageTypes.has(`${serviceInstance.owner_id}:${serviceInstance.service_type}`)}
                  loading={pending && actionMutation.variables?.action === 'install'}
                  onClick={() => execute('install')}
                >
                  安装
                </Button>
              )}
              {can('service.reset', serviceInstance.owner_id) && <Button
                size="small"
                type="text"
                icon={<RollbackOutlined />}
                disabled={busy}
                loading={pending && actionMutation.variables?.action === 'reset'}
                onClick={() => {
                  modal.confirm({
                    title: `重置 ${serviceInstance.name}？`,
                    content: serviceInstance.installed ? (
                      <div className="reset-confirm">
                        {hasAll('service.reset') && serviceInstance.owner_id !== user.id && <p><strong>所属账号：{users.find((item) => item.id === serviceInstance.owner_id)?.username ?? serviceInstance.owner_id}</strong></p>}
                        <p>如果服务最近没有成功停止，系统会先执行 stop.sh。</p>
                        <p>重置后服务变为“未安装”，可修改 IP、服务类型或安装目录并重新安装。</p>
                        <p>远端安装目录和业务文件不会被删除。</p>
                      </div>
                    ) : (
                      <div className="reset-confirm">
                        {hasAll('service.reset') && serviceInstance.owner_id !== user.id && <p><strong>所属账号：{users.find((item) => item.id === serviceInstance.owner_id)?.username ?? serviceInstance.owner_id}</strong></p>}
                        <p>将尝试停止远端服务并清理安装标记，可用于强制停止安装失败后反复重启的服务。</p>
                        <p>远端安装目录和业务文件不会被删除。</p>
                      </div>
                    ),
                    okText: '确认重置',
                    cancelText: '取消',
                    okButtonProps: { danger: true },
                    onOk: () =>
                      actionMutation.mutate({
                        id: serviceInstance.id,
                        action: 'reset',
                      }),
                  })
                }}
              >
                重置
              </Button>}
            </div>
          )
        },
      },
    ],
    [
      actionMutation.isPending,
      actionMutation.variables,
      healthMutation.isPending,
      healthMutation.variables,
	  can,
	  hasAll,
      modal,
      packageTypes,
      user.id,
      user.username,
      users,
    ],
  )

  const allServices = servicesQuery.data ?? []
  const keywordNormalized = keyword.trim().toLowerCase()
  const services = keywordNormalized
    ? allServices.filter(
        (item) =>
          item.service_instance.host.ip.toLowerCase().includes(keywordNormalized) ||
          item.service_instance.service_type.toLowerCase().includes(keywordNormalized),
      )
    : allServices
  const installedCount = services.filter((item) => item.service_instance.installed).length
  const runningCount = services.filter(
    (item) => item.service_instance.installed && item.health.status === 'ok',
  ).length
  const busyCount = services.filter((item) => item.busy).length

  return (
    <div className="page">
      <div className="page-heading service-page-heading">
        <div>
          <div className="page-eyebrow">Service operations</div>
          <Typography.Title level={2}>服务管理</Typography.Title>
          <Typography.Paragraph type="secondary">
            安装、启停和配置远端服务，持续观测部署结果与实时健康状态。
          </Typography.Paragraph>
        </div>
		{can('service.write', ownerId) && <Button type="primary" size="large" icon={<PlusOutlined />} onClick={() => openInstance()}>创建实例</Button>}
      </div>

      <div className="metric-strip">
        <MetricCard label="服务实例" value={services.length} suffix="个" tone="blue" />
        <MetricCard label="已安装" value={installedCount} suffix="个" tone="indigo" />
        <MetricCard label="运行正常" value={runningCount} suffix="个" tone="green" />
        <MetricCard label="执行中" value={busyCount} suffix="个" tone="orange" />
      </div>

      {!packagesQuery.isLoading && packagesQuery.data?.length === 0 && (
        <div className="inline-alert">
          <ClockCircleOutlined />
          <span>尚未上传安装包，暂时无法执行安装。请先前往“安装包管理”上传文件。</span>
        </div>
      )}

      <Card
        className="content-card table-card service-table-card"
        title={
          <div>
            <Typography.Text strong>服务实例</Typography.Text>
            <span className="section-caption">每 10 秒自动更新健康状态</span>
          </div>
        }
		extra={
		  <Space className="service-list-tools" wrap>
			<TagFilter tags={tagsQuery.data ?? []} value={tagFilter} onChange={setTagFilter} />
			<Input.Search
			  allowClear
			  placeholder="搜索 IP 或服务类型"
			  style={{ width: 240 }}
			  onChange={(event) => setKeyword(event.target.value)}
			/>
			<Button icon={<ReloadOutlined />} onClick={() => void servicesQuery.refetch()}>
			  刷新状态
			</Button>
		  </Space>
		}
        styles={{ body: { padding: 0 } }}
      >
        <Table
          rowKey={(record) => record.service_instance.id}
          rowClassName={(record) => record.service_instance.id === highlightedServiceInstanceId ? 'target-row' : ''}
          columns={columns}
          dataSource={services}
          loading={servicesQuery.isLoading}
          tableLayout="fixed"
          scroll={{ x: 1640 }}
          pagination={mainTablePagination}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
				description="暂无服务实例，请选择已注册主机并创建实例"
              />
            ),
          }}
        />
      </Card>

	  <Drawer width={560} title={editingInstance ? '编辑服务实例' : '创建服务实例'} open={instanceOpen} onClose={() => setInstanceOpen(false)} extra={<Space><Button onClick={() => setInstanceOpen(false)}>取消</Button><Button type="primary" loading={saveInstance.isPending} onClick={() => instanceForm.validateFields().then((value) => saveInstance.mutate(value))}>保存</Button></Space>}>
		<Form form={instanceForm} layout="vertical">
		  <Form.Item name="name" label="实例名称" rules={[{ required: true, message: '请输入实例名称' }]}><Input placeholder="例如：vLLM-Qwen3-主实例" /></Form.Item>
		  <Form.Item name="host_id" label="目标主机" rules={[{ required: true, message: '请选择目标主机' }]}><Select showSearch optionFilterProp="label" options={(hostsQuery.data ?? []).map((host) => ({ value: host.id, label: `${host.name} · ${host.ip}` }))} /></Form.Item>
		  <Form.Item name="service_type" label="服务类型" rules={[{ required: true, message: '请选择服务类型' }]}><Select options={(serviceTypesQuery.data ?? []).map((type) => ({ value: type.name, label: type.display_name }))} /></Form.Item>
		  <Form.Item name="install_dir" label="安装目录" rules={[{ required: true }, { pattern: /^\//, message: '请输入绝对路径' }]}><Input placeholder="/opt/services/vllm-qwen3" /></Form.Item>
		  <Form.Item name="tag_ids" label="资源标签"><Select mode="multiple" allowClear options={(tagsQuery.data ?? []).map((tag) => ({ value: tag.id, label: `${tag.group_name} / ${tag.value}` }))} /></Form.Item>
		  <Form.Item name="note" label="备注"><Input.TextArea rows={3} maxLength={200} showCount /></Form.Item>
		</Form>
	  </Drawer>

      <OperationModal
        operationId={operationId}
        open={operationOpen}
        onClose={() => setOperationOpen(false)}
        onComplete={completeOperation}
      />

      <ServiceLogModal
        service={logService}
        open={Boolean(logService)}
        onClose={() => setLogService(null)}
      />

      <Modal
        rootClassName="config-editor-modal"
        title={
          <div className="config-modal-title">
            <span className="config-modal-title-icon"><CodeOutlined /></span>
            <span><strong>服务配置</strong><small>{configServiceInstance ? `${configServiceInstance.name} · ${configServiceInstance.host.ip}` : '实例配置'}</small></span>
          </div>
        }
        width={1180}
        open={configOpen}
        onCancel={() => setConfigOpen(false)}
        footer={
          <div className="config-modal-footer">
            <Button type="text" icon={<HistoryOutlined />} disabled={!configServiceInstance || configInherited} onClick={() => setHistoryOpen(true)}>配置历史</Button>
            <Space><Button onClick={() => setConfigOpen(false)}>取消</Button><Button type="primary" loading={previewConfigMutation.isPending} disabled={configLoading} onClick={saveConfig}>检查并预览</Button></Space>
          </div>
        }
      >
        <div className="config-modal-meta">
          <div>
            <Typography.Text strong>{configServiceInstance?.name}</Typography.Text>
            <Typography.Text type="secondary"> · {configPath}</Typography.Text>
          </div>
          <Space>
            {configPackageUpdated && <Tag color="red">安装包已有新版本</Tag>}
            {configPackageChanged && <Tag color="orange">当前配置与新包模板不同</Tag>}
            {configInherited && <Tag color="gold">来自安装包模板，保存后转为实例独立配置</Tag>}
          </Space>
        </div>
        {configServiceInstance?.installed && (
          <div className="inline-alert compact">
            保存后将立即覆盖服务器上的配置文件，服务是否需要重启由配置项决定。
          </div>
        )}
        {!configLoading && (configPackageChanged || configPackageUpdated) && (
          <div className="config-compare-toolbar">
            <Typography.Text type="secondary">新包配置：{configPackageFilename}</Typography.Text>
            <Space>
              <Button type={configView === 'compare' ? 'primary' : 'default'} onClick={() => setConfigView('compare')}>高亮对比</Button>
              <Button type={configView === 'edit' ? 'primary' : 'default'} onClick={() => setConfigView('edit')}>编辑当前配置</Button>
              <Button disabled={!configPackageChanged} onClick={() => { setConfigContent(configPackageContent); setConfigView('edit') }}>采用新包配置</Button>
            </Space>
          </div>
        )}
        <div className="editor-frame">
          {configLoading ? (
            <div className="editor-loading"><Spin tip="正在加载配置模板…" /></div>
          ) : (configPackageChanged || configPackageUpdated) && configView === 'compare' ? (
            <Suspense fallback={<div className="editor-loading">正在加载差异编辑器…</div>}>
              <div className="config-diff-labels"><span>当前使用的配置</span><span>新安装包配置</span></div>
              <ConfigDiffEditor height="520px" language={configFormat} original={configOriginalContent} modified={configPackageContent} />
            </Suspense>
          ) : (
            <Suspense fallback={<div className="editor-loading">正在加载配置编辑器…</div>}>
              <JsonEditor
                height="520px"
                language={configFormat}
                value={configContent}
                onChange={(value) => setConfigContent(value ?? '')}
              />
            </Suspense>
          )}
        </div>
      </Modal>

      <Modal title="确认配置变更" width={1100} open={Boolean(configPreview)} onCancel={() => setConfigPreview(undefined)} okText="确认保存" confirmLoading={saveConfigMutation.isPending} onOk={commitConfig} destroyOnHidden>
        <Typography.Paragraph type="secondary">左侧为当前有效配置，右侧为待保存配置。确认后将创建不可变配置修订{configServiceInstance?.installed ? '并同步到远端服务器' : ''}。</Typography.Paragraph>
        {configPreview && <Suspense fallback={<div className="editor-loading">正在加载差异编辑器…</div>}><ConfigDiffEditor height="520px" language={configPreview.format} original={configPreview.current_content} modified={configPreview.proposed_content} /></Suspense>}
      </Modal>

      <Modal title={`配置历史 · ${configServiceInstance?.name ?? ''}`} width={940} open={historyOpen} footer={null} onCancel={() => setHistoryOpen(false)} destroyOnHidden>
        <Table<ServiceConfigRevision> rowKey="id" loading={revisionsQuery.isLoading} dataSource={revisionsQuery.data ?? []} pagination={modalTablePagination} columns={[
          { title: '修订', width: 150, render: (_, item) => <Space><Typography.Text code>{item.id.slice(0, 8)}</Typography.Text>{item.current && <Tag color="success">当前</Tag>}</Space> },
          { title: '来源', dataIndex: 'source', width: 100, render: (value: string) => value === 'rollback' ? <Tag color="gold">回滚</Tag> : <Tag>保存</Tag> },
          { title: '操作者', dataIndex: 'created_by_username', width: 130, render: (value: string) => value || '升级迁移' },
          { title: '端口', dataIndex: 'port', width: 90 },
          { title: '时间', dataIndex: 'created_at', width: 180, render: formatTime },
          { title: '操作', width: 170, render: (_, item) => <Space><Button onClick={async () => { try { setRevisionDetail(await api.getServiceConfigRevision(item.service_instance_id, item.id)) } catch (error) { message.error((error as Error).message) } }}>查看</Button><Button disabled={item.current} onClick={() => modal.confirm({ title: '回滚到该修订？', content: configServiceInstance?.installed ? '将先写入远端配置，成功后创建新的回滚修订。' : '将创建新的回滚修订，不会删除后续历史。', onOk: () => rollbackMutation.mutate({ serviceInstanceId: item.service_instance_id, revisionId: item.id }) })}>回滚</Button></Space> },
        ]} />
      </Modal>

      <Modal title={`配置修订 · ${revisionDetail?.id.slice(0, 8) ?? ''}`} width={900} open={Boolean(revisionDetail)} footer={<Button onClick={() => setRevisionDetail(undefined)}>关闭</Button>} onCancel={() => setRevisionDetail(undefined)} destroyOnHidden>
        {revisionDetail?.content !== undefined && <Suspense fallback={<div className="editor-loading">正在加载配置…</div>}><JsonEditor height="520px" language={revisionDetail.format} value={revisionDetail.content} onChange={() => undefined} readOnly /></Suspense>}
      </Modal>
    </div>
  )
}

function MetricCard({
  label,
  value,
  suffix,
  tone,
}: {
  label: string
  value: number
  suffix: string
  tone: 'blue' | 'indigo' | 'green' | 'orange'
}) {
  return (
    <div className={`metric-item metric-${tone}`}>
      <div className="metric-label">
        <span className="metric-dot" />
        {label}
      </div>
      <div className="metric-value">
        {value}<small>{suffix}</small>
      </div>
    </div>
  )
}

function healthTag(record: Service) {
  if (!record.service_instance.installed) {
    return <Tag>未安装</Tag>
  }
  switch (record.health.status) {
    case 'ok':
      return <Tag color="success">运行正常</Tag>
    case 'error':
      return <Tag color="error">运行异常</Tag>
    default:
      return <Tag>状态未知</Tag>
  }
}

function serviceActionPermission(action: 'install' | 'start' | 'stop'): Permission {
	if (action === 'install') return 'service.install'
	if (action === 'start') return 'service.start'
	return 'service.stop'
}

function healthTooltip(health: Service['health']) {
  return formatTime(health.checked_at)
}

function formatTime(value?: string | null) {
  if (!value) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}
