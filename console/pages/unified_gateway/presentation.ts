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

// 关系查询里 blocked_by 的原因码。后端给的是稳定标识符，话术留在前端，
// 这样改措辞不用碰接口。`<scope>_<state>` 形式的码由 describeRelationBlock 兜底。
const relationBlockLabels: Record<string, string> = {
  commercial_validation_missing: '缺有效商业校验',
  entitlement_validation_missing: '缺有效授权校验',
  secret_identity_inactive: '密钥身份未激活',
  execution_grant_missing: '缺 execution 授权',
  credential_version_unusable: '密钥版本失效或过期',
  circuit_broken: '熔断中',
};
const relationBlockScopes: Record<string, string> = {
  offering: 'Offering', credential: '凭据', pool: '密钥池', channel: '渠道',
};
export const describeRelationBlock = (code: string) => {
  if (relationBlockLabels[code]) return relationBlockLabels[code];
  const separator = code.indexOf('_');
  const scope = separator > 0 ? relationBlockScopes[code.slice(0, separator)] : undefined;
  return scope ? `${scope}状态 ${code.slice(separator + 1) || 'unset'}` : code;
};
