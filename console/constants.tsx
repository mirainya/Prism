import {
    LayoutDashboard,
    Layers,
    Users,
    Key,
    FileText,
    Zap,
    Book,
    Activity,
    MessageSquare,
    Lock,
    Play,
    Bot,
    Server,
    ScrollText
} from 'lucide-react';

export const ROUTES = [
  { path: '/dashboard', name: '仪表盘', icon: <LayoutDashboard size={20} />, roles: ['admin', 'user'] },
  // chat 域配置
  { path: '/gateway-channels', name: '网关渠道', icon: <Server size={20} />, roles: ['admin'] },
  { path: '/chat-models', name: '对话模型', icon: <Bot size={20} />, roles: ['admin'] },
  // 能力域配置
  { path: '/channels', name: '能力渠道', icon: <Layers size={20} />, roles: ['admin'] },
  { path: '/capabilities', name: '能力配置', icon: <Zap size={20} />, roles: ['admin'] },
  // 系统配置
  { path: '/users', name: '用户管理', icon: <Users size={20} />, roles: ['admin'] },
  // 使用
  { path: '/tokens', name: '令牌管理', icon: <Key size={20} />, roles: ['user', 'admin'] },
  { path: '/playground', name: '在线试用', icon: <Play size={20} />, roles: ['user', 'admin'] },
  { path: '/api-docs', name: 'API 文档', icon: <Book size={20} />, roles: ['user', 'admin'] },
  // 日志
  { path: '/calls', name: '调用记录', icon: <Activity size={20} />, roles: ['user', 'admin'] },
  { path: '/observability', name: '审计与流水', icon: <ScrollText size={20} />, roles: ['user', 'admin'] },
  { path: '/logs', name: '异步任务', icon: <FileText size={20} />, roles: ['user', 'admin'] },
  { path: '/chat-logs', name: '对话记录', icon: <MessageSquare size={20} />, roles: ['user', 'admin'] },
  // 账户
  { path: '/change-password', name: '修改密码', icon: <Lock size={20} />, roles: ['user', 'admin'] },
];

export const STATUS_COLORS = {
  active: 'bg-green-100 text-green-700',
  inactive: 'bg-gray-100 text-gray-700',
  cooldown: 'bg-yellow-100 text-yellow-700',
  expired: 'bg-red-100 text-red-700',
};

export const STATUS_LABELS = {
  active: '已启用',
  inactive: '已禁用',
  cooldown: '冷却中',
  expired: '已过期',
};
