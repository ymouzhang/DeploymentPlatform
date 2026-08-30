import { useState } from 'react'
import {
  ArrowRightOutlined,
  CloudServerOutlined,
  FileZipOutlined,
  LockOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { App, Button, Form, Input } from 'antd'
import { api } from '../../api/client'
import type { User } from '../../types'

export function LoginPage({ onLogin }: { onLogin: (user: User) => void }) {
  const { message } = App.useApp()
  const [loading, setLoading] = useState(false)
  const submit = async (values: { username: string; password: string }) => {
    setLoading(true)
    try { onLogin(await api.login(values)) } catch (error) { message.error((error as Error).message) } finally { setLoading(false) }
  }
  return (
    <div className="login-page">
      <div className="login-shell">
        <section className="login-hero">
          <div className="login-orbit login-orbit-one" />
          <div className="login-orbit login-orbit-two" />
          <div className="login-brand">
            <div className="brand-mark login-brand-mark"><span>DP</span></div>
            <div>
              <div className="login-brand-name">DP Console</div>
              <div className="login-brand-subtitle">DEPLOYMENT HUB</div>
            </div>
          </div>

          <div className="login-hero-copy">
            <div className="login-eyebrow"><span /> DEPLOYMENT PLATFORM</div>
            <h1>让每一次部署<br />都清晰、可靠、可追踪。</h1>
            <p>在一个安全的工作台中管理交付物、服务器环境与远程服务生命周期。</p>
          </div>

          <div className="login-capabilities">
            <Capability icon={<FileZipOutlined />} label="安装包" detail="版本集中管理" />
            <Capability icon={<CloudServerOutlined />} label="服务器" detail="环境独立隔离" />
            <Capability icon={<SafetyCertificateOutlined />} label="凭据安全" detail="AES 加密存储" />
          </div>

          <div className="login-hero-footer">
            <span className="login-live-dot" />
            PRIVATE DEPLOYMENT CONTROL PLANE
          </div>
        </section>

        <section className="login-panel">
          <div className="login-form-wrap">
            <div className="login-form-heading">
              <div className="login-form-kicker">SECURE ACCESS</div>
              <h2>欢迎回来</h2>
            </div>
            <Form className="login-form" layout="vertical" onFinish={submit} requiredMark={false}>
              <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
                <Input
                  size="large"
                  prefix={<UserOutlined />}
                  placeholder="请输入用户名"
                  autoComplete="username"
                  autoFocus
                />
              </Form.Item>
              <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
                <Input.Password
                  size="large"
                  prefix={<LockOutlined />}
                  placeholder="请输入密码"
                  autoComplete="current-password"
                />
              </Form.Item>
              <Button
                className="login-submit"
                type="primary"
                htmlType="submit"
                block
                size="large"
                loading={loading}
              >
                <span>进入控制台</span>
                {!loading && <ArrowRightOutlined />}
              </Button>
            </Form>
            <div className="login-security-note">
              <SafetyCertificateOutlined />
              <span>会话受安全 Cookie 保护，SSH 凭据不会返回浏览器</span>
            </div>
          </div>
          <div className="login-panel-footer">DP · INTERNAL DEPLOYMENT SYSTEM</div>
        </section>
      </div>
    </div>
  )
}

function Capability({
  icon,
  label,
  detail,
}: {
  icon: React.ReactNode
  label: string
  detail: string
}) {
  return (
    <div className="login-capability">
      <span className="login-capability-icon">{icon}</span>
      <span><strong>{label}</strong><small>{detail}</small></span>
    </div>
  )
}
