import { useState } from 'react'
import {
  CheckCircleFilled,
  CloudUploadOutlined,
  DeleteOutlined,
  FileZipOutlined,
  HistoryOutlined,
  InboxOutlined,
} from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  App,
  Button,
  Card,
  Empty,
  Input,
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
import { api } from '../../api/client'
import type { PackageInfo, PackageVersion } from '../../types'
import { formatBytes } from '../../utils/format'
import { useAuth } from '../../app/AuthContext'
import { mainTablePagination } from '../../components/ListPagination'
import { usePackageUpload } from './PackageUploadContext'

const serviceTypePattern = /^[a-z][a-z0-9-]{0,62}$/

export function PackagesPage() {
  const { message, modal } = App.useApp()
  const queryClient = useQueryClient()
  const packageUpload = usePackageUpload()
  const { ownerId, user, users, can, hasAll } = useAuth()
	const packageWriteAll = hasAll('package.write')
  const [uploadOpen, setUploadOpen] = useState(false)
  const [serviceType, setServiceType] = useState('')
  const [replacingType, setReplacingType] = useState('')
  const [replacingOwner, setReplacingOwner] = useState<string | undefined>()
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [note, setNote] = useState('')
  const [originalNote, setOriginalNote] = useState('')
  const [uploadOwner, setUploadOwner] = useState<string>()
  const [keyword, setKeyword] = useState('')
  const [versionPackage, setVersionPackage] = useState<PackageInfo>()

  const packagesQuery = useQuery({
    queryKey: ['packages', ownerId],
    queryFn: () => api.listPackages(ownerId),
  })
  const noteMutation = useMutation({
    mutationFn: api.uploadPackage,
    onSuccess: (_, variables) => {
      message.success(
        `${variables.serviceType} 备注已更新`,
      )
      setUploadOpen(false)
      setSelectedFile(null)
      setNote('')
      void queryClient.invalidateQueries({ queryKey: ['packages'] })
      void queryClient.invalidateQueries({ queryKey: ['service-types'] })
    },
    onError: (error: Error) => message.error(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: api.deletePackage,
    onSuccess: (_, variables) => {
      message.success(`${variables.serviceType} 安装包已删除`)
      void queryClient.invalidateQueries({ queryKey: ['packages'] })
      void queryClient.invalidateQueries({ queryKey: ['service-types'] })
    },
    onError: (error: Error) => message.error(error.message),
  })
  const versionsQuery = useQuery({
    queryKey: ['package-versions', versionPackage?.owner_id, versionPackage?.service_type],
    queryFn: () => api.listPackageVersions({ serviceType: versionPackage!.service_type, ownerId: versionPackage!.owner_id }),
    enabled: Boolean(versionPackage),
  })
  const activateMutation = useMutation({
    mutationFn: api.activatePackageVersion,
    onSuccess: () => { message.success('当前版本已切换，仅影响后续安装'); void queryClient.invalidateQueries({ queryKey: ['packages'] }); void queryClient.invalidateQueries({ queryKey: ['package-versions'] }) },
    onError: (error: Error) => message.error(error.message),
  })
  const deleteVersionMutation = useMutation({
    mutationFn: api.deletePackageVersion,
    onSuccess: () => { message.success('历史版本已删除'); void queryClient.invalidateQueries({ queryKey: ['package-versions'] }); void queryClient.invalidateQueries({ queryKey: ['packages'] }) },
    onError: (error: Error) => message.error(error.message),
  })

  const allPackages = packagesQuery.data ?? []
  const keywordNormalized = keyword.trim().toLowerCase()
  const packages = keywordNormalized
    ? allPackages.filter((item) =>
        item.service_type.toLowerCase().includes(keywordNormalized),
      )
    : allPackages
  const openUpload = (type = '', recordOwner?: string) => {
    const currentNote = allPackages.find((item) => item.service_type === type && item.owner_id === recordOwner)?.note ?? ''
    setServiceType(type)
    setReplacingType(type)
    setReplacingOwner(recordOwner)
    setUploadOwner(recordOwner ?? user.id)
    setSelectedFile(null)
    setNote(currentNote)
    setOriginalNote(currentNote)
    setUploadOpen(true)
  }
  const submitUpload = () => {
    const normalized = serviceType.trim().toLowerCase()
    if (!serviceTypePattern.test(normalized)) {
      message.error('服务类型须以小写字母开头，只能包含小写字母、数字和连字符')
      return
    }
    const noteTrimmed = note.trim()
    if (!selectedFile) {
      if (!replacingType) {
        message.error('请选择 .tar.gz 安装包')
        return
      }
      if (noteTrimmed === originalNote.trim()) {
        message.info('没有需要更新的内容')
        return
      }
    }
    const input = {
      serviceType: normalized,
      note: replacingType || noteTrimmed ? noteTrimmed : undefined,
      ownerId: replacingType ? replacingOwner : uploadOwner,
    }
    if (!selectedFile) {
      noteMutation.mutate(input)
      return
    }
    try {
      packageUpload.start({ ...input, file: selectedFile })
      setUploadOpen(false)
      setSelectedFile(null)
      setNote('')
      message.warning('安装包已转入后台上传，可以继续添加任务或切换 DP 页面；请勿刷新或关闭浏览器')
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  const columns: ColumnsType<PackageInfo> = [
      {
        title: '服务类型',
        dataIndex: 'service_type',
        width: 205,
        render: (value: string) => (
          <div className="package-type-cell">
            <div className="service-cell-icon package"><FileZipOutlined /></div>
            <div className="package-type-copy">
              <Tooltip title={value} placement="topLeft">
                <Typography.Text strong className="package-type-name">{value}</Typography.Text>
              </Tooltip>
              <div><Tag color="success" icon={<CheckCircleFilled />}>校验通过</Tag></div>
            </div>
          </div>
        ),
      },
      {
        title: '所属账号',
        key: 'owner',
        width: 130,
        render: (_, record) => {
          const username = users.find((item) => item.id === record.owner_id)?.username ?? (record.owner_id === user.id ? user.username : record.owner_id)
          return <Tooltip title={username}><Tag className="package-owner-tag">{username}</Tag></Tooltip>
        },
      },
      {
        title: '当前安装包',
        key: 'package',
        width: 300,
        render: (_, record) => (
          <div className="package-file-cell">
            <Tooltip title={record.original_filename} placement="topLeft">
              <Typography.Text strong className="package-file-name">{record.original_filename}</Typography.Text>
            </Tooltip>
            <div className="package-file-meta">
              <span>SHA-256</span>
              <Tooltip title={record.sha256} placement="bottomLeft">
                <code>{record.sha256.slice(0, 16)}…</code>
              </Tooltip>
            </div>
          </div>
        ),
      },
      {
        title: '备注',
        dataIndex: 'note',
        width: 175,
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
        title: '默认健康端口',
        dataIndex: 'config_port',
        width: 125,
        render: (value: number) => <Typography.Text code>{value}</Typography.Text>,
      },
      {
        title: '大小',
        dataIndex: 'size_bytes',
        width: 95,
        render: (value: number) => <span className="package-size">{formatBytes(value)}</span>,
      },
      {
        title: '更新时间',
        dataIndex: 'updated_at',
        width: 165,
        render: (value: string) => <span className="package-updated-at">{formatTime(value)}</span>,
      },
      {
        title: '操作',
        key: 'action',
        width: 250,
        fixed: 'right',
        render: (_, record) => (
          <div className="row-actions package-row-actions">
            <Button size="small" icon={<HistoryOutlined />} onClick={() => setVersionPackage(record)}>版本 {record.version_count}</Button>
            {can('package.write', record.owner_id) && <Button size="small" icon={<CloudUploadOutlined />} onClick={() => openUpload(record.service_type, record.owner_id)}>
              更新
            </Button>}
            {can('package.delete', record.owner_id) && <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              loading={deleteMutation.isPending && deleteMutation.variables?.serviceType === record.service_type && deleteMutation.variables?.ownerId === record.owner_id}
              onClick={() => {
                modal.confirm({
                  title: `删除 ${record.service_type} 安装包？`,
                  content: `所属账号：${users.find((item) => item.id === record.owner_id)?.username ?? record.owner_id}。删除后该服务类型将无法继续安装新服务，已安装的服务不受影响。`,
                  okText: '确认删除',
                  cancelText: '取消',
                  okButtonProps: { danger: true },
                  onOk: () => deleteMutation.mutate({ serviceType: record.service_type, ownerId: record.owner_id }),
                })
              }}
            >
              删除
            </Button>}
          </div>
        ),
      },
    ]

  return (
    <div className="page">
      <div className="page-heading">
        <div>
          <div className="page-eyebrow">Package registry</div>
          <Typography.Title level={2}>安装包管理</Typography.Title>
          <Typography.Paragraph type="secondary">
            维护可追溯的服务交付版本；上传后自动校验，历史版本可安全切换和回滚。
          </Typography.Paragraph>
        </div>
        <Space>
          <Input.Search
            allowClear
            placeholder="按服务类型搜索"
            style={{ width: 240 }}
            onChange={(event) => setKeyword(event.target.value)}
          />
          {can('package.write') && <Button
            type="primary"
            size="large"
            icon={<CloudUploadOutlined />}
            onClick={() => openUpload()}
          >
            上传安装包
          </Button>}
        </Space>
      </div>

      <div className="metric-strip package-metrics">
        <PackageMetric icon={<FileZipOutlined />} label="服务类型" value={allPackages.length} suffix="种" />
        <PackageMetric icon={<CheckCircleFilled />} label="校验通过" value={allPackages.length} suffix="个" />
        <PackageMetric icon={<CloudUploadOutlined />} label="当前安装包" value={allPackages.length} suffix="个" />
        <PackageMetric
          icon={<InboxOutlined />}
          label="存储占用"
          value={formatBytes(allPackages.reduce((total, item) => total + item.size_bytes, 0))}
        />
      </div>

      <Card
        className="content-card table-card"
        title={
          <div>
            <Typography.Text strong>安装包列表</Typography.Text>
            <span className="section-caption">共 {packages.length} 个服务类型</span>
          </div>
        }
        styles={{ body: { padding: 0 } }}
      >
        <Table
          className="package-table"
          rowKey={(record) => `${record.owner_id}:${record.service_type}`}
          columns={columns}
          dataSource={packages}
          loading={packagesQuery.isLoading}
          tableLayout="fixed"
          scroll={{ x: 1445 }}
          pagination={mainTablePagination}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无安装包，请先上传并定义服务类型"
              />
            ),
          }}
        />
      </Card>

      <Modal title={`版本历史 · ${versionPackage?.service_type ?? ''}`} width={1200} open={Boolean(versionPackage)} footer={null} onCancel={() => setVersionPackage(undefined)} destroyOnHidden>
        <Typography.Paragraph type="secondary">切换当前版本只影响后续安装；已安装服务实例继续固定使用原 SHA-256 版本。</Typography.Paragraph>
        <Table<PackageVersion>
          rowKey="id"
          loading={versionsQuery.isLoading}
          dataSource={versionsQuery.data ?? []}
          pagination={false}
          tableLayout="fixed"
          scroll={{ x: 1140 }}
          columns={[
            { title: '版本', width: 130, render: (_, item) => <div className="table-stacked-cell"><Typography.Text code>{item.id.slice(0, 8)}</Typography.Text>{item.current && <Tag color="success">当前</Tag>}</div> },
            { title: '文件 / 摘要', width: 300, render: (_, item) => <div className="table-stacked-cell"><Typography.Text ellipsis={{ tooltip: item.original_filename }}>{item.original_filename}</Typography.Text><Typography.Text type="secondary" className="cell-caption table-mono-line">{item.sha256.slice(0, 16)}… · {formatBytes(item.size_bytes)}</Typography.Text></div> },
            { title: '配置 / 健康端口', width: 170, render: (_, item) => <div className="table-stacked-cell"><span>{item.config_format || '-'} · {item.config_port}</span><Tag color="success">校验通过</Tag></div> },
            { title: '上传人', dataIndex: 'uploaded_by_username', width: 110, render: (value: string) => value || '升级迁移' },
            { title: '上传时间', dataIndex: 'uploaded_at', width: 170, render: formatTime },
            { title: '引用', dataIndex: 'referenced_service_instance_count', width: 80, render: (value: number) => `${value} 个` },
            { title: '操作', width: 200, fixed: 'right', render: (_, item) => <div className="row-actions">{can('package.write', item.owner_id) && <Button size="small" disabled={item.current} loading={activateMutation.isPending && activateMutation.variables?.versionId === item.id} onClick={() => modal.confirm({ title: '切换当前版本？', content: '只影响后续安装，不会自动重装现有服务实例。', onOk: () => activateMutation.mutate({ serviceType: item.service_type, versionId: item.id, ownerId: item.owner_id }) })}>设为当前</Button>}{can('package.delete', item.owner_id) && <Button size="small" danger disabled={item.current || item.referenced_service_instance_count > 0} loading={deleteVersionMutation.isPending && deleteVersionMutation.variables?.versionId === item.id} onClick={() => modal.confirm({ title: '删除历史版本？', content: '版本文件将从本地存储永久删除。', okButtonProps: { danger: true }, onOk: () => deleteVersionMutation.mutate({ serviceType: item.service_type, versionId: item.id, ownerId: item.owner_id }) })}>删除</Button>}</div> },
          ]}
        />
      </Modal>

      <Modal
        title={replacingType ? `更新 ${replacingType} 安装包` : '上传安装包'}
        open={uploadOpen}
        okText={replacingType ? '更新' : '上传并校验'}
        cancelText="取消"
        confirmLoading={noteMutation.isPending}
        onOk={submitUpload}
        onCancel={() => setUploadOpen(false)}
        destroyOnHidden
      >
        <Typography.Paragraph type="secondary">
          文件将在后台上传并显示进度；可关闭弹窗、继续添加其他安装包或切换 DP 页面。上传及校验完成前请勿刷新或关闭浏览器。
        </Typography.Paragraph>
        <div className="upload-form">
          {packageWriteAll && <><label>所属账号</label><Select disabled={Boolean(replacingType)} value={uploadOwner} options={users.filter((item) => item.enabled).map((item) => ({ value: item.id, label: item.username }))} onChange={setUploadOwner} /><Typography.Text type="secondary" className="field-help">代其他账号创建时会记录高风险审计。</Typography.Text></>}
          <label>服务类型</label>
          <Select
            mode="tags"
            maxCount={1}
            disabled={Boolean(replacingType)}
            value={serviceType ? [serviceType] : []}
            placeholder="选择已有类型或输入新类型"
            options={packages.map((item) => ({
              value: item.service_type,
              label: item.service_type,
            }))}
            onChange={(values) => setServiceType(values.at(-1)?.toLowerCase() ?? '')}
            tokenSeparators={[' ', ',']}
          />
          <Typography.Text type="secondary" className="field-help">
            自定义类型示例：video-forward。类型创建后不可重命名。
          </Typography.Text>

          <label>安装包文件</label>
          <Upload.Dragger
            accept=".tar.gz,application/gzip"
            maxCount={1}
            showUploadList={false}
            beforeUpload={(file) => {
              if (!file.name.toLowerCase().endsWith('.tar.gz')) {
                message.error('安装包仅支持 .tar.gz 格式')
                return Upload.LIST_IGNORE
              }
              setSelectedFile(file as File)
              return false
            }}
          >
            <p className="ant-upload-drag-icon"><InboxOutlined /></p>
            <p className="ant-upload-text">
              {selectedFile ? selectedFile.name : '点击或拖拽 .tar.gz 文件到此处'}
            </p>
            <p className="ant-upload-hint">
              {replacingType
                ? '可选：选择后创建不可变版本并设为当前，不选择则只修改备注'
                : '上传后创建首个版本并作为当前版本'}
            </p>
          </Upload.Dragger>

          <label>备注</label>
          <Input.TextArea
            rows={2}
            maxLength={200}
            showCount
            value={note}
            placeholder="可选，最多 200 字"
            onChange={(event) => setNote(event.target.value)}
          />
        </div>
      </Modal>
    </div>
  )
}

function formatTime(value?: string) {
  if (!value) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(value))
}

function PackageMetric({
  icon,
  label,
  value,
  suffix,
}: {
  icon: React.ReactNode
  label: string
  value: React.ReactNode
  suffix?: string
}) {
  return (
    <div className="metric-item">
      <span className="metric-icon">{icon}</span>
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}{suffix && <small>{suffix}</small>}</div>
    </div>
  )
}
