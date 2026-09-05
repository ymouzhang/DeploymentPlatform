// @vitest-environment jsdom

import { fireEvent, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useDraggablePanel } from './useDraggablePanel'

describe('useDraggablePanel', () => {
  afterEach(() => vi.restoreAllMocks())

  it('moves by the title handle and stays inside the viewport', () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 600, y: 400, left: 600, top: 400, right: 900, bottom: 700,
      width: 300, height: 300, toJSON: () => ({}),
    })
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1000 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    const view = render(<DraggableExample />)
    const handle = view.getByText('拖动区域')

    fireEvent.pointerDown(handle, { button: 0, pointerId: 1, clientX: 700, clientY: 450 })
    fireEvent.pointerMove(handle, { pointerId: 1, clientX: 900, clientY: 700 })
    expect(view.getByTestId('panel').style.transform).toBe('translate3d(100px, 100px, 0)')
    fireEvent.pointerUp(handle, { pointerId: 1 })
  })
})

function DraggableExample() {
  const draggable = useDraggablePanel()
  return <div ref={draggable.panelRef} data-testid="panel" style={draggable.panelStyle}>
    <span {...draggable.handleProps}>拖动区域</span>
  </div>
}
