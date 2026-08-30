import { Button } from 'antd'
import type { TablePaginationConfig } from 'antd/es/table'

const totalLabel = (total: number) => `共 ${total} 条`

export const mainTablePagination: TablePaginationConfig = {
  pageSize: 10,
  hideOnSinglePage: true,
  showSizeChanger: false,
  showTotal: totalLabel,
}

export const modalTablePagination: TablePaginationConfig = {
  pageSize: 8,
  hideOnSinglePage: true,
  showSizeChanger: false,
  showTotal: totalLabel,
}

type LoadMoreFooterProps = {
  hasMore: boolean
  loading: boolean
  onLoadMore: () => void
}

export function LoadMoreFooter({ hasMore, loading, onLoadMore }: LoadMoreFooterProps) {
  if (!hasMore) return null

  return (
    <div className="list-load-more">
      <Button loading={loading} onClick={onLoadMore}>加载更多</Button>
    </div>
  )
}
