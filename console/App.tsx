import React, { useState, useEffect, Suspense, lazy } from 'react';
import { HashRouter as Router, Routes, Route, Navigate } from 'react-router-dom';
import { ThemeProvider } from './theme/ThemeProvider';
import Layout from './components/Layout';
import ErrorBoundary from './components/ErrorBoundary';
import { PageSkeleton } from './components/shell';
import Home from './pages/Home';
import Pricing from './pages/Pricing';
// 登录后的页面按路由懒加载,避免全部打进首屏主 bundle
const Dashboard = lazy(() => import('./pages/Dashboard'));
const Channels = lazy(() => import('./pages/Channels'));
const GatewayChannels = lazy(() => import('./pages/GatewayChannels'));
const Capabilities = lazy(() => import('./pages/Capabilities'));
const Users = lazy(() => import('./pages/Users'));
const Tokens = lazy(() => import('./pages/Tokens'));
const Logs = lazy(() => import('./pages/Logs'));
const CallLogs = lazy(() => import('./pages/CallLogs'));
const Observability = lazy(() => import('./pages/Observability'));
const RequestLogs = lazy(() => import('./pages/RequestLogs'));
const ApiDocs = lazy(() => import('./pages/ApiDocs'));
const ChatLogs = lazy(() => import('./pages/ChatLogs'));
const ChatModels = lazy(() => import('./pages/GatewayModels'));
const ChangePassword = lazy(() => import('./pages/ChangePassword'));
const Playground = lazy(() => import('./pages/Playground'));
const VideoChannels = lazy(() => import('./pages/VideoChannels'));
const VideoChannelEditor = lazy(() => import('./pages/VideoChannelEditor'));
const VideoTasks = lazy(() => import('./pages/VideoTasks'));
import { User, UserRole } from './types';
import { login, register, logout, getCurrentUser } from './services/api';
import {LogIn, UserPlus, ArrowLeft} from 'lucide-react';
import logo from '@/assets/logo.svg';

const App: React.FC = () => {
  const [user, setUser] = useState<User | null>(null);
  const [isAuthenticating, setIsAuthenticating] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
    const [showAuthForm, setShowAuthForm] = useState(false);
    const [showPricing, setShowPricing] = useState(false);
  const [authMode, setAuthMode] = useState<'login' | 'register'>('login');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [successMsg, setSuccessMsg] = useState('');

  useEffect(() => {
    const token = localStorage.getItem('prism_token');
    if (token) {
      getCurrentUser()
        .then(user => {
          setUser(user);
          localStorage.setItem('prism_user', JSON.stringify(user));
        })
        .catch(() => {
          localStorage.removeItem('prism_token');
          localStorage.removeItem('prism_user');
        })
        .finally(() => setIsLoading(false));
    } else {
      setIsLoading(false);
    }
  }, []);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setIsAuthenticating(true);
    try {
      const { user } = await login(username, password);
      setUser(user);
      localStorage.setItem('prism_user', JSON.stringify(user));
        setShowAuthForm(false);
        setUsername('');
        setPassword('');
    } catch (err: any) {
      setError(err.message || '登录失败');
    } finally {
      setIsAuthenticating(false);
    }
  };

  const handleRegister = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setSuccessMsg('');
    setIsAuthenticating(true);
    try {
      await register(username, password);
      setAuthMode('login');
      setError('');
      setSuccessMsg('注册成功，请登录');
    } catch (err: any) {
      setError(err.message || '注册失败');
    } finally {
      setIsAuthenticating(false);
    }
  };

  const handleLogout = () => {
    logout();
    setUser(null);
  };

    const handleShowLogin = () => {
        setShowAuthForm(true);
        setAuthMode('login');
        setError('');
        setSuccessMsg('');
    };

    const handleBackToHome = () => {
        setShowAuthForm(false);
        setShowPricing(false);
        setError('');
        setSuccessMsg('');
        setUsername('');
        setPassword('');
    };

    const handleShowPricing = () => {
        setShowPricing(true);
        setShowAuthForm(false);
    };

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center" style={{ background: 'var(--login-gradient)' }}>
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-[var(--primary)]"></div>
      </div>
    );
  }

    // 未登录：显示首页或登录表单或价格页面
  if (!user) {
      if (showPricing) {
          return <Pricing onBack={handleBackToHome}/>;
      }
      if (!showAuthForm) {
          return <Home onLogin={handleShowLogin} onPricing={handleShowPricing}/>;
      }

    return (
      <div className="min-h-screen relative overflow-hidden flex items-center justify-center p-6" style={{ background: 'var(--login-gradient)' }}>
        {/* 背景装饰 */}
        <div className="absolute inset-0 bg-mesh" />
        <div className="absolute w-[500px] h-[500px] rounded-full border-2 border-[var(--primary)]/10 -top-[150px] -right-[150px] animate-[login-spin_24s_linear_infinite]" />
        <div className="absolute w-[200px] h-[200px] rounded-full border border-dashed border-[var(--primary)]/15 bottom-[10%] left-[5%] animate-[login-spin-reverse_36s_linear_infinite]" />
        <div className="absolute w-[300px] h-[300px] rounded-full bg-[var(--primary)]/5 top-[30%] right-[10%] blur-[40px] animate-[glow-pulse_6s_ease-in-out_infinite]" />

        {/* 登录卡片 */}
        <div className="relative w-full max-w-md glass-card p-8">
            <button
                onClick={handleBackToHome}
                className="flex items-center gap-2 text-[var(--text-secondary)] hover:text-[var(--primary)] mb-6 text-sm transition-colors"
            >
                <ArrowLeft size={16}/>
                返回首页
            </button>

          <div className="flex flex-col items-center mb-8">
              <img src={logo} alt="Prism" className="w-16 h-16 mb-4"/>
              <h1 className="text-2xl font-bold bg-gradient-to-r from-[var(--primary)] to-[var(--accent)] bg-clip-text text-transparent">棱镜</h1>
            <p className="text-[var(--text-secondary)] mt-2">
              {authMode === 'login' ? '请登录您的账户' : '创建新账户'}
            </p>
          </div>

          {successMsg && (
            <div className="mb-4 p-3 bg-green-50 border border-green-200 text-green-600 rounded-xl text-sm">
              {successMsg}
            </div>
          )}

          {error && (
            <div className="mb-4 p-3 bg-red-50 border border-red-200 text-red-600 rounded-xl text-sm">
              {error}
            </div>
          )}

          <form onSubmit={authMode === 'login' ? handleLogin : handleRegister} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">用户名</label>
              <input
                type="text"
                value={username}
                onChange={e => setUsername(e.target.value)}
                className="w-full px-4 py-3 border border-[var(--border-soft)] rounded-xl bg-white/80 focus:outline-none focus:ring-2 focus:ring-[var(--primary)]/30 focus:border-[var(--primary)] transition-all"
                placeholder="请输入用户名"
                required
                minLength={3}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">密码</label>
              <input
                type="password"
                value={password}
                onChange={e => setPassword(e.target.value)}
                className="w-full px-4 py-3 border border-[var(--border-soft)] rounded-xl bg-white/80 focus:outline-none focus:ring-2 focus:ring-[var(--primary)]/30 focus:border-[var(--primary)] transition-all"
                placeholder="请输入密码"
                required
                minLength={6}
              />
            </div>
            <button
              type="submit"
              disabled={isAuthenticating}
              className="w-full py-3 bg-gradient-to-r from-[var(--primary)] to-[var(--primary-light)] text-white rounded-xl font-bold hover:shadow-lg hover:-translate-y-0.5 transition-all disabled:opacity-50 flex items-center justify-center gap-2"
            >
              {isAuthenticating ? (
                <div className="animate-spin rounded-full h-5 w-5 border-b-2 border-white"></div>
              ) : authMode === 'login' ? (
                <>
                  <LogIn size={18} />
                  登录
                </>
              ) : (
                <>
                  <UserPlus size={18} />
                  注册
                </>
              )}
            </button>
          </form>

          <div className="mt-6 text-center">
            <button
              onClick={() => {
                setAuthMode(authMode === 'login' ? 'register' : 'login');
                setError('');
                setSuccessMsg('');
              }}
              className="text-[var(--primary)] hover:text-[var(--accent)] text-sm font-medium transition-colors"
            >
              {authMode === 'login' ? '没有账户? 点击注册' : '已有账户? 点击登录'}
            </button>
          </div>

          <p className="mt-8 text-center text-xs text-[var(--text-secondary)]">
              棱镜 v1.0.0
          </p>
        </div>
      </div>
    );
  }

  const isAdmin = user.role === UserRole.ADMIN;

  return (
    <ThemeProvider>
    <Router>
      <Layout user={user} onLogout={handleLogout}>
        <ErrorBoundary>
        <Suspense fallback={<PageSkeleton />}>
        <Routes>
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<Dashboard />} />

          {isAdmin && (
            <>
              <Route path="/channels" element={<Channels />} />
              <Route path="/gateway-channels" element={<GatewayChannels />} />
              <Route path="/chat-models" element={<ChatModels />} />
              <Route path="/capabilities" element={<Capabilities />} />
              <Route path="/video-channels" element={<VideoChannels />} />
              <Route path="/video-channels/new" element={<VideoChannelEditor />} />
              <Route path="/video-channels/:id/edit" element={<VideoChannelEditor />} />
              <Route path="/video-tasks" element={<VideoTasks />} />
              <Route path="/users" element={<Users />} />
              <Route path="/request-logs" element={<RequestLogs />} />
            </>
          )}

          <Route path="/tokens" element={<Tokens />} />
            <Route path="/playground" element={<Playground/>}/>
            <Route path="/api-docs" element={<ApiDocs/>}/>
          <Route path="/logs" element={<Logs />} />
            <Route path="/calls" element={<CallLogs/>}/>
            <Route path="/observability" element={<Observability/>}/>
            <Route path="/chat-logs" element={<ChatLogs/>}/>
            <Route path="/change-password" element={<ChangePassword/>}/>

          <Route path="*" element={<div className="text-center py-20 text-gray-400 font-medium italic">页面正在开发中</div>} />
        </Routes>
        </Suspense>
        </ErrorBoundary>
      </Layout>
    </Router>
    </ThemeProvider>
  );
};

export default App;
