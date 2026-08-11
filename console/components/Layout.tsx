import React, { useState, useEffect, useCallback, useRef } from 'react';
import { NavLink, useLocation, useNavigate } from 'react-router-dom';
import { Menu, X, LogOut, ChevronRight, ChevronDown, Lock } from 'lucide-react';
import { ROUTE_GROUPS, ALL_ROUTES, RouteGroup, RouteItem } from '../constants';
import { User, UserRole } from '../types';
import { ThemeSwitch } from '../theme/ThemeSwitch';
import logo from '@/assets/logo.svg';

interface LayoutProps {
  children: React.ReactNode;
  user: User | null;
  onLogout: () => void;
}

const STORAGE_KEY = 'prism_sidebar_groups';

const loadExpandedGroups = (): Set<string> => {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) return new Set(JSON.parse(raw));
  } catch {}
  return new Set(ROUTE_GROUPS.map(g => g.key));
};

const saveExpandedGroups = (groups: Set<string>) => {
  localStorage.setItem(STORAGE_KEY, JSON.stringify([...groups]));
};

const Layout: React.FC<LayoutProps> = ({ children, user, onLogout }) => {
  const [isSidebarOpen, setSidebarOpen] = useState(true);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(loadExpandedGroups);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const userMenuRef = useRef<HTMLDivElement>(null);
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => { setMobileMenuOpen(false); }, [location]);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  useEffect(() => {
    const currentPath = location.pathname;
    for (const group of ROUTE_GROUPS) {
      if (group.children.some(r => r.path === currentPath)) {
        setExpandedGroups(prev => {
          if (prev.has(group.key)) return prev;
          const next = new Set(prev);
          next.add(group.key);
          saveExpandedGroups(next);
          return next;
        });
        break;
      }
    }
  }, [location.pathname]);

  const toggleGroup = useCallback((key: string) => {
    setExpandedGroups(prev => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      saveExpandedGroups(next);
      return next;
    });
  }, []);

  if (!user) return <>{children}</>;

  const userRole = user.role;
  const filteredGroups = ROUTE_GROUPS
    .filter(g => g.roles.includes(userRole))
    .map(g => ({ ...g, children: g.children.filter(r => r.roles.includes(userRole)) }))
    .filter(g => g.children.length > 0);

  const getBreadcrumbName = () => {
    const currentPath = location.pathname;
    const route = ALL_ROUTES
      .filter(r => currentPath === r.path || currentPath.startsWith(`${r.path}/`))
      .sort((a, b) => b.path.length - a.path.length)[0];
    return route ? route.name : '控制台';
  };

  const showLabels = isSidebarOpen || mobileMenuOpen;

  const renderNavItem = (route: RouteItem) => (
    <NavLink
      key={route.path}
      to={route.path}
      className={({ isActive }) => `
        flex items-center gap-3 px-3 py-2 rounded-xl transition-all duration-200
        ${isActive
          ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm font-medium'
          : 'text-[var(--text-secondary)] hover:bg-[var(--surface-card)] hover:text-[var(--text-primary)]'}
      `}
      style={({ isActive }) => isActive ? { boxShadow: '0 2px 12px var(--glow-color)' } : {}}
    >
      <span className="flex-shrink-0">{route.icon}</span>
      {showLabels && <span className="text-sm">{route.name}</span>}
    </NavLink>
  );

  const renderGroup = (group: RouteGroup & { children: RouteItem[] }) => {
    const isExpanded = expandedGroups.has(group.key);
    const isSingleItem = group.children.length === 1;

    if (isSingleItem) {
      return <div key={group.key} className="mb-0.5">{renderNavItem(group.children[0])}</div>;
    }

    if (!showLabels) {
      return (
        <div key={group.key} className="mb-1 space-y-0.5">
          {group.children.map(route => (
            <NavLink
              key={route.path}
              to={route.path}
              className={({ isActive }) => `
                flex items-center justify-center p-2.5 rounded-xl transition-all duration-200
                ${isActive
                  ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm'
                  : 'text-[var(--text-secondary)] hover:bg-[var(--surface-card)] hover:text-[var(--text-primary)]'}
              `}
              style={({ isActive }) => isActive ? { boxShadow: '0 2px 12px var(--glow-color)' } : {}}
              title={route.name}
            >
              {route.icon}
            </NavLink>
          ))}
        </div>
      );
    }

    return (
      <div key={group.key} className="mb-1">
        <button
          onClick={() => toggleGroup(group.key)}
          className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-xs font-bold uppercase tracking-wider text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--surface-card)]/50 transition-colors"
        >
          <span className="flex-shrink-0 opacity-60">{group.icon}</span>
          <span className="flex-1 text-left">{group.label}</span>
          <ChevronDown size={14} className={`transition-transform duration-200 ${isExpanded ? '' : '-rotate-90'}`} />
        </button>
        {isExpanded && (
          <div className="mt-0.5 ml-2 space-y-0.5">
            {group.children.map(renderNavItem)}
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="flex overflow-hidden" style={{ backgroundColor: 'var(--surface)', height: '100dvh' }}>
      {mobileMenuOpen && (
        <div className="fixed inset-0 z-40 bg-black/30 md:hidden" onClick={() => setMobileMenuOpen(false)} />
      )}

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
            {showLabels && (
              <span className="font-bold text-xl tracking-tight" style={{ color: 'var(--text-primary)' }}>
                棱镜
              </span>
            )}
          </div>
          <button onClick={() => setMobileMenuOpen(false)} className="ml-auto p-1.5 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-card)] md:hidden">
            <X size={20} />
          </button>
        </div>

        <nav className="flex-1 py-4 md:py-5 px-3 md:px-4 space-y-0.5 overflow-y-auto no-scrollbar">
          {filteredGroups.map(renderGroup)}
        </nav>

        <div className="border-t border-[var(--border-soft)] p-3">
          <button
            onClick={() => setSidebarOpen(!isSidebarOpen)}
            className="w-full hidden md:flex items-center justify-center p-2 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-card)] transition-colors"
          >
            {isSidebarOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>
      </aside>

      <div className="flex-1 flex flex-col min-h-0 min-w-0">
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
            <div className="relative" ref={userMenuRef}>
              <button
                onClick={() => setUserMenuOpen(!userMenuOpen)}
                className="flex items-center gap-2 md:gap-3 px-3 py-2 rounded-xl hover:bg-[var(--surface)] active:scale-95 transition-all duration-150 cursor-pointer select-none"
              >
                <div className="w-8 h-8 rounded-full bg-gradient-to-br from-[var(--primary)] to-[var(--primary-light)] flex items-center justify-center text-white text-sm font-bold shadow-sm">
                  {user.username.charAt(0).toUpperCase()}
                </div>
                <div className="flex-col text-right hidden md:flex">
                  <span className="text-sm font-semibold text-[var(--text-primary)] leading-tight">{user.username}</span>
                  <span className="text-xs text-[var(--text-secondary)]">{user.role === UserRole.ADMIN ? '管理员' : '普通用户'}</span>
                </div>
                <ChevronDown size={14} className={`hidden md:block text-[var(--text-secondary)] transition-transform duration-200 ${userMenuOpen ? 'rotate-180' : ''}`} />
              </button>
              {userMenuOpen && (
                <div className="absolute right-0 top-full mt-2 w-44 bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] shadow-lg py-1.5 z-50">
                  <button
                    onClick={() => { setUserMenuOpen(false); navigate('/change-password'); }}
                    className="w-full flex items-center gap-2.5 px-4 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--surface)] transition-colors"
                  >
                    <Lock size={15} />
                    修改密码
                  </button>
                  <div className="mx-3 my-1 border-t border-[var(--border-soft)]"></div>
                  <button
                    onClick={() => { setUserMenuOpen(false); onLogout(); }}
                    className="w-full flex items-center gap-2.5 px-4 py-2 text-sm text-red-500 hover:bg-red-50 transition-colors"
                  >
                    <LogOut size={15} />
                    退出登录
                  </button>
                </div>
              )}
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
