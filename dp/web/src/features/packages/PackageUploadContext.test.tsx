// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { App as AntApp } from 'antd'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { PackageUploadProvider, usePackageUpload } from './PackageUploadContext'

vi.mock('../../api/client', () => ({ api: { uploadPackageWithProgress: vi.fn() } }))

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })),
})
vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
vi.stubGlobal('crypto', { randomUUID: vi.fn().mockReturnValueOnce('task-1').mockReturnValueOnce('task-2') })

describe('PackageUploadProvider', () => {
  afterEach(() => vi.clearAllMocks())

  it('keeps multiple package uploads active and reports independent progress', async () => {
    vi.mocked(api.uploadPackageWithProgress).mockImplementation(({ serviceType, onProgress }) => {
      onProgress(serviceType === 'vllm' ? 25 : 50, 100)
      return new Promise(() => {})
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(<QueryClientProvider client={client}><MemoryRouter><AntApp>
      <PackageUploadProvider><StartPackages /></PackageUploadProvider>
    </AntApp></MemoryRouter></QueryClientProvider>)

    fireEvent.click(view.getByRole('button', { name: 'start packages' }))
    await waitFor(() => expect(api.uploadPackageWithProgress).toHaveBeenCalledTimes(2))
    expect(view.getByRole('button', { name: /安装包任务 · 2 个 · 38%/ })).toBeTruthy()
    fireEvent.click(view.getByRole('button', { name: /安装包任务/ }))
    expect(view.getByText('2 个任务进行中，请勿刷新或关闭浏览器')).toBeTruthy()
    expect(view.getByText('上传中 25%')).toBeTruthy()
    expect(view.getByText('上传中 50%')).toBeTruthy()
  })
})

function StartPackages() {
  const { start } = usePackageUpload()
  return <button onClick={() => {
    start({ serviceType: 'vllm', file: new File(['vllm'], 'vllm.tar.gz') })
    start({ serviceType: 'sglang', file: new File(['sglang'], 'sglang.tar.gz') })
  }}>start packages</button>
}
