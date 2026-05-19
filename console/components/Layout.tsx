import React, { useState } from 'react';
import { NavLink, useNavigate } from 'react-router-dom';
import { Menu, X, LogOut, ChevronRight } from 'lucide-react';
import { ROUTES } from '../constants';
import { User, UserRole } from '../types';
import { ThemeSwitch } from '../theme/ThemeSwitch';
import logo from '@/assets/logo.svg';

interface LayoutProps {
  children: React.ReactNode;
  user: User | null;
  onLogout: () => void;
}

const Layout: React.FC<LayoutProps> = ({ children, user, onLogout }) => {
  const [isSidebarOpen, setSidebarOpen] = useState(true);

  if (!user) return <>{children}</>;

  const filteredRoutes = ROUTES.filter(route =>
    route.roles.includes(user.role)
  );

  const getBreadcrumbName = () => {
    const path = window.location.hash.split('/').pop();
    const route = ROUTES.find(r => r.path.includes(path || 'dashboard'));
    return route ? route.name : '控制台';
  };

  return (
    <div className="flex h-screen overflow-hidden" style={{ backgroundColor: 'var(--surface)' }}>
      {/* Sidebar */}
      <aside
        className={`${
          isSidebarOpen ? 'w-64' : 'w-20'
        } flex flex-col transition-all duration-300 ease-in-out z-30 border-r border-[var(--border-soft)]`}
        style={{ background: 'var(--sidebar-gradient)' }}
      >
        <div className="h-16 flex items-center px-6 border-b border-[var(--border-soft)]">
          <div className="flex items-center gap-3">
            <img src={logo} alt="Prism" className="w-8 h-8"/>
            {isSidebarOpen && (
              <span className="font-bold text-xl tracking-tight" style={{ color: 'var(--text-primary)' }}>
                棱镜
              </span>
            )}
          </div>
        </div>

        <nav className="flex-1 py-6 px-4 space-y-1.5 overflow-y-auto no-scrollbar">
          {filteredRoutes.map((route) => (
            <NavLink
              key={route.path}
              to={route.path}
              className={({ isActive }) => `
                flex items-center gap-3 px-3 py-2.5 rounded-xl transition-all duration-200
                ${isActive
                  ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm font-medium'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--surface-card)] hover:text-[var(--text-primary)]'}
              `}
              style={({ isActive }) => isActive ? { boxShadow: `0 2px 12px var(--glow-color)` } : {}}
            >
              <span className="flex-shrink-0">{route.icon}</span>
              {isSidebarOpen && <span>{route.name}</span>}
            </NavLink>
          ))}
        </nav>

        <div className="p-4 border-t border-[var(--border-soft)]">
          <button
            onClick={() => setSidebarOpen(!isSidebarOpen)}
            className="w-full flex items-center justify-center p-2 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-card)] transition-colors"
          >
            {isSidebarOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Top Header */}
        <header className="h-16 bg-[var(--surface-card)] backdrop-blur-md border-b border-[var(--border-soft)] flex items-center justify-between px-8 z-20">
          <div className="flex items-center text-sm text-[var(--text-secondary)]">
            <span className="hover:text-[var(--primary)] cursor-pointer">管理后台</span>
            <ChevronRight size={14} className="mx-2" />
            <span className="font-medium text-[var(--text-primary)]">
              {getBreadcrumbName()}
            </span>
          </div>

          <div className="flex items-center gap-6">
            <ThemeSwitch />

            <div className="h-8 w-px bg-[var(--border-soft)]"></div>

            <div className="text-right">
              <div className="text-xs text-[var(--text-secondary)] uppercase font-semibold">余额</div>
              <div className="text-sm font-bold text-[var(--primary)]">¥{user.balance.toFixed(2)}</div>
            </div>

            <div className="h-8 w-px bg-[var(--border-soft)]"></div>

            <div className="flex items-center gap-3">
              <div className="flex flex-col text-right">
                <span className="text-sm font-semibold text-[var(--text-primary)] leading-tight">{user.username}</span>
                <span className="text-xs text-[var(--text-secondary)]">{user.role === UserRole.ADMIN ? '管理员' : '普通用户'}</span>
              </div>
              <button
                onClick={onLogout}
                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg transition-all"
                title="登出"
              >
                <LogOut size={20} />
              </button>
            </div>
          </div>
        </header>

        <main className="flex-1 overflow-y-auto p-8">
          <div className="max-w-7xl mx-auto page-enter">
            {children}
          </div>
        </main>
      </div>
    </div>
  );
};

export default Layout;
