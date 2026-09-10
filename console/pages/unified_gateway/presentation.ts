import type { UnifiedGatewayOverview } from '../../services/unifiedGatewayApi';

export const gatewayStatus = (data: UnifiedGatewayOverview | null, failed = false) => {
  if (failed) return { ready: false, label: '运行状态读取失败' };
  if (!data) return { ready: false, label: '运行状态未读取' };
  if (data.legacy.channels > 0 || data.legacy.abilities > 0) return { ready: false, label: '迁移未完成' };
  if (data.runtime_ready && data.ready_for_cutover) return { ready: true, label: '运行条件已满足' };
  if (data.target.channels === 0 && data.target.catalog_releases === 0) return { ready: false, label: '尚未配置' };
  return { ready: false, label: '运行条件未满足' };
};

export const gatewayChecks = (data: UnifiedGatewayOverview | null) => [
  { label: '活动目录', ok: Boolean(data?.runtime.active_release_id), value: data?.runtime.active_release_id ? `#${data.runtime.active_release_id}` : '未激活' },
  { label: '正式售价', ok: (data?.target.sell_rates ?? 0) > 0, value: data ? `${data.target.sell_rates ?? 0} 项` : '-' },
  { label: '成本费率', ok: (data?.target.cost_rates ?? 0) > 0, value: data ? `${data.target.cost_rates ?? 0} 项` : '-' },
  { label: '结算币种', ok: (data?.target.currencies ?? 0) > 0, value: data?.target.currencies ? '已配置' : '未配置' },
  { label: '目录与加密证明', ok: Boolean(data?.runtime_ready), value: data?.runtime_ready ? '已通过' : '未通过' },
];

export const formatDate = (value?: string | null) => {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString('zh-CN', { hour12: false });
};
const statusLabels: Record<string, string> = {
  draft: '草稿', published: '已发布', retired: '已退役', active: '启用', disabled: '停用',
  draining: '排空中', preparing: '准备中', completed: '成功', failed: '失败', cancelled: '已取消',
  received: '已接收', in_progress: '处理中', started: '执行中', not_created: '未创建',
  submitting: '提交中', submission_unknown: '提交结果未知', accepted: '上游已接受', running: '生成中',
  manual_review: '待核对', succeeded: '成功', terminated_unknown: '终态未知', indeterminate: '结果待核对',
  prepared: '待发送', dispatching: '发送中', sent: '已发送', response_recorded: '响应已记录',
  unknown: '响应未知', not_sent: '未发送', recovery_pending: '恢复中', retry_pending: '等待重试',
  scheduled: '等待执行', submitted: '待审核', rejected: '已拒绝', pending: '待确认',
  confirmed: '已确认', dismissed: '已忽略',
};
export const statusLabel = (status: string) => statusLabels[status] || status;
