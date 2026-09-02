// @vitest-environment jsdom

import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PageErrorBoundary } from './PageErrorBoundary'

const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)

afterEach(() => {
  cleanup()
  consoleError.mockClear()
})

function Page({ broken, label }: { broken: boolean; label: string }) {
  if (broken) throw new Error('render failed')
  return <div>{label}</div>
}

describe('PageErrorBoundary', () => {
  it('keeps a recoverable fallback and clears it when the route changes', async () => {
    const view = render(
      <PageErrorBoundary resetKey="/broken" onHome={vi.fn()}>
        <Page broken label="broken" />
      </PageErrorBoundary>,
    )
    expect(view.getByText('当前页面加载失败')).toBeTruthy()
    expect(view.getByText('返回首页')).toBeTruthy()

    view.rerender(
      <PageErrorBoundary resetKey="/healthy" onHome={vi.fn()}>
        <Page broken={false} label="healthy page" />
      </PageErrorBoundary>,
    )
    await waitFor(() => expect(view.getByText('healthy page')).toBeTruthy())
  })

  it('retries the current page without reloading the shell', async () => {
    let broken = true
    function RecoverablePage() {
      if (broken) throw new Error('first render failed')
      return <div>recovered page</div>
    }

    const view = render(
      <PageErrorBoundary resetKey="/recoverable" onHome={vi.fn()}>
        <RecoverablePage />
      </PageErrorBoundary>,
    )
    broken = false
    fireEvent.click(view.getByRole('button', { name: /重\s*试/ }))
    await waitFor(() => expect(view.getByText('recovered page')).toBeTruthy())
  })
})
