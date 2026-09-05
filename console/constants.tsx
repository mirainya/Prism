import { ReactElement } from 'react';
import {
    LayoutDashboard,
    Layers,
    Layers3,
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
    ScrollText,
    Film,
    Video,
    Gauge,
    Network,
    Puzzle,
    Clapperboard,
    Sparkles,
    ClipboardList,
} from 'lucide-react';

export interface RouteItem {
  path: string;
  name: string;
  icon: ReactElement;
  roles: string[];
}

export interface RouteGroup {
  key: string;
  label: string;
  icon: ReactElement;
  roles: string[];
  children: RouteItem[];
}

export const ROUTE_GROUPS: RouteGroup[] = [
  {
    key: 'overview',
    label: '总览',
    icon: <Gauge size={18} />,
    roles: ['admin', 'user'],
    children: [
      { path: '/dashboard', name: '仪表盘', icon: <LayoutDashboard size={20} />, roles: ['admin', 'user'] },
      { path: '/users', name: '用户管理', icon: <Users size={20} />, roles: ['admin'] },
    ],
  },
  {
    key: 'gateway',
    label: '网关',
    icon: <Network size={18} />,
    roles: ['admin'],
    children: [
      { path: '/gateway-channels', name: '网关渠道', icon: <Server size={20} />, roles: ['admin'] },
      { path: '/unified-gateway', name: '统一网关', icon: <Layers3 size={20} />, roles: ['admin'] },
      { path: '/chat-models', name: '对话模型', icon: <Bot size={20} />, roles: ['admin'] },
    ],
  },
  {
    key: 'capability',
    label: '能力',
    icon: <Puzzle size={18} />,
    roles: ['admin'],
    children: [
      { path: '/channels', name: '能力渠道', icon: <Layers size={20} />, roles: ['admin'] },
      { path: '/capabilities', name: '能力配置', icon: <Zap size={20} />, roles: ['admin'] },
    ],
  },
  {
    key: 'video',
    label: '视频',
    icon: <Clapperboard size={18} />,
    roles: ['admin'],
    children: [
      { path: '/video-channels', name: '视频渠道', icon: <Film size={20} />, roles: ['admin'] },
    ],
  },
  {
    key: 'usage',
    label: '使用',
    icon: <Sparkles size={18} />,
    roles: ['user', 'admin'],
    children: [
      { path: '/tokens', name: '令牌管理', icon: <Key size={20} />, roles: ['user', 'admin'] },
      { path: '/playground', name: '在线试用', icon: <Play size={20} />, roles: ['user', 'admin'] },
      { path: '/api-docs', name: 'API 文档', icon: <Book size={20} />, roles: ['user', 'admin'] },
    ],
  },
  {
    key: 'logs',
    label: '日志',
    icon: <ClipboardList size={18} />,
    roles: ['user', 'admin'],
    children: [
      { path: '/calls', name: '调用记录', icon: <Activity size={20} />, roles: ['user', 'admin'] },
      { path: '/observability', name: '审计与流水', icon: <ScrollText size={20} />, roles: ['user', 'admin'] },
      { path: '/logs', name: '异步任务', icon: <FileText size={20} />, roles: ['user', 'admin'] },
      { path: '/video-tasks', name: '视频任务', icon: <Video size={20} />, roles: ['admin'] },
      { path: '/chat-logs', name: '对话记录', icon: <MessageSquare size={20} />, roles: ['user', 'admin'] },
    ],
  },
];

// 底部独立路由（不在分组菜单中显示）
export const BOTTOM_ROUTES: RouteItem[] = [
  { path: '/change-password', name: '修改密码', icon: <Lock size={20} />, roles: ['user', 'admin'] },
];

// 兼容：扁平化所有路由，供 breadcrumb / 路由注册等使用
export const ALL_ROUTES: RouteItem[] = [
  ...ROUTE_GROUPS.flatMap(g => g.children),
  ...BOTTOM_ROUTES,
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
