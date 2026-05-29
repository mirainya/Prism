import React, { useEffect, useState } from 'react';
import {
  XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
  PieChart, Pie, Cell, AreaChart, Area
} from 'recharts';
import { TrendingUp, Activity, AlertCircle, DollarSign, ArrowUpRight, ArrowDownRight, RefreshCw } from 'lucide-react';
import { fetchDashboardStats } from '../services/api';
import { DashboardStats } from '../types';

const COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)'];
const COLORS_RAW = ['#8b5cf6', '#ec4899', '#6366f1', '#a78bfa', '#f472b6'];

const Dashboard: React.FC = () => {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loading, setLoading] = useState(true);

  const loadStats = async () => {
    setLoading(true);
    try {
      const data = await fetchDashboardStats();
      setStats(data);
    } catch (e) {
      console.error('Failed to load dashboard stats', e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadStats();
  }, []);

  if (loading || !stats) {
    return (
      <div className="space-y-4 md:space-y-8">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">系统概览</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">实时监控及用量统计数据</p>
        </div>
        <div className="grid grid-cols-2 md:grid-cols-2 lg:grid-cols-4 gap-3 md:gap-6">
          {[1, 2, 3, 4].map(i => (
            <div key={i} className="glass-card p-4 md:p-6 animate-pulse">
              <div className="h-4 bg-[var(--border-soft)] rounded w-1/2 mb-3"></div>
              <div className="h-8 bg-[var(--border-soft)] rounded w-3/4"></div>
            </div>
          ))}
        </div>
      </div>
    );
  }

  const { today, weekly_trend, capability_dist } = stats;
  const weeklyTrend = (weekly_trend || []).map((item, index) => {
    const fallbackDate = (() => {
      const d = new Date(Date.now() - (6 - index) * 86400000);
      return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    })();
    return {
      ...item,
      date: item.date || fallbackDate,
      requests: Number(item.requests) || 0,
      errors: Number(item.errors) || 0,
      cost: Number(item.cost) || 0,
    };
  });

  const statCards = [
    {
      label: '今日请求数',
      value: today.total_requests.toLocaleString(),
      trend: today.request_trend,
      trendLabel: `较昨日 ${today.request_trend >= 0 ? '+' : ''}${today.request_trend.toFixed(1)}%`,
      icon: <TrendingUp size={22} />,
      gradient: 'from-[var(--primary)] to-[var(--primary-light)]',
    },
    {
      label: '成功数',
      value: today.success_count.toLocaleString(),
      trend: 1,
      trendLabel: `成功率 ${today.total_requests > 0 ? ((today.success_count / today.total_requests) * 100).toFixed(1) : 0}%`,
      icon: <Activity size={22} />,
      gradient: 'from-emerald-400 to-emerald-300',
    },
    {
      label: '今日消耗',
      value: `¥${today.total_cost.toFixed(2)}`,
      trend: -today.cost_trend,
      trendLabel: `较昨日 ${today.cost_trend >= 0 ? '+' : ''}${today.cost_trend.toFixed(1)}%`,
      icon: <DollarSign size={22} />,
      gradient: 'from-amber-400 to-amber-300',
    },
    {
      label: '错误率',
      value: `${today.error_rate.toFixed(1)}%`,
      trend: -today.error_rate,
      trendLabel: `失败 ${today.failed_count} 次`,
      icon: <AlertCircle size={22} />,
      gradient: 'from-rose-400 to-rose-300',
    },
  ];

  return (
    <div className="space-y-4 md:space-y-8">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">系统概览</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">实时监控及用量统计数据</p>
        </div>
        <button
          onClick={loadStats}
          className="px-3 md:px-4 py-2 glass-card text-sm font-medium text-[var(--text-secondary)] hover:text-[var(--primary)] transition-colors flex items-center gap-2"
        >
          <RefreshCw size={16} className={loading ? 'animate-spin' : ''} />
          <span className="hidden md:inline">刷新</span>
        </button>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3 md:gap-6">
        {statCards.map((card, i) => (
          <div key={i} className="glass-card hover-lift p-4 md:p-6 card-enter flex items-start justify-between">
            <div>
              <p className="text-xs md:text-sm text-[var(--text-secondary)] font-medium">{card.label}</p>
              <h3 className="text-lg md:text-2xl font-bold text-[var(--text-primary)] mt-1">{card.value}</h3>
              <div className={`flex items-center mt-2 text-xs font-medium ${card.trend >= 0 ? 'text-emerald-600' : 'text-rose-600'}`}>
                {card.trend >= 0 ? <ArrowUpRight size={14} className="mr-1" /> : <ArrowDownRight size={14} className="mr-1" />}
                {card.trendLabel}
              </div>
            </div>
            <div className={`p-3 rounded-xl bg-gradient-to-br ${card.gradient} text-white shadow-md`}>
              {card.icon}
            </div>
          </div>
        ))}
      </div>

      {/* Charts */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-3 md:gap-6">
        {/* Weekly Trend */}
        <div className="lg:col-span-2 glass-card p-4 md:p-6">
          <h3 className="text-base md:text-lg font-semibold text-[var(--text-primary)] mb-4">近 7 日趋势</h3>
          <div className="h-[200px] md:h-[280px]">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={weeklyTrend}>
                <defs>
                  <linearGradient id="colorRequests" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--primary)" stopOpacity={0.2} />
                    <stop offset="95%" stopColor="var(--primary)" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="colorErrors" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--accent)" stopOpacity={0.2} />
                    <stop offset="95%" stopColor="var(--accent)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border-soft)" />
                <XAxis dataKey="date" tick={{ fill: 'var(--text-secondary)', fontSize: 12 }} />
                <YAxis tick={{ fill: 'var(--text-secondary)', fontSize: 12 }} />
                <Tooltip
                  contentStyle={{
                    background: 'var(--surface-card)',
                    border: '1px solid var(--border-soft)',
                    borderRadius: '12px',
                    backdropFilter: 'blur(10px)',
                  }}
                />
                <Area type="monotone" dataKey="requests" stroke="var(--primary)" fill="url(#colorRequests)" strokeWidth={2} name="请求数" />
                <Area type="monotone" dataKey="errors" stroke="var(--accent)" fill="url(#colorErrors)" strokeWidth={2} name="错误数" />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* Capability Distribution */}
        <div className="glass-card p-4 md:p-6">
          <h3 className="text-base md:text-lg font-semibold text-[var(--text-primary)] mb-4">能力调用分布</h3>
          {!capability_dist || capability_dist.length === 0 ? (
            <div className="h-[220px] flex items-center justify-center text-[var(--text-secondary)] text-sm">
              暂无数据
            </div>
          ) : (
            <>
              <div className="h-[220px] w-full flex items-center justify-center">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={capability_dist}
                      innerRadius={60}
                      outerRadius={90}
                      paddingAngle={4}
                      dataKey="count"
                      nameKey="capability"
                    >
                      {capability_dist.map((_, index) => (
                        <Cell key={`cell-${index}`} fill={COLORS_RAW[index % COLORS_RAW.length]} strokeWidth={0} />
                      ))}
                    </Pie>
                    <Tooltip formatter={(value: number) => [value + ' 次', '调用数']} />
                  </PieChart>
                </ResponsiveContainer>
              </div>
              <div className="mt-4 grid grid-cols-2 gap-3">
                {capability_dist.slice(0, 4).map((item, index) => (
                  <div key={item.capability} className="flex items-center gap-2">
                    <div className="w-2.5 h-2.5 rounded-full" style={{ backgroundColor: COLORS_RAW[index % COLORS_RAW.length] }}></div>
                    <span className="text-xs text-[var(--text-secondary)] font-medium truncate">{item.capability}</span>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
};

export default Dashboard;
