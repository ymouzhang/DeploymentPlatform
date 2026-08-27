import { Select, Space, Tag, Typography } from 'antd'
import type { ResourceTag, ResourceTagRef } from '../../types'

export function TagFilter({ tags, value, onChange, width = 280 }: { tags: ResourceTag[]; value: string[]; onChange: (value: string[]) => void; width?: number }) {
  return <Select mode="multiple" allowClear maxTagCount="responsive" value={value} onChange={onChange} style={{ width }} placeholder="按标签筛选（同时满足）" options={tags.map((tag) => ({ value: tag.id, label: `${tag.group_name} / ${tag.value}${tag.owner_username ? ` · ${tag.owner_username}` : ''}` }))} />
}

export function TagList({ tags, empty = '-' }: { tags?: ResourceTagRef[]; empty?: string }) {
  if (!tags?.length) return <Typography.Text type="secondary">{empty}</Typography.Text>
  return <Space size={[4, 4]} wrap>{tags.map((tag) => <Tag key={`${tag.group_name}\u0000${tag.value}`} color={tagColor(tag.group_name)}>{tag.group_name} · {tag.value}</Tag>)}</Space>
}

function tagColor(group: string) {
  const colors = ['blue', 'cyan', 'geekblue', 'purple', 'green', 'gold', 'magenta']
  let hash = 0
  for (const char of group) hash = (hash * 31 + char.charCodeAt(0)) | 0
  return colors[Math.abs(hash) % colors.length]
}
