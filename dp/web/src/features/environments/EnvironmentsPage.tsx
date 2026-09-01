import { useEffect, useMemo, useState } from 'react'
import {
  CheckCircleFilled,
  CloudDownloadOutlined,
  CloudUploadOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
  TagsOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { mainTablePagination, modalTablePagination } from '../../components/ListPagination'
import {
  App,
  Button,
  Card,
  Descriptions,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  Upload,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { UploadProps } from 'antd'
import { api } from '../../api/client'
import type {
  Environment,
  EnvironmentInput,
  ResourceTag,
  ValidationResponse,
  ValidationStage,
} from '../../types'
import { useAuth } from '../../app/AuthContext'
import { TagFilter, TagList } from '../tags/TagControls'
import { useSearchParams } from 'react-router-dom'

const defaults: EnvironmentInput = {
  name: '',
  ip: '',
  ssh_user: 'aaron',
  ssh_port: 22,
  ssh_password: '',
  install_dir: '/opt/dp-demo',
  service_type: '',
  note: '',
  tag_ids: [],
}

const stageLabels: Record<ValidationStage['name'], string> = {
  connect: 'SSH 连接',
  directory: '安装目录',
  upload: '上传权限',
}

export function EnvironmentsPage() {
  const { message, modal } = App.useApp()
  const queryClient = useQueryClient()
  const { ownerId, user, users, hasAll } = useAuth()
	const environmentReadAll = hasAll('environment.read')
	const environmentWriteAll = hasAll('environment.write')
	const tagReadAll = hasAll('tag.read')
	const tagWriteAll = hasAll('tag.write')
  const [search] = useSearchParams()
  const [form] = Form.useForm<EnvironmentInput>()
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editing, setEditing] = useState<Environment | null>(null)
  const [validation, setValidation] = useState<ValidationResponse | null>(null)
  const [keyword, setKeyword] = useState('')
  const [createOwner, setCreateOwner] = useState<string>(user.id)
  const [tagFilter, setTagFilter] = useState<string[]>(() => search.getAll('tag_id'))
  const [tagManagerOpen, setTagManagerOpen] = useState(false)
  const [tagOwner, setTagOwner] = useState(user.id)
  const [editingTag, setEditingTag] = useState<ResourceTag>()
  const [tagForm] = Form.useForm<{ group_name: string; value: string }>()

  const environmentsQuery = useQuery({
    queryKey: ['environments', ownerId, tagFilter],
    queryFn: () => api.listEnvironments(ownerId, tagFilter),
  })
  const tagsQuery = useQuery({ queryKey: ['tags', ownerId], queryFn: () => api.listTags(ownerId) })
  const allTagsQuery = useQuery({ queryKey: ['tags', 'all'], queryFn: () => api.listTags(), enabled: tagReadAll })
  const tagCatalog = tagReadAll ? (allTagsQuery.data ?? []) : (tagsQuery.data ?? [])
  useEffect(() => {
    if (!tagsQuery.data) return
    const allowed = new Set(tagsQuery.data.map((tag) => tag.id))
    setTagFilter((current) => current.filter((id) => allowed.has(id)))
  }, [ownerId, tagsQuery.data])
  const serviceTypesQuery = useQuery({
    queryKey: ['service-types', ownerId],
    queryFn: () => api.listServiceTypes(ownerId),
  })
  const createServiceTypesQuery = useQuery({ queryKey: ['service-types', createOwner], queryFn: () => api.listServiceTypes(createOwner), enabled: environmentWriteAll && Boolean(createOwner) })

  const saveMutation = useMutation({
    mutationFn: async (values: EnvironmentInput) => {
      if (editing) return api.updateEnvironment(editing.id, values)
      return api.createEnvironment(values, environmentWriteAll ? createOwner : undefined)
    },
    onSuccess: () => {
      message.success(editing ? '环境信息已更新' : '环境已创建')
      setDrawerOpen(false)
      setEditing(null)
      void queryClient.invalidateQueries({ queryKey: ['environments'] })
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const validateSavedMutation = useMutation({
    mutationFn: api.validateEnvironment,
    onSuccess: (result) => {
      setValidation(result)
      void queryClient.invalidateQueries({ queryKey: ['environments'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const validateDraftMutation = useMutation({
    mutationFn: (input: EnvironmentInput) => api.validateDraft(input, environmentWriteAll ? createOwner : undefined),
    onSuccess: setValidation,
    onError: (error: Error) => message.error(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: api.deleteEnvironment,
    onSuccess: () => {
      message.success('环境已删除')
      void queryClient.invalidateQueries({ queryKey: ['environments'] })
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const importMutation = useMutation({
    mutationFn: (document: unknown) => api.importEnvironments(document, hasAll('environment.import') ? ownerId : undefined),
    onSuccess: (result) => {
      message.success(`导入完成：新增 ${result.created}，覆盖 ${result.overwritten}`)
      void queryClient.invalidateQueries({ queryKey: ['environments'] })
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const saveTagMutation = useMutation({
    mutationFn: (values: { group_name: string; value: string }) => editingTag ? api.updateTag(editingTag.id, values) : api.createTag(values, tagWriteAll ? tagOwner : undefined),
    onSuccess: () => {
      message.success(editingTag ? '标签已更新' : '标签已创建')
      setEditingTag(undefined)
      tagForm.resetFields()
      void queryClient.invalidateQueries({ queryKey: ['tags'] })
      void queryClient.invalidateQueries({ queryKey: ['environments'] })
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })
  const deleteTagMutation = useMutation({
    mutationFn: api.deleteTag,
    onSuccess: () => {
      message.success('标签已删除并解除关联')
      void queryClient.invalidateQueries({ queryKey: ['tags'] })
      void queryClient.invalidateQueries({ queryKey: ['environments'] })
      void queryClient.invalidateQueries({ queryKey: ['services'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const openCreate = () => {
    setEditing(null)
    const suggestedOwner = user.id
    setCreateOwner(suggestedOwner)
    form.setFieldsValue({
      ...defaults,
      service_type: (environmentWriteAll ? (suggestedOwner === createOwner ? createServiceTypesQuery.data : undefined) : serviceTypesQuery.data)?.[0]?.name ?? '',
      tag_ids: [],
    })
    setDrawerOpen(true)
  }

  const openEdit = (environment: Environment) => {
    setEditing(environment)
    form.setFieldsValue({
      name: environment.name,
      ip: environment.ip,
      ssh_user: environment.ssh_user,
      ssh_port: environment.ssh_port,
      ssh_password: '',
      install_dir: environment.install_dir,
      service_type: environment.service_type,
      note: environment.note,
      tag_ids: (environment.tags ?? []).map((tag) => tag.id).filter((id): id is string => Boolean(id)),
    })
    setDrawerOpen(true)
  }

  const submit = async () => {
    const values = await form.validateFields()
    saveMutation.mutate(values)
  }

  const validateDraft = async () => {
    const values = await form.validateFields()
    validateDraftMutation.mutate(values)
  }

  const importProps: UploadProps = {
    accept: '.json,application/json',
    showUploadList: false,
    beforeUpload: async (file) => {
      try {
        const document = JSON.parse(await file.text()) as unknown
        importMutation.mutate(document)
      } catch {
        message.error('导入文件不是有效的 JSON')
      }
      return Upload.LIST_IGNORE
    },
  }

  const keywordNormalized = keyword.trim().toLowerCase()
  const environments = (environmentsQuery.data ?? []).filter(
    (item) =>
      !keywordNormalized ||
      item.service_type.toLowerCase().includes(keywordNormalized) ||
      item.ip.toLowerCase().includes(keywordNormalized),
  )

  const columns = useMemo<ColumnsType<Environment>>(
    () => [
      {
        title: '环境',
        dataIndex: 'name',
        key: 'name',
        width: 180,
        render: (name: string, record) => (
          <div className="table-stacked-cell">
            <Typography.Text strong ellipsis={{ tooltip: name }}>{name}</Typography.Text>
            <Typography.Text type="secondary" className="cell-caption" ellipsis={{ tooltip: record.service_type }}>
              {record.service_type}
            </Typography.Text>
          </div>
        ),
      },
      {
        title: '所属账号',
        key: 'owner',
        width: 150,
        render: (_, record) => (
          <Tag>{users.find((item) => item.id === record.owner_id)?.username ?? (record.owner_id === user.id ? user.username : record.owner_id)}</Tag>
        ),
      },
      {
        title: '服务器',
        key: 'server',
        width: 180,
        render: (_, record) => (
          <div className="table-stacked-cell">
            <Typography.Text code className="table-mono-line">{record.ip}</Typography.Text>
            <Typography.Text type="secondary" className="cell-caption">
              {record.ssh_user}@{record.ssh_port}
            </Typography.Text>
          </div>
        ),
      },
      {
        title: '备注',
        dataIndex: 'note',
        key: 'note',
        width: 200,
        ellipsis: true,
        render: (value: string) =>
          value ? (
            <Tooltip title={value} placement="topLeft">
              <span>{value}</span>
            </Tooltip>
          ) : (
            '-'
          ),
      },
      {
        title: '架构',
        dataIndex: 'arch',
        key: 'arch',
        width: 110,
        render: (arch: string) => arch || '-',
      },
      { title: '标签', key: 'tags', width: 230, render: (_, record) => <TagList tags={record.tags} /> },
      {
        title: '安装目录',
        dataIndex: 'install_dir',
        key: 'install_dir',
        width: 260,
        render: (value: string) => <Typography.Text code className="table-ellipsis-line" ellipsis={{ tooltip: value }}>{value}</Typography.Text>,
      },
      {
        title: 'SSH 校验',
        key: 'validation',
        width: 150,
        render: (_, record) =>
          record.last_validation_at ? (
            <Space direction="vertical" size={0}>
              <Tag color="success" icon={<CheckCircleFilled />}>已通过</Tag>
              <span className="cell-caption">{formatTime(record.last_validation_at)}</span>
            </Space>
          ) : (
            <Tag>未校验</Tag>
          ),
      },
      {
        title: '安装状态',
        dataIndex: 'installed',
        key: 'installed',
        width: 110,
        render: (installed: boolean) => (
          <Tag color={installed ? 'blue' : 'default'}>{installed ? '已安装' : '未安装'}</Tag>
        ),
      },
      {
        title: '操作',
        key: 'actions',
        width: 280,
        fixed: 'right',
        render: (_, record) => (
          <Space>
            <Button
              size="small"
              icon={<SafetyCertificateOutlined />}
              loading={validateSavedMutation.isPending && validateSavedMutation.variables === record.id}
              onClick={() => validateSavedMutation.mutate(record.id)}
            >
              校验
            </Button>
            <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(record)}>
              编辑
            </Button>
            <Tooltip title={record.installed ? '已安装的环境请先在服务管理页重置后再删除' : undefined}>
              <span>
                <Button
                  size="small"
                  danger
                  icon={<DeleteOutlined />}
                  disabled={record.installed}
                  loading={deleteMutation.isPending && deleteMutation.variables === record.id}
                  onClick={() => {
                    modal.confirm({
                      title: `删除环境 ${record.name}？`,
                      content: `所属账号：${users.find((item) => item.id === record.owner_id)?.username ?? record.owner_id}。删除后环境和实例配置将被移除，操作历史保留，远端服务器文件不会被清理。`,
                      okText: '确认删除',
                      cancelText: '取消',
                      okButtonProps: { danger: true },
                      onOk: () => deleteMutation.mutate(record.id),
                    })
                  }}
                >
                  删除
                </Button>
              </span>
            </Tooltip>
          </Space>
        ),
      },
    ],
    [
      deleteMutation.isPending,
      deleteMutation.variables,
      modal,
      user.id,
      user.username,
      users,
      validateSavedMutation.isPending,
      validateSavedMutation.variables,
    ],
  )

  return (
    <div className="page">
      <div className="page-heading">
        <div>
          <div className="page-eyebrow">Infrastructure</div>
          <Typography.Title level={2}>环境管理</Typography.Title>
          <Typography.Paragraph type="secondary">
            集中管理目标服务器、SSH 凭据与部署路径，在交付前完成连接和权限校验。
          </Typography.Paragraph>
        </div>
        <Space>
          <TagFilter tags={tagsQuery.data ?? []} value={tagFilter} onChange={setTagFilter} />
          <Input.Search
            allowClear
            placeholder="搜索 IP 或服务类型"
            style={{ width: 240 }}
            onChange={(event) => setKeyword(event.target.value)}
          />
          <Button
            icon={<CloudDownloadOutlined />}
            href={ownerId ? `/api/v1/environments/export?owner_id=${encodeURIComponent(ownerId)}` : '/api/v1/environments/export'}
            disabled={!environmentsQuery.data?.length}
          >
            导出 JSON
          </Button>
          <Upload {...importProps}>
            <Button
              icon={<CloudUploadOutlined />}
              loading={importMutation.isPending}
            >
              导入 JSON
            </Button>
          </Upload>
          <Button icon={<TagsOutlined />} onClick={() => setTagManagerOpen(true)}>标签管理</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建环境
          </Button>
        </Space>
      </div>

      <Card
        className="content-card table-card"
        title={
          <div>
            <Typography.Text strong>服务器环境</Typography.Text>
            <span className="section-caption">SSH 连接与部署目标</span>
          </div>
        }
        styles={{ body: { padding: 0 } }}
      >
        <Table
          rowKey="id"
          columns={columns}
          dataSource={environments}
          loading={environmentsQuery.isLoading}
          tableLayout="fixed"
          scroll={{ x: 1850 }}
          pagination={mainTablePagination}
          locale={{ emptyText: '暂无环境，请先创建目标服务器环境' }}
        />
      </Card>

      <Drawer
        title={editing ? '编辑环境' : '新建环境'}
        size={520}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        destroyOnHidden
        footer={
          <div className="drawer-footer">
            {!editing && (
              <Button
                icon={<SafetyCertificateOutlined />}
                loading={validateDraftMutation.isPending}
                onClick={() => void validateDraft()}
              >
                校验 SSH
              </Button>
            )}
            <Space>
              <Button onClick={() => setDrawerOpen(false)}>取消</Button>
              <Button type="primary" loading={saveMutation.isPending} onClick={() => void submit()}>
                保存
              </Button>
            </Space>
          </div>
        }
      >
        <Form form={form} layout="vertical" initialValues={defaults} requiredMark="optional">
          {!editing && environmentWriteAll && <Form.Item label="所属账号" extra="新环境及其服务配置将归所选账号管理。"><Select value={createOwner} options={users.filter((item) => item.enabled).map((item) => ({ value: item.id, label: item.username }))} onChange={(value) => { setCreateOwner(value); form.setFieldValue('service_type', undefined); form.setFieldValue('tag_ids', []) }} /></Form.Item>}
          <Form.Item
            name="name"
            label="环境名称"
            rules={[{ required: true, message: '请输入环境名称' }]}
          >
            <Input placeholder="例如：生产服务器 A" maxLength={80} />
          </Form.Item>
          <div className="form-grid">
            <Form.Item
              name="ip"
              label="服务器 IP"
              rules={[{ required: true, message: '请输入服务器 IP' }]}
            >
              <Input placeholder="10.0.0.8" />
            </Form.Item>
            <Form.Item name="service_type" label="服务类型" rules={[{ required: true }]}>
              <Select
                placeholder="请先上传对应类型的安装包"
                loading={editing || !environmentWriteAll ? serviceTypesQuery.isLoading : createServiceTypesQuery.isLoading}
                options={((editing || !environmentWriteAll ? serviceTypesQuery.data : createServiceTypesQuery.data) ?? []).map((item) => ({
                  value: item.name,
                  label: item.display_name,
                }))}
              />
            </Form.Item>
          </div>
          <div className="form-grid">
            <Form.Item
              name="ssh_user"
              label="SSH 用户"
              rules={[{ required: true, message: '请输入 SSH 用户' }]}
            >
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item
              name="ssh_port"
              label="SSH 端口"
              rules={[{ required: true, message: '请输入 SSH 端口' }]}
            >
              <InputNumber min={1} max={65535} style={{ width: '100%' }} />
            </Form.Item>
          </div>
          <Form.Item
            name="ssh_password"
            label="SSH 密码"
            extra={editing ? '留空表示保持原密码' : '密码仅以密文形式保存'}
            rules={editing ? [] : [{ required: true, message: '请输入 SSH 密码' }]}
          >
            <Input.Password autoComplete="new-password" placeholder={editing ? '保持原密码' : ''} />
          </Form.Item>
          <Form.Item
            name="install_dir"
            label="服务安装目录"
            rules={[
              { required: true, message: '请输入安装目录' },
              { pattern: /^\//, message: '安装目录必须是绝对路径' },
            ]}
          >
            <Input placeholder="/opt/dp-demo" />
          </Form.Item>
          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} maxLength={200} showCount placeholder="可选，最多 200 字" />
          </Form.Item>
          <Form.Item name="tag_ids" label="资源标签" extra="标签只用于组织和筛选，不会改变账号权限。">
            <Select mode="multiple" allowClear maxCount={20} placeholder="选择环境阶段、区域、项目等标签" options={tagCatalog.filter((tag) => tag.owner_id === (editing?.owner_id ?? createOwner)).map((tag) => ({ value: tag.id, label: `${tag.group_name} / ${tag.value}` }))} />
          </Form.Item>
        </Form>
      </Drawer>

      <Modal title="标签管理" width={820} open={tagManagerOpen} onCancel={() => { setTagManagerOpen(false); setEditingTag(undefined); tagForm.resetFields() }} footer={null} destroyOnHidden>
        <Typography.Paragraph type="secondary">标签按账号隔离，以“分组 / 值”组织环境和服务。删除标签只解除关联。</Typography.Paragraph>
        <Form form={tagForm} layout="inline" onFinish={(values) => saveTagMutation.mutate(values)} style={{ marginBottom: 18 }}>
          {!editingTag && tagWriteAll && <Form.Item><Select style={{ width: 150 }} value={tagOwner} onChange={setTagOwner} options={users.filter((item) => item.enabled).map((item) => ({ value: item.id, label: item.username }))} /></Form.Item>}
          <Form.Item name="group_name" rules={[{ required: true, message: '请输入分组' }]}><Input maxLength={32} placeholder="分组，如环境阶段" /></Form.Item>
          <Form.Item name="value" rules={[{ required: true, message: '请输入标签值' }]}><Input maxLength={32} placeholder="值，如生产" /></Form.Item>
          <Form.Item><Space><Button type="primary" htmlType="submit" loading={saveTagMutation.isPending}>{editingTag ? '保存修改' : '新增标签'}</Button>{editingTag && <Button onClick={() => { setEditingTag(undefined); tagForm.resetFields() }}>取消编辑</Button>}</Space></Form.Item>
        </Form>
        <Table<ResourceTag> rowKey="id" size="small" pagination={modalTablePagination} scroll={{ x: 720 }} dataSource={tagCatalog.filter((tag) => !tagReadAll || !ownerId || tag.owner_id === ownerId)} columns={[
          { title: '所属账号', dataIndex: 'owner_username', width: 130, render: (value, item) => value || users.find((userItem) => userItem.id === item.owner_id)?.username || item.owner_id },
          { title: '分组', dataIndex: 'group_name', width: 160, ellipsis: true },
          { title: '值', dataIndex: 'value', width: 170, ellipsis: true },
          { title: '关联环境', dataIndex: 'environment_count', width: 100, render: (value) => `${value} 个` },
          { title: '操作', width: 160, fixed: 'right', render: (_, item) => <Space><Button size="small" onClick={() => { setEditingTag(item); tagForm.setFieldsValue({ group_name: item.group_name, value: item.value }) }}>编辑</Button><Button size="small" danger loading={deleteTagMutation.isPending && deleteTagMutation.variables === item.id} onClick={() => modal.confirm({ title: `删除标签 ${item.group_name} / ${item.value}？`, content: `将解除 ${item.environment_count} 个环境的关联，不会删除资源。`, okButtonProps: { danger: true }, onOk: () => deleteTagMutation.mutate(item.id) })}>删除</Button></Space> },
        ]} />
      </Modal>

      <Modal
        title="SSH 校验结果"
        open={validation !== null}
        onCancel={() => setValidation(null)}
        footer={<Button type="primary" onClick={() => setValidation(null)}>知道了</Button>}
      >
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          {(validation?.data.stages ?? []).map((stage) => (
            <Card size="small" key={stage.name}>
              <Space>
                <Tag color={stage.success ? 'success' : 'error'}>
                  {stage.success ? '通过' : '失败'}
                </Tag>
                <Typography.Text strong>{stageLabels[stage.name]}</Typography.Text>
                <Typography.Text type="secondary">{stage.message}</Typography.Text>
              </Space>
            </Card>
          ))}
          {validation?.validation_error && (
            <Typography.Text type="danger">{validation.validation_error}</Typography.Text>
          )}
          {validation?.data.fingerprint && (
            <Descriptions size="small" column={1}>
              <Descriptions.Item label="SSH 主机指纹">
                <Typography.Text copyable code>{validation.data.fingerprint}</Typography.Text>
              </Descriptions.Item>
            </Descriptions>
          )}
        </Space>
      </Modal>
    </div>
  )
}

function formatTime(value?: string) {
  if (!value) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}
