import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Empty, Input, Modal, Pagination, Segmented, Space, Table, Tag, Typography, message } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { DeleteOutlined, EditOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons'
import { useNavigate, useParams } from 'react-router-dom'
import PageHeader from '../components/PageHeader'
import { ApiError, apiRequest, errorMessage } from '../api'

type EntityRow = { id: string; name: string; key: string; entity_version: number }
type EntityPage = { items: EntityRow[]; total: number }
const pageSize = 50

const kinds = [
  { value: 'attribute', label: '属性' }, { value: 'tag', label: '标签' }, { value: 'character', label: '角色' },
  { value: 'skill', label: '技能' }, { value: 'item', label: '物品' }, { value: 'effect', label: '效果' },
]

export default function EntityListPage() {
  const { kind = 'attribute' } = useParams()
  const navigate = useNavigate()
  const client = useQueryClient()
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [page, setPage] = useState(1)
  const [references, setReferences] = useState<Array<{ entity_id: string; field_path: string }>>([])
  const [messageApi, contextHolder] = message.useMessage()
  const kindLabel = kinds.find(item => item.value === kind)?.label ?? kind

  useEffect(() => { const timer = window.setTimeout(() => { setDebounced(query); setPage(1) }, 250); return () => window.clearTimeout(timer) }, [query])
  useEffect(() => { setQuery(''); setDebounced(''); setPage(1); setReferences([]) }, [kind])

  const list = useQuery({
    queryKey: ['entities', kind, debounced, page],
    queryFn: () => {
      const params = new URLSearchParams({ limit: String(pageSize), page: String(page) })
      if (debounced) params.set('query', debounced)
      return apiRequest<EntityPage>(`/api/v1/entities/${kind}?${params}`)
    },
  })

  useEffect(() => {
    if (list.data && page > Math.max(1, Math.ceil(list.data.total / pageSize))) {
      setPage(Math.max(1, Math.ceil(list.data.total / pageSize)))
    }
  }, [list.data, page])

  const archive = useMutation({
    mutationFn: async (item: EntityRow) => {
      try {
        await apiRequest<void>(`/api/v1/entities/${kind}/${item.id}`, { method: 'DELETE', headers: { 'If-Match': `"${item.id}:${item.entity_version}"` } })
      } catch (cause) {
        if (cause instanceof ApiError && Array.isArray(cause.details?.references)) setReferences(cause.details.references as typeof references)
        throw cause
      }
    },
    onSuccess: () => { messageApi.success('对象已归档'); void client.invalidateQueries({ queryKey: ['entities', kind] }) },
    onError: cause => messageApi.error(errorMessage(cause, '归档失败')),
  })

  function confirmArchive(item: EntityRow) {
    Modal.confirm({ title: `归档「${item.name}」？`, content: '归档后该对象不会出现在默认列表中；被其他对象引用时操作会被阻止。', okText: '确认归档', okButtonProps: { danger: true }, cancelText: '取消', onOk: () => archive.mutateAsync(item) })
  }

  return <div className="page-container">
    {contextHolder}
    <PageHeader title={`${kindLabel}配置`} description="在结构化配置目录中搜索、编辑和归档对象。" extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => navigate(`/config/${kind}/new`)}>新建{kindLabel}</Button>} />
    <Card className="section-card">
      <Space orientation="vertical" size="large" style={{ width: '100%' }}>
        <Segmented block options={kinds} value={kind} onChange={value => navigate(`/config/${value}`)} />
        <Input allowClear size="large" prefix={<SearchOutlined />} value={query} onChange={event => setQuery(event.target.value)} placeholder={`搜索${kindLabel}名称或 Key`} aria-label="搜索" />
        {references.length > 0 && <Alert type="warning" showIcon title="对象仍被引用，无法归档" description={<ul>{references.map(reference => <li key={`${reference.entity_id}:${reference.field_path}`}>{reference.entity_id} · {reference.field_path}</li>)}</ul>} />}
        <Table<EntityRow>
          rowKey="id"
          loading={list.isLoading}
          dataSource={list.data?.items ?? []}
          pagination={false}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={debounced ? '没有匹配结果' : '暂无对象'} /> }}
          columns={[
            { title: '名称', dataIndex: 'name', render: (value, row) => <Button type="link" className="table-primary-link" onClick={() => navigate(`/config/${kind}/${row.id}`)}>{value}</Button> },
            { title: 'Key', dataIndex: 'key', render: value => <Typography.Text code>{value}</Typography.Text> },
            { title: '实体版本', dataIndex: 'entity_version', width: 130, render: value => <Tag color="blue">v{value}</Tag> },
            { title: '对象 ID', dataIndex: 'id', ellipsis: true, render: value => <Typography.Text copyable={{ text: value }} type="secondary">{value}</Typography.Text> },
            { title: '操作', key: 'actions', width: 170, render: (_, row) => <Space><Button icon={<EditOutlined />} onClick={() => navigate(`/config/${kind}/${row.id}`)}>编辑</Button><Button danger type="text" aria-label={`归档 ${row.name}`} icon={<DeleteOutlined />} onClick={() => confirmArchive(row)} /></Space> },
          ]}
        />
        {list.isError && <Alert type="error" showIcon title={errorMessage(list.error, '列表加载失败')} action={<Button onClick={() => void list.refetch()}>重试</Button>} />}
        <Pagination
          current={page}
          pageSize={pageSize}
          total={list.data?.total ?? 0}
          locale={zhCN.Pagination}
          showSizeChanger={false}
          showQuickJumper={{ goButton: <Button aria-label="跳转">跳转</Button> }}
          showTotal={total => `共 ${total} 条`}
          onChange={setPage}
          itemRender={(_, type, original) => type === 'prev' ? '上一页' : type === 'next' ? '下一页' : original}
        />
      </Space>
    </Card>
  </div>
}
