import { useState } from 'react'
import { Alert, Badge, Button, Descriptions, Drawer, Flex, Modal, Space, Tag, Tooltip, Typography, message } from 'antd'
import { ApiOutlined, CheckCircleOutlined, ExclamationCircleOutlined, ReloadOutlined, SettingOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { apiRequest, errorMessage, newIdempotencyKey } from '../api'
import { useAppState } from '../context/AppContext'
import { capabilityName, runtimeActionNames, runtimeReasonSummary } from '../runtimeCopy'

const stateColor: Record<string, string> = {
  available: 'success', healthy: 'success', ready: 'success', degraded: 'warning', unavailable: 'error', stopped: 'default',
}

const stateLabel: Record<string, string> = {
  available: '可用', healthy: '健康', ready: '就绪', degraded: '降级', unavailable: '不可用', stopped: '已停止',
  loading_settings: '加载设置', recovering: '恢复中',
}

export default function RuntimeStatus() {
  const { project, runtime, runtimeCapabilities, runtimeError, refreshRuntime } = useAppState()
  const [open, setOpen] = useState(false)
  const [runningAction, setRunningAction] = useState('')
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()
  const phase = runtime?.phase ?? 'unavailable'
  const actions = new Map<string, NonNullable<typeof runtime>['capabilities'][number]['actions'][number]>()
  runtime?.capabilities.forEach(capability => {
    if (capability.id === 'backup' && capability.reasons.some(reason => reason.code === 'BACKUP_PROJECT_UNAVAILABLE')) return
    capability.actions.forEach(action => actions.set(`${action.id}:${action.uri}`, action))
  })
  const restoreState = runtimeCapabilities?.backup.restore_state ?? 'idle'
  const maintenance = ['maintenance', 'recovering', 'recovery_required'].includes(restoreState)

  async function refreshStatus() {
    setRunningAction('runtime.refresh')
    try {
      await apiRequest('/api/v1/runtime/reprobe', {
        method: 'POST',
        headers: { 'Idempotency-Key': newIdempotencyKey() },
      })
      await refreshRuntime()
      messageApi.success('运行状态已刷新')
    } catch (cause) {
      await refreshRuntime().catch(() => undefined)
      messageApi.error(errorMessage(cause, '运行状态刷新失败'))
    } finally {
      setRunningAction('')
    }
  }

  async function runAction(action: (typeof actions extends Map<string, infer V> ? V : never)) {
    setRunningAction(action.id)
    try {
      await apiRequest(action.uri, {
        method: action.method,
        headers: action.idempotency_required ? { 'Idempotency-Key': newIdempotencyKey() } : undefined,
      })
      await refreshRuntime()
      messageApi.success('操作已提交，运行状态已刷新')
    } catch (cause) {
      messageApi.error(errorMessage(cause, '运行时操作失败'))
    } finally {
      setRunningAction('')
    }
  }

  return (
    <>
      {contextHolder}
      <Tooltip title={runtimeError || '查看本地运行状态'}>
        <Button className="runtime-trigger" type="text" onClick={() => setOpen(true)}>
          <Badge status={phase === 'ready' ? 'success' : phase === 'degraded' ? 'warning' : 'error'} />
          <span className="runtime-trigger-label">{runtimeError ? '状态未知' : stateLabel[phase] ?? phase}</span>
        </Button>
      </Tooltip>

      <Drawer title={<Space><ApiOutlined />本地运行状态</Space>} size={520} open={open} onClose={() => setOpen(false)}
        extra={<Button icon={<ReloadOutlined />} loading={runningAction === 'runtime.refresh'} disabled={runningAction !== '' && runningAction !== 'runtime.refresh'} onClick={() => void refreshStatus()}>刷新</Button>}>
        {runtimeError && <Alert type="error" showIcon title="无法读取运行状态" description={runtimeError} className="block-alert" />}
        {runtime && <>
          <Descriptions bordered size="small" column={1} className="runtime-descriptions">
            <Descriptions.Item label="运行阶段"><Tag color={stateColor[phase]}>{stateLabel[phase] ?? phase}</Tag></Descriptions.Item>
            <Descriptions.Item label="当前项目">{project?.name ?? '尚未打开'}</Descriptions.Item>
            <Descriptions.Item label="应用版本">{runtime.build.version} · {runtime.build.package_mode}</Descriptions.Item>
            <Descriptions.Item label="监听地址"><Typography.Text copyable code>{runtime.listener.url}</Typography.Text></Descriptions.Item>
            <Descriptions.Item label="日志位置"><Typography.Text copyable code>{runtime.log_location}</Typography.Text></Descriptions.Item>
          </Descriptions>
          {runtime.project.state === 'none' && <Alert type="info" showIcon className="block-alert" title="打开项目后可使用备份与发布"
            description="当前没有打开项目。创建或打开项目后，系统会自动重新检查这些能力。"
            action={<Button onClick={() => { setOpen(false); navigate('/projects') }}>前往项目空间</Button>} />}
          <Typography.Title level={5}>能力与依赖</Typography.Title>
          <Flex vertical className="runtime-capability-list">
            {runtime.capabilities.map(item => <div className="runtime-capability-row" key={item.id}>
              <span className="runtime-capability-icon">{item.state === 'available' ? <CheckCircleOutlined className="status-ok" /> : <ExclamationCircleOutlined className="status-warn" />}</span>
              <span className="runtime-capability-copy"><strong>{capabilityName(item.id)}</strong>
                <Tooltip title={item.reasons.length ? `诊断码：${item.reasons.map(reason => reason.code).join('、')}` : undefined}>
                  <small>{item.state === 'available' ? '已就绪' : runtimeReasonSummary(item.reasons, runtime.project.state === 'active')}</small>
                </Tooltip>
              </span>
              <Tag color={stateColor[item.state]}>{stateLabel[item.state] ?? item.state}</Tag>
            </div>)}
          </Flex>
          {actions.size > 0 && <>
            <Typography.Title level={5}>可用操作</Typography.Title>
            <Flex gap="small" wrap>
              {[...actions.values()].map(action => {
                const settings = ['provider.settings', 'credential.configure', 'backup.settings'].includes(action.id)
                const restore = action.id === 'restore.inspect'
                return <Button key={`${action.id}:${action.uri}`} loading={runningAction === action.id}
                  onClick={() => {
                    if (settings || restore) {
                      setOpen(false)
                      navigate(restore ? '/backups' : action.id === 'credential.configure' ? '/ai-design' : '/settings')
                    } else {
                      void runAction(action)
                    }
                  }}>
                  {runtimeActionNames[action.id] ?? '查看操作'}
                </Button>
              })}
            </Flex>
          </>}
        </>}
      </Drawer>

      <Modal
        open={maintenance}
        closable={false}
        mask={{ closable: false }}
        keyboard={false}
        title={<Space><ExclamationCircleOutlined className="status-warn" />项目处于维护保护状态</Space>}
        footer={<Space><Button onClick={() => navigate('/backups')}>查看恢复与任务状态</Button><Button type="primary" icon={<SettingOutlined />} onClick={() => navigate('/settings')}>运行设置</Button></Space>}
      >
        <Alert type="warning" showIcon title="恢复流程尚未完成" description="维护期间不能取消、修改、切换或关闭项目。请等待服务端确认恢复完成。" />
      </Modal>
    </>
  )
}
