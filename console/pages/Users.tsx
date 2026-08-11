import React, { useEffect, useState } from 'react';
import {Users as UsersIcon, Search, Calendar, Shield, Plus} from 'lucide-react';
import { Modal, useAppDialog } from '../components/ui';
import {fetchUsers, updateUserRole, rechargeUser} from '../services/api';
import { User, UserRole } from '../types';

const Users: React.FC = () => {
  const { askConfirmation, showAlert } = useAppDialog();
  const [users, setUsers] = useState<User[]>([]);
  const [isLoading, setIsLoading] = useState(true);
    const [rechargeTarget, setRechargeTarget] = useState<User | null>(null);
    const [rechargeAmount, setRechargeAmount] = useState('');
    const [isRecharging, setIsRecharging] = useState(false);

  const loadUsers = () => {
    setIsLoading(true);
    fetchUsers()
      .then(data => setUsers(data))
      .finally(() => setIsLoading(false));
  };

  useEffect(() => {
    loadUsers();
  }, []);

  const handleRoleChange = async (user: User) => {
    const newRole = user.role === UserRole.ADMIN ? UserRole.USER : UserRole.ADMIN;
    const roleLabel = newRole === UserRole.ADMIN ? '管理员' : '普通用户';
    const confirmed = await askConfirmation({
      title: '修改用户角色？',
      description: `将“${user.username}”的角色修改为${roleLabel}。`,
      confirmLabel: '修改角色',
      tone: 'warning',
    });
    if (!confirmed) return;
    try {
      await updateUserRole(user.id, newRole);
      loadUsers();
    } catch (err: any) {
      await showAlert({ title: '操作失败', description: err.message || '角色修改失败，请稍后重试。', tone: 'danger' });
    }
  };

    const handleRecharge = async () => {
        if (!rechargeTarget) return;
        const amount = Number(rechargeAmount);
        if (!amount || amount <= 0) {
            await showAlert({ title: '金额无效', description: '请输入大于 0 的充值金额。', tone: 'warning' });
            return;
        }
        setIsRecharging(true);
        try {
            await rechargeUser(rechargeTarget.id, amount);
            setRechargeTarget(null);
            setRechargeAmount('');
            loadUsers();
        } catch (err: any) {
            await showAlert({ title: '充值失败', description: err.message || '用户充值失败，请稍后重试。', tone: 'danger' });
        } finally {
            setIsRecharging(false);
        }
    };

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">用户管理</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">管理系统注册用户、角色分配及账户余额</p>
        </div>
      </div>

      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
        <div className="p-3 md:p-4 border-b border-[var(--border-soft)] bg-[var(--surface)]/50 flex items-center justify-between flex-wrap gap-3">
          <div className="relative w-full md:w-72">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={16} />
            <input
              type="text"
              placeholder="搜索用户名..."
              className="w-full pl-9 pr-4 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
            />
          </div>
          <div className="flex gap-4">
            <div className="px-3 py-1 bg-[var(--primary-lighter)] text-[var(--primary)] text-xs font-bold rounded-full">
              共 {users.length} 位用户
            </div>
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left min-w-[600px]">
            <thead>
              <tr className="border-b border-[var(--border-soft)]">
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">用户信息</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">角色</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">余额</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider hidden sm:table-cell">注册日期</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {isLoading ? (
                Array.from({ length: 4 }).map((_, i) => (
                  <tr key={i} className="animate-pulse">
                    <td colSpan={5} className="px-3 md:px-6 py-4 md:py-6"><div className="h-4 bg-[var(--primary-lighter)] rounded w-full"></div></td>
                  </tr>
                ))
              ) : users.map(user => (
                <tr key={user.id} className="hover:bg-[var(--surface)] transition-colors group">
                  <td className="px-3 md:px-6 py-3 md:py-4">
                    <div className="flex items-center gap-2 md:gap-3">
                      <div className="w-8 h-8 md:w-9 md:h-9 bg-[var(--primary-lighter)] rounded-full flex items-center justify-center text-[var(--primary)]">
                        <UsersIcon size={16} />
                      </div>
                      <div>
                        <div className="text-sm font-bold text-[var(--text-primary)]">{user.username}</div>
                        <div className="text-[10px] text-[var(--text-secondary)] font-mono">ID: {user.id}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-3 md:px-6 py-3 md:py-4">
                    <div className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-[10px] font-bold uppercase ${user.role === UserRole.ADMIN ? 'bg-indigo-100 text-[var(--primary)]' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                      <Shield size={10} />
                      {user.role === UserRole.ADMIN ? '管理员' : '用户'}
                    </div>
                  </td>
                  <td className="px-3 md:px-6 py-3 md:py-4">
                    <span className="text-sm font-bold text-[var(--text-primary)]">{user.balance}</span>
                  </td>
                  <td className="px-3 md:px-6 py-3 md:py-4 text-sm text-[var(--text-secondary)] hidden sm:table-cell">
                    <div className="flex items-center gap-1.5">
                      <Calendar size={14} className="text-gray-300" />
                      {user.createdAt}
                    </div>
                  </td>
                  <td className="px-3 md:px-6 py-3 md:py-4 text-right">
                    <div className="flex items-center justify-end gap-2 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                      <button
                          className="px-2 md:px-3 py-1.5 bg-[var(--surface-card)] border border-[var(--border-soft)] text-xs font-bold text-[var(--text-secondary)] rounded-lg hover:border-emerald-600 hover:text-emerald-600 transition-colors flex items-center gap-1"
                          onClick={() => {
                              setRechargeTarget(user);
                              setRechargeAmount('');
                          }}
                      >
                          <Plus size={12}/>
                          充值
                      </button>
                        <button
                        className="px-2 md:px-3 py-1.5 bg-[var(--surface-card)] border border-[var(--border-soft)] text-xs font-bold text-[var(--text-secondary)] rounded-lg hover:border-indigo-600 hover:text-[var(--primary)] transition-colors flex items-center gap-1"
                        onClick={() => handleRoleChange(user)}
                      >
                        <Shield size={12} />
                        <span className="hidden md:inline">{user.role === UserRole.ADMIN ? '设为用户' : '设为管理员'}</span>
                        <span className="md:hidden">角色</span>
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

            <Modal open={Boolean(rechargeTarget)} onClose={() => setRechargeTarget(null)} title="用户充值" width="max-w-sm">
                    <div className="space-y-4">
                    <p className="text-sm text-[var(--text-secondary)]">
                        为用户 <span className="font-bold text-[var(--text-primary)]">{rechargeTarget?.username}</span> 充值额度
                    </p>
                    <p className="text-sm text-[var(--text-secondary)]">
                        当前余额: <span className="font-bold text-[var(--text-primary)]">{rechargeTarget?.balance}</span>
                    </p>
                    <input
                        type="number"
                        min="1"
                        placeholder="请输入充值金额"
                        value={rechargeAmount}
                        onChange={e => setRechargeAmount(e.target.value)}
                        className="w-full px-4 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                        autoFocus
                    />
                    <div className="flex justify-end gap-3">
                        <button
                            className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors"
                            onClick={() => setRechargeTarget(null)}
                            disabled={isRecharging}
                        >
                            取消
                        </button>
                        <button
                            className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 transition-colors disabled:opacity-50"
                            onClick={handleRecharge}
                            disabled={isRecharging}
                        >
                            {isRecharging ? '充值中...' : '确认充值'}
                        </button>
                    </div>
                    </div>
            </Modal>
    </div>
  );
};

export default Users;
