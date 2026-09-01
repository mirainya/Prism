import React, { useEffect, useRef, useState } from 'react';
import { NavLink, useLocation, useNavigate } from 'react-router-dom';
import {
  ChevronDown,
  ChevronRight,
  Lock,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  X,
} from 'lucide-react';
import { ALL_ROUTES, ROUTE_GROUPS, type RouteGroup, type RouteItem } from '../constants';
import { type User, UserRole } from '../types';
import { ThemeSwitch } from '../theme/ThemeSwitch';
import logo from '@/assets/logo.svg';

interface LayoutProps {
  children: React.ReactNode;
  user: User | null;
  onLogout: () => void;
}

const STORAGE_KEY = 'prism_sidebar_groups';
const SIDEBAR_STORAGE_KEY = 'prism_sidebar_open';

const loadExpandedGroups = (): Set<string> => {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) return new Set(JSON.parse(raw));
  } catch {}
  return new Set(ROUTE_GROUPS.map(group => group.key));
};

const saveExpandedGroups = (groups: Set<string>) => {
  localStorage.setItem(STORAGE_KEY, JSON.stringify([...groups]));
};

const loadSidebarState = () => {
  try {
    return localStorage.getItem(SIDEBAR_STORAGE_KEY) === 'expanded';
  } catch {}
  return false;
};

const saveSidebarState = (open: boolean) => {
  try {
    localStorage.setItem(SIDEBAR_STORAGE_KEY, open ? 'expanded' : 'collapsed');
  } catch {}
};

const Layout: React.FC<LayoutProps> = ({ children, user, onLogout }) => {
  const [isSidebarOpen, setSidebarOpen] = useState(loadSidebarState);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(loadExpandedGroups);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [hoveredDrawerGroup, setHoveredDrawerGroup] = useState<string | null>(null);
  const userMenuRef = useRef<HTMLDivElement>(null);
  const drawerCloseTimerRef = useRef<number | null>(null);
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    setMobileMenuOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 768px)');
    const closeMobileMenu = () => {
      if (desktop.matches) setMobileMenuOpen(false);
    };
    closeMobileMenu();
    desktop.addEventListener('change', closeMobileMenu);
    return () => desktop.removeEventListener('change', closeMobileMenu);
  }, []);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(event.target as Node)) {
        setUserMenuOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  useEffect(() => () => {
    if (drawerCloseTimerRef.current !== null) window.clearTimeout(drawerCloseTimerRef.current);
  }, []);

  useEffect(() => {
    const group = ROUTE_GROUPS.find(item => item.children.some(route => route.path === location.pathname));
    if (!group) return;
    setExpandedGroups(previous => {
      if (previous.has(group.key)) return previous;
      const next = new Set(previous);
      next.add(group.key);
      saveExpandedGroups(next);
      return next;
    });
  }, [location.pathname]);

  if (!user) return <>{children}</>;

  const filteredGroups = ROUTE_GROUPS
    .filter(group => group.roles.includes(user.role))
    .map(group => ({
      ...group,
      children: group.children.filter(route => route.roles.includes(user.role)),
    }))
    .filter(group => group.children.length > 0);

  const currentRoute = ALL_ROUTES
    .filter(route => location.pathname === route.path || location.pathname.startsWith(`${route.path}/`))
    .sort((left, right) => right.path.length - left.path.length)[0];
  const currentGroup = filteredGroups.find(group => group.children.some(route => route.path === currentRoute?.path));
  const showLabels = isSidebarOpen || mobileMenuOpen;

  const toggleGroup = (key: string) => {
    setExpandedGroups(previous => {
      const next = new Set(previous);
      next.has(key) ? next.delete(key) : next.add(key);
      saveExpandedGroups(next);
      return next;
    });
  };

  const toggleSidebar = () => {
    setSidebarOpen(previous => {
      const next = !previous;
      saveSidebarState(next);
      return next;
    });
  };

  const renderNavItem = (route: RouteItem, compact = false, onNavigate?: () => void) => (
    <NavLink
      key={route.path}
      to={route.path}
      title={compact ? route.name : undefined}
      aria-label={compact ? route.name : undefined}
      onClick={onNavigate}
      className={({ isActive }) => [
        'group flex min-h-10 items-center transition duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus-ring)]',
        compact ? 'sidebar-rail-item' : 'gap-3 rounded-xl px-3 py-2.5',
        isActive
          ? compact
            ? 'sidebar-rail-active'
            : '[background:var(--brand-gradient)] font-semibold text-white shadow-[0_6px_16px_var(--glow-color)]'
          : 'text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]',
      ].join(' ')}
    >
      {({ isActive }) => (
        <>
          <span
            className={[
              'flex shrink-0 items-center justify-center transition duration-150',
              compact ? 'sidebar-rail-icon' : 'h-8 w-8 rounded-lg',
              compact && isActive ? 'sidebar-rail-icon-active' : '',
            ].join(' ')}
          >
            {route.icon}
          </span>
          <span className={compact ? 'sidebar-rail-label' : 'min-w-0 truncate text-sm'}>{route.name}</span>
        </>
      )}
    </NavLink>
  );

  const renderGroup = (group: RouteGroup & { children: RouteItem[] }, index: number) => {
    if (!showLabels) {
      const isActive = currentGroup?.key === group.key;
      const openDrawer = () => {
        if (drawerCloseTimerRef.current !== null) {
          window.clearTimeout(drawerCloseTimerRef.current);
          drawerCloseTimerRef.current = null;
        }
        setHoveredDrawerGroup(group.key);
      };
      const scheduleDrawerClose = () => {
        if (drawerCloseTimerRef.current !== null) window.clearTimeout(drawerCloseTimerRef.current);
        drawerCloseTimerRef.current = window.setTimeout(() => {
          setHoveredDrawerGroup(current => current === group.key ? null : current);
          drawerCloseTimerRef.current = null;
        }, 180);
      };
      return (
        <div
          key={group.key}
          aria-label={group.label}
          onMouseEnter={openDrawer}
          onMouseLeave={scheduleDrawerClose}
          className={`sidebar-rail-group ${hoveredDrawerGroup === group.key ? 'sidebar-rail-group-open' : ''} ${index > 0 ? 'sidebar-rail-group-separated' : ''}`}
        >
          <div
            aria-label={group.label}
            title={`${group.label}（悬停查看菜单）`}
            className={[
              'sidebar-rail-trigger group flex min-h-10 items-center justify-center',
              isActive ? 'sidebar-rail-trigger-active' : '',
            ].join(' ')}
          >
            <span className="sidebar-rail-trigger-icon">{group.icon}</span>
          </div>
          <div className="sidebar-rail-drawer" role="group" aria-label={`${group.label}菜单`}>
            <div className="sidebar-rail-drawer-head">
              <span className={isActive ? 'sidebar-rail-drawer-icon sidebar-rail-drawer-icon-active' : 'sidebar-rail-drawer-icon'}>
                {group.icon}
              </span>
              <span className="min-w-0 flex-1 truncate text-sm font-extrabold text-[var(--text-primary)]">{group.label}</span>
            </div>
            <div className="mt-1 space-y-1">
              {group.children.map(route => renderNavItem(route, false, () => setHoveredDrawerGroup(null)))}
            </div>
          </div>
        </div>
      );
    }

    const isExpanded = expandedGroups.has(group.key);
    const isActive = currentGroup?.key === group.key;
    return (
      <div key={group.key} className="mb-1">
        <button
          type="button"
          onClick={() => toggleGroup(group.key)}
          aria-expanded={isExpanded}
          className={[
            'flex w-full items-center gap-2 rounded-xl px-3 py-2 text-xs font-bold transition',
            isActive
              ? 'bg-[var(--surface-tint)] text-[var(--text-primary)]'
              : 'text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]',
          ].join(' ')}
        >
          <span className={[
            'flex h-7 w-7 shrink-0 items-center justify-center rounded-lg transition',
            isActive
              ? '[background:var(--brand-gradient)] text-white shadow-[0_5px_12px_var(--glow-color)]'
              : 'bg-[var(--surface-muted)] text-[var(--primary)] opacity-90',
          ].join(' ')}>
            {group.icon}
          </span>
          <span className="flex-1 text-left">{group.label}</span>
          <ChevronDown size={14} className={`transition-transform ${isExpanded ? '' : '-rotate-90'}`} />
        </button>
        {isExpanded && <div className="ml-2 mt-1 space-y-1">{group.children.map(route => renderNavItem(route))}</div>}
      </div>
    );
  };

  return (
    <div className="flex h-dvh min-w-0 flex-col gap-3 overflow-hidden [background:var(--app-background)] p-3 md:gap-4 md:p-4">
      <header className="glass-surface z-30 flex h-16 shrink-0 items-center justify-between gap-3 rounded-lg border border-[var(--border-soft)] [background:var(--shell-glass)] px-3 shadow-[var(--shadow-soft)] md:px-5">
        <div className="flex min-w-0 items-center gap-3">
          <button
            type="button"
            onClick={() => setMobileMenuOpen(true)}
            title="打开导航"
            aria-label="打开导航"
            className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] md:hidden"
          >
            <Menu size={20} />
          </button>

          <div className="flex shrink-0 items-center gap-2 border-r border-[var(--border-soft)] pr-3 md:pr-5">
            <span className="flex h-9 w-9 items-center justify-center rounded-lg [background:var(--brand-gradient)] shadow-[0_6px_16px_var(--glow-color)]">
              <img src={logo} alt="" className="h-6 w-6" />
            </span>
            <span className="hidden text-lg font-extrabold text-[var(--primary)] sm:inline">Prism</span>
          </div>

          <div className="flex min-w-0 items-center gap-2 text-sm">
            {currentGroup && <span className="hidden text-[var(--text-secondary)] lg:inline">{currentGroup.label}</span>}
            {currentGroup && <ChevronRight size={14} className="hidden shrink-0 text-[var(--text-tertiary)] lg:block" />}
            <span className="truncate font-bold text-[var(--text-primary)]">{currentRoute?.name || '控制台'}</span>
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-2 md:gap-3">
          <div className="hidden lg:block"><ThemeSwitch /></div>
          <div className="hidden h-7 w-px bg-[var(--border-soft)] lg:block" />
          <div className="hidden text-right sm:block">
            <div className="text-[10px] font-semibold text-[var(--text-secondary)]">余额</div>
            <div className="text-sm font-bold text-[var(--accent)]">¥{user.balance.toFixed(2)}</div>
          </div>

          <div className="relative" ref={userMenuRef}>
            <button
              type="button"
              onClick={() => setUserMenuOpen(open => !open)}
              className="flex h-10 items-center gap-2 rounded-lg px-1.5 text-left transition hover:bg-[var(--surface-muted)] md:px-2"
              aria-expanded={userMenuOpen}
            >
              <span className="flex h-8 w-8 items-center justify-center rounded-full [background:var(--brand-gradient)] text-sm font-bold text-white">
                {user.username.charAt(0).toUpperCase()}
              </span>
              <span className="hidden max-w-32 flex-col md:flex">
                <span className="truncate text-sm font-semibold text-[var(--text-primary)]">{user.username}</span>
                <span className="text-[10px] text-[var(--text-secondary)]">{user.role === UserRole.ADMIN ? '管理员' : '普通用户'}</span>
              </span>
              <ChevronDown size={14} className={`hidden text-[var(--text-secondary)] transition-transform md:block ${userMenuOpen ? 'rotate-180' : ''}`} />
            </button>

            {userMenuOpen && (
              <div className="glass-surface absolute right-0 top-full z-50 mt-2 w-44 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-elevated)] py-1.5 shadow-[var(--shadow-floating)]">
                <div className="px-3 py-2 lg:hidden"><ThemeSwitch /></div>
                <div className="mx-2 border-t border-[var(--border-soft)] lg:hidden" />
                <button
                  type="button"
                  onClick={() => { setUserMenuOpen(false); navigate('/change-password'); }}
                  className="flex w-full items-center gap-2.5 px-3 py-2 text-sm text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]"
                >
                  <Lock size={15} />修改密码
                </button>
                <div className="mx-2 border-t border-[var(--border-soft)]" />
                <button
                  type="button"
                  onClick={() => { setUserMenuOpen(false); onLogout(); }}
                  className="flex w-full items-center gap-2.5 px-3 py-2 text-sm text-[var(--candy-red)] hover:bg-red-50"
                >
                  <LogOut size={15} />退出登录
                </button>
              </div>
            )}
          </div>
        </div>
      </header>

      <div className="flex min-h-0 min-w-0 flex-1 gap-4">
        {mobileMenuOpen && (
          <button
            type="button"
            aria-label="关闭导航"
            className="fixed inset-0 z-40 bg-black/25 md:hidden"
            onClick={() => setMobileMenuOpen(false)}
          />
        )}

        <aside
          className={[
            'glass-surface fixed inset-y-0 left-0 z-50 flex w-64 flex-col border-r border-[var(--border-soft)] [background:var(--sidebar-gradient)] shadow-[var(--shadow-floating)] transition-transform md:static md:z-20 md:border md:transition-[width]',
            mobileMenuOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0',
            isSidebarOpen ? 'md:w-60 md:rounded-xl' : 'md:w-[68px] md:rounded-[24px]',
          ].join(' ')}
        >
          <div className="flex h-12 items-center justify-end border-b border-[var(--border-soft)] px-3 md:hidden">
            <button
              type="button"
              title="关闭导航"
              aria-label="关闭导航"
              onClick={() => setMobileMenuOpen(false)}
              className="ml-auto flex h-9 w-9 items-center justify-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-muted)]"
            >
              <X size={20} />
            </button>
          </div>

          <nav className={`no-scrollbar min-h-0 flex-1 ${showLabels ? 'overflow-y-auto space-y-1 p-3' : 'sidebar-collapsed-nav'}`}>
            {filteredGroups.map(renderGroup)}
          </nav>

          <div className="hidden border-t border-[var(--border-soft)] p-2 md:block">
            <button
              type="button"
              onClick={toggleSidebar}
              title={isSidebarOpen ? '收起导航' : '展开导航'}
              aria-label={isSidebarOpen ? '收起导航' : '展开导航'}
              className="flex h-10 w-full items-center justify-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]"
            >
              {isSidebarOpen ? <PanelLeftClose size={19} /> : <PanelLeftOpen size={19} />}
            </button>
          </div>
        </aside>

        <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
          <div className="page-enter mx-auto w-full max-w-[1800px] pb-4">{children}</div>
        </main>
      </div>
    </div>
  );
};

export default Layout;
