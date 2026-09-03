// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { App as AntApp } from 'antd'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { ModelUploadProvider, useModelUpload } from './ModelUploadContext'

vi.mock('../../api/client', () => ({
  api: {
    createModelUpload: vi.fn(), modelUploadOffset: vi.fn(), uploadModelChunk: vi.fn(),
    completeModelUpload: vi.fn(), cancelModelUpload: vi.fn(),
  },
}))

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })),
})
vi.stubGlobal('ResizeObserver', class {
  observe() {}
  unobserve() {}
  disconnect() {}
})

describe('ModelUploadProvider', () => {
  afterEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
  })

  it('keeps uploading when the model page child is replaced', async () => {
    let finishChunk!: (offset: number) => void
    const chunkResult = new Promise<number>((resolve) => { finishChunk = resolve })
    vi.mocked(api.createModelUpload).mockResolvedValue({
      model: {} as never, upload_id: 'upload-1', offset: 0, chunk_bytes: 4, expires_at: '2026-09-02T00:00:00Z',
    })
    vi.mocked(api.modelUploadOffset).mockResolvedValue(0)
    vi.mocked(api.uploadModelChunk).mockReturnValue(chunkResult)
    vi.mocked(api.completeModelUpload).mockResolvedValue({} as never)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrap = (child: ReactNode) => (
      <QueryClientProvider client={client}><MemoryRouter><AntApp>
        <ModelUploadProvider userId="user-1">{child}</ModelUploadProvider>
      </AntApp></MemoryRouter></QueryClientProvider>
    )
    const view = render(wrap(<StartUpload />))

    fireEvent.click(view.getByRole('button', { name: 'start' }))
    await waitFor(() => expect(api.uploadModelChunk).toHaveBeenCalledOnce())
    expect(view.getByText('1 个任务上传中，请勿刷新或关闭浏览器')).toBeTruthy()

    view.rerender(wrap(<div>其他 DP 页面</div>))
    finishChunk(4)
    await waitFor(() => expect(api.completeModelUpload).toHaveBeenCalledWith('upload-1'))
    await waitFor(() => expect(localStorage.getItem('dp:model-upload:v3:user-1')).toBeNull())
  })

  it('uploads multiple model files concurrently', async () => {
    vi.mocked(api.createModelUpload)
      .mockResolvedValueOnce({ model: {} as never, upload_id: 'upload-1', offset: 0, chunk_bytes: 4, expires_at: '2026-09-02T00:00:00Z' })
      .mockResolvedValueOnce({ model: {} as never, upload_id: 'upload-2', offset: 0, chunk_bytes: 4, expires_at: '2026-09-02T00:00:00Z' })
    vi.mocked(api.modelUploadOffset).mockResolvedValue(0)
    vi.mocked(api.uploadModelChunk).mockReturnValue(new Promise(() => {}))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(<QueryClientProvider client={client}><MemoryRouter><AntApp>
      <ModelUploadProvider userId="user-1"><StartTwoUploads /></ModelUploadProvider>
    </AntApp></MemoryRouter></QueryClientProvider>)

    fireEvent.click(view.getByRole('button', { name: 'start two' }))
    await waitFor(() => expect(api.uploadModelChunk).toHaveBeenCalledTimes(2))
    expect(api.uploadModelChunk).toHaveBeenCalledWith('upload-1', 0, expect.any(Blob))
    expect(api.uploadModelChunk).toHaveBeenCalledWith('upload-2', 0, expect.any(Blob))
    expect(view.getByText('2 个任务上传中，请勿刷新或关闭浏览器')).toBeTruthy()
  })
})

function StartUpload() {
  const { start } = useModelUpload()
  return <button onClick={() => void start(
    { name: 'Qwen', host_id: 'env-1', target_dir: '/data/models/Qwen' },
    new File(['test'], 'Qwen.tar.gz'),
  )}>start</button>
}

function StartTwoUploads() {
  const { start } = useModelUpload()
  return <button onClick={() => void Promise.all([
    start({ name: 'Qwen', host_id: 'host-1', target_dir: '/models/qwen' }, new File(['test'], 'qwen.tar.gz')),
    start({ name: 'Llama', host_id: 'host-1', target_dir: '/models/llama' }, new File(['test'], 'llama.tar.gz')),
  ])}>start two</button>
}
