import React, { useEffect, useMemo, useState } from 'react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import {
  Activity,
  AlertCircle,
  ArrowDownRight,
  ArrowUpRight,
  DollarSign,
  LayoutDashboard,
  RefreshCw,
  TrendingUp,
  type LucideIcon,
} from 'lucide-react';
import { fetchDashboardStats } from '../services/api';
import type { DashboardStats } from '../types';
import { PageHeader } from '../components/shell';

const CHART_COLORS = [
  'var(--candy-pink)',
  'var(--candy-mint)',
  'var(--candy-blue)',
  'var(--candy-yellow)',
  'var(--candy-lilac)',
];

interface Metric {
  label: string;
  value: string;
  note: string;
  trend?: number;
  icon: LucideIcon;
  color: string;
}

const DashboardSkeleton: React.FC = () => (
  <div role="status" aria-label="总览数据加载中" className="space-y-4">
    <section className="grid overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)] sm:grid-cols-2 xl:grid-cols-4">
      {[0, 1, 2, 3].map(item => (
        <div key={item} className="border-b border-[var(--border-soft)] p-5 sm:border-r xl:border-b-0 last:border-0">
          <div className="candy-skeleton h-3 w-24 rounded-md" />
          <div className="candy-skeleton mt-4 h-7 w-32 rounded-md" />
          <div className="candy-skeleton mt-3 h-3 w-28 rounded-md opacity-70" />
        </div>
      ))}
    </section>

    <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(320px,1fr)]">
      <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-5 shadow-[var(--shadow-soft)]">
        <div className="flex items-center justify-between gap-3">
          <div className="candy-skeleton h-4 w-28 rounded-md" />
          <div className="candy-skeleton h-3 w-20 rounded-md" />
        </div>
        <div className="mt-10 flex h-64 items-end gap-3 border-b border-[var(--border-soft)] px-2">
          {[48, 58, 78, 68, 88, 73, 84, 64, 72, 56, 62, 44].map((height, index) => (
            <div key={index} className="candy-skeleton flex-1 rounded-t-sm" style={{ height: `${height}%` }} />
          ))}
        </div>
      </section>

      <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-5 shadow-[var(--shadow-soft)]">
        <div className="candy-skeleton h-4 w-28 rounded-md" />
        <div className="mx-auto mt-8 h-48 w-48 rounded-full border-[32px] border-[var(--surface-muted)]" />
        <div className="mt-7 grid grid-cols-2 gap-3">
          {[0, 1, 2, 3].map(item => <div key={item} className="candy-skeleton h-3 rounded-md" />)}
        </div>
      </section>
    </div>
  </div>
);

const MetricCell: React.FC<{ metric: Metric }> = ({ metric }) => {
  const Icon = metric.icon;
  return (
    <article className="flex min-w-0 items-start justify-between gap-4 border-b border-[var(--border-soft)] p-4 sm:border-r sm:p-5 xl:border-b-0 last:border-0">
      <div className="min-w-0">
        <div className="flex items-center gap-2 text-xs font-semibold text-[var(--text-secondary)]">
          <span className="h-2 w-2 rounded-full" style={{ backgroundColor: metric.color }} />
          {metric.label}
        </div>
        <div className="mt-2 truncate text-2xl font-extrabold text-[var(--text-primary)]">{metric.value}</div>
        <div className="mt-1.5 flex items-center gap-1 text-xs text-[var(--text-secondary)]">
          {metric.trend != null && metric.trend !== 0 && (
            metric.trend > 0 ? <ArrowUpRight size={13} /> : <ArrowDownRight size={13} />
          )}
          <span className="truncate">{metric.note}</span>
        </div>
      </div>
      <span
        className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg"
        style={{ color: metric.color, backgroundColor: `color-mix(in srgb, ${metric.color} 13%, white)` }}
      >
        <Icon size={19} />
      </span>
    </article>
  );
};

const Dashboard: React.FC = () => {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

  const loadStats = async () => {
    setLoading(true);
    setError('');
    try {
      const data = await fetchDashboardStats();
      setStats(data);
      setUpdatedAt(new Date());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '总览数据加载失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadStats();
  }, []);

  const weeklyTrend = useMemo(() => (stats?.weekly_trend || []).map((item, index) => {
    const fallback = new Date(Date.now() - (6 - index) * 86400000);
    const fallbackDate = `${String(fallback.getMonth() + 1).padStart(2, '0')}-${String(fallback.getDate()).padStart(2, '0')}`;
    return {
      ...item,
      date: item.date || fallbackDate,
      requests: Number(item.requests) || 0,
      errors: Number(item.errors) || 0,
      cost: Number(item.cost) || 0,
    };
  }), [stats?.weekly_trend]);

  const metrics = useMemo<Metric[]>(() => {
    if (!stats) return [];
    const { today } = stats;
    const successRate = today.total_requests > 0 ? (today.success_count / today.total_requests) * 100 : 0;
    return [
      {
        label: '今日请求',
        value: today.total_requests.toLocaleString(),
        note: `较昨日 ${today.request_trend >= 0 ? '+' : ''}${today.request_trend.toFixed(1)}%`,
        trend: today.request_trend,
        icon: TrendingUp,
        color: 'var(--candy-pink)',
      },
      {
        label: '请求成功率',
        value: `${successRate.toFixed(1)}%`,
        note: `${today.success_count.toLocaleString()} 次成功`,
        icon: Activity,
        color: 'var(--candy-mint)',
      },
      {
        label: '今日消耗',
        value: `¥${today.total_cost.toFixed(2)}`,
        note: `较昨日 ${today.cost_trend >= 0 ? '+' : ''}${today.cost_trend.toFixed(1)}%`,
        trend: today.cost_trend,
        icon: DollarSign,
        color: 'var(--candy-yellow)',
      },
      {
        label: '错误率',
        value: `${today.error_rate.toFixed(1)}%`,
        note: `${today.failed_count.toLocaleString()} 次失败`,
        icon: AlertCircle,
        color: 'var(--candy-red)',
      },
    ];
  }, [stats]);

  return (
    <div className="space-y-4">
      <PageHeader
        icon={LayoutDashboard}
        title="系统概览"
        meta={loading ? '正在同步统计数据' : updatedAt ? `更新于 ${updatedAt.toLocaleTimeString('zh-CN', { hour12: false })}` : '统计数据暂不可用'}
        actions={(
          <button
            type="button"
            title="刷新"
            aria-label="刷新"
            onClick={loadStats}
            disabled={loading}
            className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-secondary)] shadow-[var(--shadow-soft)] transition hover:border-[var(--primary)] hover:text-[var(--primary)] disabled:opacity-60"
          >
            <RefreshCw size={17} className={loading ? 'animate-spin' : ''} />
          </button>
        )}
      />

      {loading && !stats ? (
        <DashboardSkeleton />
      ) : !stats ? (
        <section className="flex min-h-56 flex-col items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-6 text-center shadow-[var(--shadow-soft)]">
          <AlertCircle size={24} className="text-[var(--candy-red)]" />
          <div className="mt-3 text-sm font-semibold text-[var(--text-primary)]">总览数据加载失败</div>
          <div className="mt-1 max-w-xl break-words text-xs text-[var(--text-secondary)]">{error}</div>
          <button type="button" onClick={loadStats} className="mt-4 rounded-lg [background:var(--brand-gradient)] px-4 py-2 text-sm font-semibold text-white">重新加载</button>
        </section>
      ) : (
        <>
          {error && (
            <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              <AlertCircle size={16} />{error}
            </div>
          )}

          <section className="grid overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)] sm:grid-cols-2 xl:grid-cols-4">
            {metrics.map(metric => <MetricCell key={metric.label} metric={metric} />)}
          </section>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(320px,1fr)]">
            <section className="min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 shadow-[var(--shadow-soft)] md:p-5">
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-soft)] pb-4">
                <div>
                  <h2 className="text-sm font-bold text-[var(--text-primary)]">近 7 日趋势</h2>
                  <div className="mt-1 text-xs text-[var(--text-secondary)]">请求量与错误数</div>
                </div>
                <div className="flex items-center gap-4 text-xs text-[var(--text-secondary)]">
                  <span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-full bg-[var(--candy-pink)]" />请求</span>
                  <span className="flex items-center gap-1.5"><span className="h-2 w-2 rounded-full bg-[var(--candy-blue)]" />错误</span>
                </div>
              </div>
              <div className="mt-4 h-[260px] md:h-[320px]">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={weeklyTrend} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
                    <defs>
                      <linearGradient id="dashboardRequests" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="var(--candy-pink)" stopOpacity={0.2} />
                        <stop offset="95%" stopColor="var(--candy-pink)" stopOpacity={0} />
                      </linearGradient>
                      <linearGradient id="dashboardErrors" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="var(--candy-blue)" stopOpacity={0.14} />
                        <stop offset="95%" stopColor="var(--candy-blue)" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid vertical={false} stroke="var(--border-soft)" />
                    <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fill: 'var(--text-secondary)', fontSize: 11 }} />
                    <YAxis axisLine={false} tickLine={false} tick={{ fill: 'var(--text-secondary)', fontSize: 11 }} />
                    <Tooltip
                      contentStyle={{
                        background: 'var(--surface-card)',
                        border: '1px solid var(--border-soft)',
                        borderRadius: '8px',
                        boxShadow: 'var(--shadow-soft)',
                      }}
                    />
                    <Area type="monotone" dataKey="requests" stroke="var(--candy-pink)" fill="url(#dashboardRequests)" strokeWidth={2} name="请求数" />
                    <Area type="monotone" dataKey="errors" stroke="var(--candy-blue)" fill="url(#dashboardErrors)" strokeWidth={2} name="错误数" />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </section>

            <section className="min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 shadow-[var(--shadow-soft)] md:p-5">
              <div className="border-b border-[var(--border-soft)] pb-4">
                <h2 className="text-sm font-bold text-[var(--text-primary)]">能力调用分布</h2>
                <div className="mt-1 text-xs text-[var(--text-secondary)]">按能力统计调用次数</div>
              </div>
              {!stats.capability_dist || stats.capability_dist.length === 0 ? (
                <div className="flex h-[320px] items-center justify-center text-sm text-[var(--text-secondary)]">暂无数据</div>
              ) : (
                <>
                  <div className="h-[250px] w-full">
                    <ResponsiveContainer width="100%" height="100%">
                      <PieChart>
                        <Pie data={stats.capability_dist} innerRadius={62} outerRadius={92} paddingAngle={3} dataKey="count" nameKey="capability">
                          {stats.capability_dist.map((item, index) => (
                            <Cell key={`${item.capability}-${index}`} fill={CHART_COLORS[index % CHART_COLORS.length]} strokeWidth={0} />
                          ))}
                        </Pie>
                        <Tooltip
                          formatter={(value: number) => [`${value} 次`, '调用数']}
                          contentStyle={{ border: '1px solid var(--border-soft)', borderRadius: '8px', boxShadow: 'var(--shadow-soft)' }}
                        />
                      </PieChart>
                    </ResponsiveContainer>
                  </div>
                  <div className="grid grid-cols-2 gap-x-4 gap-y-3 border-t border-[var(--border-soft)] pt-4">
                    {stats.capability_dist.slice(0, 6).map((item, index) => (
                      <div key={item.capability} className="flex min-w-0 items-center gap-2">
                        <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: CHART_COLORS[index % CHART_COLORS.length] }} />
                        <span className="min-w-0 flex-1 truncate text-xs text-[var(--text-secondary)]" title={item.capability}>{item.capability}</span>
                        <span className="text-xs font-semibold text-[var(--text-primary)]">{item.count}</span>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </section>
          </div>
        </>
      )}
    </div>
  );
};

export default Dashboard;
