import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Form, Input, InputNumber, Row, Select, Space, Switch, Typography, message } from 'antd'
import { ApiOutlined, CloudOutlined, CloudSyncOutlined, FolderOpenOutlined, SaveOutlined, SettingOutlined } from '@ant-design/icons'
import type { components } from '@/api/generated'
import PageHeader from '../components/PageHeader'
import { apiRequest, errorMessage, patchJSON } from '../api'
import { useAppState } from '../context/AppContext'

type Settings = components['schemas']['SettingsResource']
type Patch = components['schemas']['PatchSettingsRequest']
type Result = components['schemas']['SettingsUpdateResult']

export default function SettingsPage() {
  const [form] = Form.useForm<Patch>()
  const [messageApi, contextHolder] = message.useMessage()
  const client = useQueryClient()
  const { refreshRuntime, runtime } = useAppState()
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => apiRequest<Settings>('/api/v1/settings') })

  useEffect(() => {
    if (!settings.data) return
    form.setFieldsValue({
      browser: settings.data.browser,
      graph: settings.data.graph,
      ai: {
        enabled: settings.data.ai.enabled,
        endpoint: settings.data.ai.endpoint,
        model: settings.data.ai.model,
        request_timeout_seconds: settings.data.ai.request_timeout_seconds,
        allow_cloud: settings.data.ai.allow_cloud,
      },
      logs: settings.data.logs,
      backup: {
        daily_retention_count: settings.data.backup.daily_retention_count,
        release_migration_retention_count: settings.data.backup.release_migration_retention_count,
      },
    })
  }, [form, settings.data])

  function applyResult(result: Result) {
    client.setQueryData(['settings'], result.settings)
    const summary = result.effects.map(effect => `${effect.disposition}：${effect.field}`).join('；') || '设置已保存'
    messageApi.success(summary)
    void refreshRuntime()
  }

  const save = useMutation({
    mutationFn: (value: Patch) => patchJSON<Result>('/api/v1/settings', value),
    onSuccess: applyResult,
    onError: cause => messageApi.error(errorMessage(cause, '无法保存运行设置')),
  })
  const selectRoot = useMutation({
    mutationFn: () => apiRequest<Result>('/api/v1/settings/backup-root-selection', { method: 'POST' }),
    onSuccess: result => { applyResult(result); messageApi.success('自定义备份根已验证并应用') },
    onError: cause => messageApi.error(errorMessage(cause, '无法选择备份目录')),
  })
  const resetRoot = useMutation({
    mutationFn: () => patchJSON<Result>('/api/v1/settings', { backup: { use_default_root: true } }),
    onSuccess: result => { applyResult(result); messageApi.success('已恢复默认备份根') },
    onError: cause => messageApi.error(errorMessage(cause, '无法恢复默认备份目录')),
  })

  return <div className="page-container settings-page">
    {contextHolder}
    <PageHeader title="运行设置" description="管理本机运行时、Graph、AI Provider、日志与项目备份策略。凭据不会出现在此表单中。" eyebrow={<><SettingOutlined /> 本机配置</>} />
    {settings.isError && <Alert className="block-alert" type="error" showIcon title={errorMessage(settings.error, '无法读取运行设置')} action={<Button onClick={() => void settings.refetch()}>重试</Button>} />}
    <Form<Patch> form={form} layout="vertical" requiredMark={false} onFinish={value => save.mutate(value)}>
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card className="settings-card" title={<Space><CloudOutlined />启动与浏览器</Space>} loading={settings.isLoading}>
            <Form.Item name={['browser', 'auto_open']} label="服务可达后自动打开浏览器" valuePropName="checked"><Switch checkedChildren="开启" unCheckedChildren="关闭" /></Form.Item>
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card className="settings-card" title={<Space><ApiOutlined />Graph / local-rag</Space>} loading={settings.isLoading}>
            <Row gutter={16}>
              <Col span={12}><Form.Item name={['graph', 'mode']} label="模式"><Select options={[{ value: 'disabled', label: 'Disabled' }, { value: 'external', label: 'External' }, { value: 'bundled', label: 'Bundled' }]} /></Form.Item></Col>
              <Col span={12}><Form.Item name={['graph', 'restart_limit']} label="重启上限"><InputNumber min={0} max={10} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
            <Form.Item name={['graph', 'endpoint']} label="Loopback endpoint" rules={[{ type: 'url', warningOnly: true }]}><Input placeholder="http://127.0.0.1:8080" /></Form.Item>
            <Row gutter={16}>
              <Col span={12}><Form.Item name={['graph', 'health_timeout_seconds']} label="健康超时（秒）"><InputNumber min={1} max={60} style={{ width: '100%' }} /></Form.Item></Col>
              <Col span={12}><Form.Item name={['graph', 'startup_timeout_seconds']} label="启动超时（秒）"><InputNumber min={1} max={300} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card className="settings-card" title={<Space><CloudOutlined />AI Provider</Space>} loading={settings.isLoading}>
            <Form.Item name={['ai', 'enabled']} label="启用 AI 设计" valuePropName="checked"><Switch checkedChildren="启用" unCheckedChildren="停用" /></Form.Item>
            <Form.Item name={['ai', 'endpoint']} label="Endpoint" rules={[{ type: 'url', warningOnly: true }]}><Input /></Form.Item>
            <Form.Item name={['ai', 'model']} label="Model"><Input autoComplete="off" /></Form.Item>
            <Row gutter={16}>
              <Col span={12}><Form.Item name={['ai', 'request_timeout_seconds']} label="请求超时（秒）"><InputNumber min={1} max={600} style={{ width: '100%' }} /></Form.Item></Col>
              <Col span={12}><Form.Item name={['ai', 'allow_cloud']} label="允许 HTTPS Cloud" valuePropName="checked"><Switch /></Form.Item></Col>
            </Row>
            <Alert type={settings.data?.ai.credential_present ? 'success' : 'warning'} showIcon title={`凭据状态：${settings.data?.ai.credential_present ? '已配置' : '未配置'}`} description="凭据由系统凭据管理器保存，可在 AI 平衡设计页更新。" />
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card className="settings-card" title={<Space><CloudSyncOutlined />日志与项目备份</Space>} loading={settings.isLoading}>
            <Row gutter={16}>
              <Col span={12}><Form.Item name={['logs', 'max_bytes']} label="日志单文件上限（bytes）"><InputNumber min={65_536} max={1_073_741_824} style={{ width: '100%' }} /></Form.Item></Col>
              <Col span={12}><Form.Item name={['logs', 'max_files']} label="保留日志文件数"><InputNumber min={1} max={20} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
            <Typography.Paragraph type="secondary">日志位置：<Typography.Text code copyable>{runtime?.log_location ?? '等待运行状态'}</Typography.Text></Typography.Paragraph>
            <Row gutter={16}>
              <Col span={12}><Form.Item name={['backup', 'daily_retention_count']} label="日常备份保留数"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
              <Col span={12}><Form.Item name={['backup', 'release_migration_retention_count']} label="发布/迁移备份保留数"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
            </Row>
            <Space wrap>
              <Button icon={<FolderOpenOutlined />} loading={selectRoot.isPending} onClick={() => selectRoot.mutate()}>选择自定义备份目录</Button>
              <Button disabled={settings.data?.backup.root_selection_state === 'default'} loading={resetRoot.isPending} onClick={() => resetRoot.mutate()}>恢复默认目录</Button>
            </Space>
          </Card>
        </Col>
      </Row>
      <div className="sticky-form-actions"><Button type="primary" size="large" htmlType="submit" icon={<SaveOutlined />} loading={save.isPending} disabled={!settings.data}>保存设置</Button></div>
    </Form>
  </div>
}
