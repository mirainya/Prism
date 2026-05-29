import React, { useState, useEffect } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
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
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const location = useLocation();

  useEffect(() => { setMobileMenuOpen(false); }, [location]);

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
    <div className="flex overflow-hidden" style={{ backgroundColor: 'var(--surface)', height: '100dvh' }}>
      {/* Mobile overlay */}
      {mobileMenuOpen && (
        <div className="fixed inset-0 z-40 bg-black/30 md:hidden" onClick={() => setMobileMenuOpen(false)} />
      )}

      {/* Sidebar */}
      <aside
        className={`
          fixed inset-y-0 left-0 z-50 md:static md:z-30
          ${mobileMenuOpen ? 'translate-x-0' : '-translate-x-full'} md:translate-x-0
          ${isSidebarOpen ? 'w-64' : 'md:w-20 w-64'}
          flex flex-col transition-all duration-300 ease-in-out border-r border-[var(--border-soft)]
        `}
        style={{ background: 'var(--sidebar-gradient)' }}
      >
        <div className="h-14 md:h-16 flex items-center px-6 border-b border-[var(--border-soft)]">
          <div className="flex items-center gap-3">
            <img src={logo} alt="Prism" className="w-8 h-8"/>
            {(isSidebarOpen || mobileMenuOpen) && (
              <span className="font-bold text-xl tracking-tight" style={{ color: 'var(--text-primary)' }}>
                棱镜
              </span>
            )}
          </div>
          <button onClick={() => setMobileMenuOpen(false)} className="ml-auto p-1.5 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-card)] md:hidden">
            <X size={20} />
          </button>
        </div>

        <nav className="flex-1 py-4 md:py-6 px-3 md:px-4 space-y-1 overflow-y-auto no-scrollbar">
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
              {(isSidebarOpen || mobileMenuOpen) && <span>{route.name}</span>}
            </NavLink>
          ))}
        </nav>

        <div className="p-4 border-t border-[var(--border-soft)] hidden md:block">
          <button
            onClick={() => setSidebarOpen(!isSidebarOpen)}
            className="w-full flex items-center justify-center p-2 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-card)] transition-colors"
          >
            {isSidebarOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <div className="flex-1 flex flex-col min-h-0 min-w-0">
        {/* Top Header */}
        <header className="h-14 md:h-16 flex-shrink-0 bg-[var(--surface-card)] backdrop-blur-md border-b border-[var(--border-soft)] flex items-center justify-between px-4 md:px-8 z-20">
          <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
            <button onClick={() => setMobileMenuOpen(true)} className="p-1.5 rounded-lg hover:bg-[var(--surface)] md:hidden">
              <Menu size={20} />
            </button>
            <span className="hover:text-[var(--primary)] cursor-pointer hidden sm:inline">管理后台</span>
            <ChevronRight size={14} className="hidden sm:block" />
            <span className="font-medium text-[var(--text-primary)]">
              {getBreadcrumbName()}
            </span>
          </div>

          <div className="flex items-center gap-3 md:gap-6">
            <ThemeSwitch />

            <div className="h-8 w-px bg-[var(--border-soft)] hidden sm:block"></div>

            <div className="text-right hidden sm:block">
              <div className="text-xs text-[var(--text-secondary)] uppercase font-semibold">余额</div>
              <div className="text-sm font-bold text-[var(--primary)]">¥{user.balance.toFixed(2)}</div>
            </div>

            <div className="h-8 w-px bg-[var(--border-soft)] hidden md:block"></div>

            <div className="flex items-center gap-2 md:gap-3">
              <div className="flex flex-col text-right hidden md:flex">
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

        <main className="flex-1 min-h-0 overflow-y-auto p-4 md:p-8">
          <div className="max-w-7xl mx-auto page-enter">
            {children}
          </div>
        </main>
      </div>
    </div>
  );
};

export default Layout;
