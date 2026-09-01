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
    expect(view.getByText('后台上传中')).toBeTruthy()
    expect(view.getByText('上传依赖当前浏览器，请勿刷新或关闭浏览器')).toBeTruthy()

    view.rerender(wrap(<div>其他 DP 页面</div>))
    finishChunk(4)
    await waitFor(() => expect(api.completeModelUpload).toHaveBeenCalledWith('upload-1'))
    await waitFor(() => expect(localStorage.getItem('dp:model-upload:v2:user-1')).toBeNull())
  })
})

function StartUpload() {
  const { start } = useModelUpload()
  return <button onClick={() => void start(
    { name: 'Qwen', environment_id: 'env-1', target_dir: '/data/models/Qwen' },
    new File(['test'], 'Qwen.tar.gz'),
  )}>start</button>
}
