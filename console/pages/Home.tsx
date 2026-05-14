import React from 'react';
import {
    Zap,
    Shield,
    Layers,
    RefreshCw,
    Code2,
    Globe,
    ArrowRight,
    CheckCircle2,
    Coins
} from 'lucide-react';
import logo from '@/assets/logo.png';

interface HomeProps {
    onLogin: () => void;
    onPricing: () => void;
}

const Home: React.FC<HomeProps> = ({onLogin, onPricing}) => {
    const features = [
        {
            icon: <Layers className="w-6 h-6"/>,
            title: '统一能力网关',
            description: '将多个 AI 服务商的能力统一封装，提供标准化的 API 接口，简化接入流程'
        },
        {
            icon: <RefreshCw className="w-6 h-6"/>,
            title: '智能负载均衡',
            description: '自动在多个渠道账号间分配请求，支持权重配置和故障自动切换'
        },
        {
            icon: <Shield className="w-6 h-6"/>,
            title: '安全可控',
            description: '完善的用户认证和 Token 管理机制，保障 API 调用安全'
        },
        {
            icon: <Zap className="w-6 h-6"/>,
            title: '异步任务处理',
            description: '支持长时间运行的 AI 任务，通过回调或轮询获取结果'
        },
        {
            icon: <Code2 className="w-6 h-6"/>,
            title: '灵活的参数映射',
            description: '自定义请求参数到供应商 API 的映射规则，适配不同场景需求'
        },
        {
            icon: <Globe className="w-6 h-6"/>,
            title: '多渠道支持',
            description: '支持主流 AI 服务商，包括图像生成、视频生成等多种能力'
        }
    ];

    const capabilities = [
        '图像生成 (Text to Image)',
        '视频生成 (Text to Video)',
        '图像转视频 (Image to Video)',
        '更多能力持续扩展中...'
    ];

    return (
        <div className="min-h-screen bg-gradient-to-b from-[var(--surface)] to-[var(--surface-card)]">
            {/* Header */}
            <header className="fixed top-0 left-0 right-0 bg-[var(--surface-card)]/80 backdrop-blur-md z-50 border-b border-[var(--border-soft)]">
                <div className="max-w-7xl mx-auto px-6 py-4 flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <img src={logo} alt="Prism" className="w-10 h-10"/>
                        <span className="text-xl font-bold text-[var(--text-primary)]">棱镜</span>
                        <span className="text-sm text-[var(--text-secondary)] hidden sm:inline">Prism AI Gateway</span>
                    </div>
                    <div className="flex items-center gap-4">
                        <button
                            onClick={onPricing}
                            className="px-4 py-2 text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors flex items-center gap-2"
                        >
                            <Coins className="w-4 h-4"/>
                            价格
                        </button>
                        <button
                            onClick={onLogin}
                            className="px-5 py-2 bg-[var(--primary)] text-white rounded-lg font-medium hover:opacity-90 transition-colors flex items-center gap-2"
                        >
                            登录
                            <ArrowRight className="w-4 h-4"/>
                        </button>
                    </div>
                </div>
            </header>

            {/* Hero Section */}
            <section className="pt-32 pb-20 px-6">
                <div className="max-w-4xl mx-auto text-center">
                    <div
                        className="inline-flex items-center gap-2 px-4 py-2 bg-[var(--primary-lighter)] text-[var(--primary)] rounded-full text-sm font-medium mb-6">
                        <Zap className="w-4 h-4"/>
                        AI 能力聚合网关
                    </div>
                    <h1 className="text-4xl sm:text-5xl lg:text-6xl font-bold text-[var(--text-primary)] mb-6 leading-tight">
                        统一接入，<span className="text-[var(--primary)]">无限可能</span>
                    </h1>
                    <p className="text-lg sm:text-xl text-[var(--text-secondary)] mb-10 max-w-2xl mx-auto leading-relaxed">
                        棱镜是一个轻量级的 AI 能力聚合网关，将多个 AI 服务商的能力统一封装，
                        提供标准化的 API 接口，让 AI 能力接入变得简单高效。
                    </p>
                    <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
                        <button
                            onClick={onLogin}
                            className="px-8 py-3 bg-[var(--primary)] text-white rounded-xl font-bold hover:opacity-90 transition-all shadow-lg shadow-[var(--glow-color)] flex items-center gap-2"
                        >
                            开始使用
                            <ArrowRight className="w-5 h-5"/>
                        </button>
                        <button
                            onClick={onPricing}
                            className="px-8 py-3 bg-[var(--surface-card)] text-[var(--text-primary)] rounded-xl font-bold hover:bg-[var(--surface)] transition-all border border-[var(--border-soft)] flex items-center gap-2"
                        >
                            <Coins className="w-5 h-5"/>
                            查看价格
                        </button>
                    </div>
                </div>
            </section>

            {/* Features Section */}
            <section className="py-20 px-6 bg-[var(--surface)]">
                <div className="max-w-6xl mx-auto">
                    <div className="text-center mb-16">
                        <h2 className="text-3xl sm:text-4xl font-bold text-[var(--text-primary)] mb-4">核心特性</h2>
                        <p className="text-[var(--text-secondary)] max-w-2xl mx-auto">
                            专为 AI 能力聚合场景设计，提供完整的网关解决方案
                        </p>
                    </div>
                    <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6">
                        {features.map((feature, index) => (
                            <div
                                key={index}
                                className="bg-[var(--surface-card)] rounded-2xl p-6 shadow-sm border border-[var(--border-soft)] hover:shadow-md hover:border-[var(--primary-light)] transition-all"
                            >
                                <div
                                    className="w-12 h-12 bg-[var(--primary-lighter)] text-[var(--primary)] rounded-xl flex items-center justify-center mb-4">
                                    {feature.icon}
                                </div>
                                <h3 className="text-lg font-bold text-[var(--text-primary)] mb-2">{feature.title}</h3>
                                <p className="text-[var(--text-secondary)] text-sm leading-relaxed">{feature.description}</p>
                            </div>
                        ))}
                    </div>
                </div>
            </section>

            {/* Capabilities Section */}
            <section className="py-20 px-6">
                <div className="max-w-6xl mx-auto">
                    <div className="grid lg:grid-cols-2 gap-12 items-center">
                        <div>
                            <h2 className="text-3xl sm:text-4xl font-bold text-[var(--text-primary)] mb-6">支持的能力</h2>
                            <p className="text-[var(--text-secondary)] mb-8 leading-relaxed">
                                棱镜支持多种 AI 能力的统一接入，通过标准化的 API
                                接口调用不同服务商的能力，无需关心底层实现细节。
                            </p>
                            <ul className="space-y-4">
                                {capabilities.map((cap, index) => (
                                    <li key={index} className="flex items-center gap-3">
                                        <CheckCircle2 className="w-5 h-5 text-green-500 flex-shrink-0"/>
                                        <span className="text-[var(--text-primary)]">{cap}</span>
                                    </li>
                                ))}
                            </ul>
                        </div>
                        <div className="bg-gray-900 rounded-2xl p-6 shadow-xl">
                            <div className="flex items-center gap-2 mb-4">
                                <div className="w-3 h-3 rounded-full bg-red-500"></div>
                                <div className="w-3 h-3 rounded-full bg-yellow-500"></div>
                                <div className="w-3 h-3 rounded-full bg-green-500"></div>
                                <span className="ml-2 text-[var(--text-secondary)] text-sm">API 调用示例</span>
                            </div>
                            <pre className="text-sm text-gray-300 overflow-x-auto">
                <code>{`curl -X POST \\
  http://your-host/v1/capabilities/text2img \\
  -H "Authorization: Bearer YOUR_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{
    "prompt": "a beautiful sunset",
    "width": 1024,
    "height": 1024
  }'`}</code>
              </pre>
                        </div>
                    </div>
                </div>
            </section>

            {/* Architecture Section */}
            <section className="py-20 px-6 bg-[var(--surface)]">
                <div className="max-w-6xl mx-auto text-center">
                    <h2 className="text-3xl sm:text-4xl font-bold text-[var(--text-primary)] mb-4">技术架构</h2>
                    <p className="text-[var(--text-secondary)] max-w-2xl mx-auto mb-12">
                        基于 Go + React 构建，轻量高效，易于部署和扩展
                    </p>
                    <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-6">
                        {[
                            {name: 'Go', desc: '后端服务'},
                            {name: 'Gin', desc: 'Web 框架'},
                            {name: 'React', desc: '前端界面'},
                            {name: 'SQLite/MySQL', desc: '数据存储'}
                        ].map((tech, index) => (
                            <div key={index} className="bg-[var(--surface-card)] rounded-xl p-6 shadow-sm border border-[var(--border-soft)]">
                                <div className="text-2xl font-bold text-[var(--primary)] mb-1">{tech.name}</div>
                                <div className="text-[var(--text-secondary)] text-sm">{tech.desc}</div>
                            </div>
                        ))}
                    </div>
                </div>
            </section>

            {/* CTA Section */}
            <section className="py-20 px-6">
                <div className="max-w-4xl mx-auto text-center">
                    <h2 className="text-3xl sm:text-4xl font-bold text-[var(--text-primary)] mb-6">
                        开始使用棱镜
                    </h2>
                    <p className="text-[var(--text-secondary)] mb-10 max-w-xl mx-auto">
                        立即登录体验，或查看文档了解更多功能
                    </p>
                    <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
                        <button
                            onClick={onLogin}
                            className="px-8 py-3 bg-[var(--primary)] text-white rounded-xl font-bold hover:opacity-90 transition-all shadow-lg shadow-[var(--glow-color)] flex items-center gap-2"
                        >
                            登录 / 注册
                            <ArrowRight className="w-5 h-5"/>
                        </button>
                    </div>
                </div>
            </section>

            {/* Footer */}
            <footer className="py-8 px-6 border-t border-[var(--border-soft)]">
                <div className="max-w-6xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-4">
                    <div className="flex items-center gap-3">
                        <div
                            className="w-8 h-8 bg-[var(--primary)] rounded-lg flex items-center justify-center text-white font-bold">
                            <img src={logo} alt="Prism" className="w-8 h-8"/>
                        </div>
                        <span className="text-[var(--text-secondary)]">棱镜 Prism</span>
                    </div>
                    <div className="text-[var(--text-secondary)] text-sm">
                        v1.0.0 - AI Gateway
                    </div>
                </div>
            </footer>
        </div>
    );
};

export default Home;
