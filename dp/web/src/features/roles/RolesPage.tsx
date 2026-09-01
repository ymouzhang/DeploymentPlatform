import { DeleteOutlined, EditOutlined, PlusOutlined, TeamOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Card, Form, Input, Modal, Select, Space, Table, Tag, Typography } from 'antd'
import { useMemo, useState } from 'react'
import { api } from '../../api/client'
import { useAuth } from '../../app/AuthContext'
import type { Permission, PermissionDefinition, PermissionScope, Role, RoleGrant } from '../../types'

type GrantMap = Partial<Record<Permission, PermissionScope>>

export function RolesPage() {
  const { message, modal } = App.useApp()
  const auth = useAuth()
  const client = useQueryClient()
  const roles = useQuery({ queryKey: ['roles'], queryFn: api.listRoles })
  const permissions = useQuery({ queryKey: ['permissions'], queryFn: api.listPermissions })
  const [editing, setEditing] = useState<Role>()
  const [open, setOpen] = useState(false)
  const [membersRole, setMembersRole] = useState<Role>()
  const [grants, setGrants] = useState<GrantMap>({})
  const [form] = Form.useForm<{ key: string; name: string; description: string }>()

  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['roles'] })
    void client.invalidateQueries({ queryKey: ['users'] })
  }
  const save = useMutation({
    mutationFn: async (values: { key: string; name: string; description: string }) => {
      const roleGrants = Object.entries(grants).filter((entry): entry is [Permission, PermissionScope] => Boolean(entry[1])).map(([permission, scope]) => ({ permission, scope }))
      if (editing) return api.updateRole(editing.id, { name: values.name, description: values.description ?? '', grants: roleGrants })
      return api.createRole({ key: values.key, name: values.name, description: values.description ?? '', grants: roleGrants })
    },
    onSuccess: () => { message.success(editing ? '角色已更新' : '角色已创建'); setOpen(false); setEditing(undefined); setGrants({}); form.resetFields(); refresh() },
    onError: (error: Error) => message.error(error.message),
  })
  const remove = useMutation({
    mutationFn: api.deleteRole,
    onSuccess: () => { message.success('角色已删除'); refresh() },
    onError: (error: Error) => message.error(error.message),
  })
  const openCreate = () => { setEditing(undefined); setGrants({}); form.resetFields(); setOpen(true) }
  const openEdit = (role: Role) => {
    setEditing(role)
    setGrants(Object.fromEntries(role.grants.map((grant) => [grant.permission, grant.scope])) as GrantMap)
    form.setFieldsValue({ key: role.key, name: role.name, description: role.description })
    setOpen(true)
  }
  const grouped = useMemo(() => groupPermissions(permissions.data ?? []), [permissions.data])
  const members = auth.users.filter((user) => user.roles.some((role) => role.id === membersRole?.id))

  return <div className="page">
    <div className="page-heading"><div><div className="page-eyebrow">Role-based access control</div><Typography.Title level={2}>角色与权限</Typography.Title><Typography.Paragraph type="secondary">角色聚合权限，账号可以绑定多个角色；权限范围分为本人和全部账号。</Typography.Paragraph></div>{auth.can('role.create') && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建角色</Button>}</div>
    <Card className="content-card table-card" styles={{ body: { padding: 0 } }}>
      <Table<Role> rowKey="id" loading={roles.isLoading} dataSource={roles.data ?? []} pagination={false} columns={[
        { title: '角色', width: 240, render: (_, role) => <Space><Typography.Text strong>{role.name}</Typography.Text>{role.system && <Tag color="blue">系统内置</Tag>}</Space> },
        { title: '标识', dataIndex: 'key', width: 180, render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
        { title: '说明', dataIndex: 'description' },
        { title: '权限', width: 100, render: (_, role) => `${role.grants.length} 项` },
        { title: '成员', width: 100, render: (_, role) => <Button type="link" icon={<TeamOutlined />} onClick={() => setMembersRole(role)}>{role.member_count}</Button> },
        { title: '操作', width: 180, fixed: 'right', render: (_, role) => <Space>{auth.can('role.update') && <Button size="small" icon={<EditOutlined />} disabled={role.system} onClick={() => openEdit(role)}>编辑</Button>}{auth.can('role.delete') && <Button size="small" danger icon={<DeleteOutlined />} disabled={role.system || role.member_count > 0} onClick={() => modal.confirm({ title: `删除角色 ${role.name}？`, content: '删除后无法恢复。已绑定成员的角色不能删除。', okButtonProps: { danger: true }, onOk: () => remove.mutate(role.id) })}>删除</Button>}</Space> },
      ]} />
    </Card>
    <Modal width={920} title={editing ? `编辑角色 · ${editing.name}` : '新建角色'} open={open} onCancel={() => setOpen(false)} onOk={() => form.validateFields().then((values) => save.mutate(values))} confirmLoading={save.isPending}>
      <Form form={form} layout="vertical"><Space align="start" style={{ width: '100%' }}><Form.Item name="key" label="角色标识" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_-]{1,62}$/, message: '2–63 位小写字母、数字、下划线或连字符' }]}><Input disabled={Boolean(editing)} style={{ width: 260 }} /></Form.Item><Form.Item name="name" label="显示名称" rules={[{ required: true }, { max: 64 }]}><Input style={{ width: 260 }} /></Form.Item></Space><Form.Item name="description" label="说明" rules={[{ max: 500 }]}><Input.TextArea rows={2} /></Form.Item></Form>
      <Typography.Title level={5}>权限矩阵</Typography.Title>
      <Table<PermissionDefinition> rowKey="key" size="small" loading={permissions.isLoading} pagination={false} dataSource={grouped} scroll={{ y: 420 }} columns={[
        { title: '资源', dataIndex: 'resource', width: 150, render: (value: string, item, index) => index === 0 || grouped[index - 1]?.resource !== value ? value : '' },
        { title: '权限', width: 250, render: (_, item) => <div><Typography.Text code>{item.key}</Typography.Text><div><Typography.Text type="secondary">{item.description}</Typography.Text></div></div> },
        { title: '授权范围', width: 220, render: (_, item) => <Select allowClear value={grants[item.key]} placeholder="未授权" style={{ width: 180 }} options={item.scoped ? [{ value: 'own', label: '本人资源' }, { value: 'all', label: '全部账号' }] : [{ value: 'all', label: '允许' }]} onChange={(scope) => setGrants((current) => ({ ...current, [item.key]: scope }))} /> },
      ]} />
    </Modal>
    <Modal title={membersRole ? `角色成员 · ${membersRole.name}` : '角色成员'} open={Boolean(membersRole)} footer={null} onCancel={() => setMembersRole(undefined)}><Space size={[6, 8]} wrap>{members.length ? members.map((user) => <Tag key={user.id}>{user.username}</Tag>) : <Typography.Text type="secondary">暂无成员</Typography.Text>}</Space></Modal>
  </div>
}

function groupPermissions(items: PermissionDefinition[]) {
  return [...items].sort((left, right) => left.resource.localeCompare(right.resource) || left.key.localeCompare(right.key))
}
