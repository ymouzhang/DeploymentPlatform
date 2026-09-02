import { CloudDownloadOutlined, CloudUploadOutlined, DeleteOutlined, EditOutlined, PlusOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Drawer, Form, Input, InputNumber, Space, Table, Tag, Typography, Upload } from 'antd'
import type { UploadProps } from 'antd'
import { useState } from 'react'
import { api } from '../../api/client'
import { useAuth } from '../../app/AuthContext'
import type { Host, HostInput } from '../../types'

const defaults: HostInput = { name: '', ip: '', ssh_user: 'aaron', ssh_port: 22, ssh_password: '', note: '' }

export function HostsPage() {
  const { message, modal } = App.useApp()
  const { ownerId, can } = useAuth()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Host>()
  const [form] = Form.useForm<HostInput>()
  const hosts = useQuery({ queryKey: ['hosts', ownerId], queryFn: () => api.listHosts(ownerId) })

  const save = useMutation({
    mutationFn: (input: HostInput) => editing ? api.updateHost(editing.id, input) : api.createHost(input, ownerId),
    onSuccess: () => { message.success(editing ? '主机已更新' : '主机已注册'); setOpen(false); void queryClient.invalidateQueries({ queryKey: ['hosts'] }) },
    onError: (error: Error) => message.error(error.message),
  })
  const validate = useMutation({
    mutationFn: ({ id, input }: { id?: string; input?: HostInput }) => id ? api.validateHost(id) : api.validateDraftHost(input!),
    onSuccess: (result) => {
      if (result.validation_error) message.error(result.validation_error)
      else { message.success('SSH 连接、架构识别和 SFTP 传输均正常'); void queryClient.invalidateQueries({ queryKey: ['hosts'] }) }
    },
    onError: (error: Error) => message.error(error.message),
  })
  const remove = useMutation({
    mutationFn: api.deleteHost,
    onSuccess: () => { message.success('主机已删除'); void queryClient.invalidateQueries({ queryKey: ['hosts'] }) },
    onError: (error: Error) => message.error(error.message),
  })
  const importHosts = useMutation({
    mutationFn: (document: unknown) => api.importHosts(document, ownerId),
    onSuccess: (result) => { message.success(`导入完成：新增 ${result.created}，更新 ${result.updated}`); void queryClient.invalidateQueries({ queryKey: ['hosts'] }) },
    onError: (error: Error) => message.error(error.message),
  })
  const importProps: UploadProps = {
    accept: '.json,application/json',
    showUploadList: false,
    beforeUpload: async (file) => {
      try { importHosts.mutate(JSON.parse(await file.text()) as unknown) }
      catch { message.error('导入文件不是有效的 JSON') }
      return Upload.LIST_IGNORE
    },
  }
  const showCreate = () => { setEditing(undefined); form.setFieldsValue(defaults); setOpen(true) }
  const showEdit = (host: Host) => {
    setEditing(host)
    form.setFieldsValue({ name: host.name, ip: host.ip, ssh_user: host.ssh_user, ssh_port: host.ssh_port, ssh_password: '', note: host.note })
    setOpen(true)
  }

  return <div className="page-stack">
    <div className="page-heading"><div><Typography.Title level={2}>主机管理</Typography.Title><Typography.Paragraph type="secondary">SSH 主机只需注册一次，多个服务实例和模型可以复用同一套连接凭据。</Typography.Paragraph></div><Space>
      {can('host.export', ownerId) && <Button icon={<CloudDownloadOutlined />} href={ownerId ? `/api/v1/hosts/export?owner_id=${encodeURIComponent(ownerId)}` : '/api/v1/hosts/export'} disabled={!hosts.data?.length}>导出 JSON</Button>}
      {can('host.import', ownerId) && <Upload {...importProps}><Button icon={<CloudUploadOutlined />} loading={importHosts.isPending}>导入 JSON</Button></Upload>}
      {can('host.write', ownerId) && <Button type="primary" icon={<PlusOutlined />} onClick={showCreate}>注册主机</Button>}
    </Space></div>
    <Table<Host> rowKey="id" loading={hosts.isLoading} dataSource={hosts.data ?? []} columns={[
      { title: '主机', render: (_, host) => <div><Typography.Text strong>{host.name}</Typography.Text><div className="cell-caption">{host.ip}:{host.ssh_port}</div></div> },
      { title: 'SSH 用户', dataIndex: 'ssh_user' },
      { title: '架构', dataIndex: 'arch', render: (value: string) => value || '-' },
      { title: '校验状态', render: (_, host) => host.last_validation_at ? <Tag color="green">已校验</Tag> : <Tag>未校验</Tag> },
      { title: '备注', dataIndex: 'note', ellipsis: true },
      { title: '操作', width: 250, render: (_, host) => <Space>
        {can('host.validate', host.owner_id) && <Button size="small" icon={<SafetyCertificateOutlined />} loading={validate.isPending && validate.variables?.id === host.id} onClick={() => validate.mutate({ id: host.id })}>校验</Button>}
        {can('host.write', host.owner_id) && <Button size="small" icon={<EditOutlined />} onClick={() => showEdit(host)}>编辑</Button>}
        {can('host.delete', host.owner_id) && <Button danger size="small" icon={<DeleteOutlined />} onClick={() => modal.confirm({ title: `删除主机“${host.name}”？`, content: '存在服务实例或模型时不能删除。', okButtonProps: { danger: true }, onOk: () => remove.mutateAsync(host.id) })}>删除</Button>}
      </Space> },
    ]} />
    <Drawer width={520} title={editing ? '编辑主机' : '注册主机'} open={open} onClose={() => setOpen(false)} extra={<Space><Button onClick={() => setOpen(false)}>取消</Button><Button type="primary" loading={save.isPending} onClick={() => form.validateFields().then((v) => save.mutate(v))}>保存</Button></Space>}>
      <Form form={form} layout="vertical" initialValues={defaults}>
        <Form.Item name="name" label="主机名称" rules={[{ required: true }]}><Input placeholder="例如：GPU 服务器 A" /></Form.Item>
        <Form.Item name="ip" label="服务器 IP" rules={[{ required: true }]}><Input placeholder="10.0.0.8" /></Form.Item>
        <Space align="start" style={{ width: '100%' }}><Form.Item name="ssh_user" label="SSH 用户" rules={[{ required: true }]}><Input style={{ width: 220 }} /></Form.Item><Form.Item name="ssh_port" label="SSH 端口" rules={[{ required: true }]}><InputNumber min={1} max={65535} style={{ width: 180 }} /></Form.Item></Space>
        <Form.Item name="ssh_password" label="SSH 密码" extra={editing ? '留空表示保持原密码' : '密码加密保存'} rules={editing ? [] : [{ required: true }]}><Input.Password autoComplete="new-password" /></Form.Item>
        <Form.Item name="note" label="备注"><Input.TextArea rows={3} maxLength={200} showCount /></Form.Item>
        {!editing && <Button icon={<SafetyCertificateOutlined />} loading={validate.isPending} onClick={() => form.validateFields().then((input) => validate.mutate({ input }))}>保存前校验 SSH</Button>}
      </Form>
    </Drawer>
  </div>
}
