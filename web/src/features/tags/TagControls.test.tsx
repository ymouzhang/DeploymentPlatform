// @vitest-environment jsdom

import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { TagList } from './TagControls'

describe('TagList', () => {
  it('renders grouped resource labels', () => {
    const view = render(<TagList tags={[{ group_name: '环境阶段', value: '生产' }, { group_name: '区域', value: '华东' }]} />)
    expect(view.getByText('环境阶段 · 生产')).toBeTruthy()
    expect(view.getByText('区域 · 华东')).toBeTruthy()
  })
})
