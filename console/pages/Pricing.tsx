import React, {useState, useEffect} from 'react';
import {Coins, Zap, ChevronDown, ChevronUp, ArrowLeft} from 'lucide-react';

interface PricingChannelModel {
    channel_code: string;
    model: string;
    name: string;
    price: number;
    price_unit: string;
}

interface PricingCapability {
    code: string;
    name: string;
    description: string;
    channels: PricingChannelModel[];
}

interface PricingProps {
    onBack: () => void;
}

const Pricing: React.FC<PricingProps> = ({onBack}) => {
    const [capabilities, setCapabilities] = useState<PricingCapability[]>([]);
    const [loading, setLoading] = useState(true);
    const [expandedCaps, setExpandedCaps] = useState<Set<string>>(new Set());

    useEffect(() => {
        fetchPricing();
    }, []);

    const fetchPricing = async () => {
        try {
            const response = await fetch('/api/public/pricing');
            const data = await response.json();
            if (data.data) {
                setCapabilities(data.data);
                // 默认展开所有能力
                setExpandedCaps(new Set(data.data.map((c: PricingCapability) => c.code)));
            }
        } catch (error) {
            console.error('Failed to fetch pricing:', error);
        } finally {
            setLoading(false);
        }
    };

    const toggleExpand = (code: string) => {
        setExpandedCaps(prev => {
            const next = new Set(prev);
            if (next.has(code)) {
                next.delete(code);
            } else {
                next.add(code);
            }
            return next;
        });
    };

    const formatPrice = (price: number, unit: string) => {
        if (price === 0) return '免费';
        const unitText = unit === 'request' ? '次' : unit;
        return `¥${price.toFixed(4)} / ${unitText}`;
    };

    if (loading) {
        return (
            <div className="min-h-screen bg-[var(--surface)] flex items-center justify-center">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-indigo-600"></div>
            </div>
        );
    }

    return (
        <div className="min-h-screen bg-gradient-to-b from-[var(--surface)] to-[var(--surface-card)]">
            {/* Header */}
            <header className="fixed top-0 left-0 right-0 bg-[var(--surface-card)]/80 backdrop-blur-md z-50 border-b border-[var(--border-soft)]">
                <div className="max-w-7xl mx-auto px-6 py-4 flex items-center justify-between">
                    <div className="flex items-center gap-3">
                        <div
                            className="w-10 h-10 bg-[var(--primary)] rounded-xl flex items-center justify-center text-white text-xl font-bold shadow-lg">
                            P
                        </div>
                        <span className="text-xl font-bold text-[var(--text-primary)]">棱镜</span>
                        <span className="text-sm text-[var(--text-secondary)] hidden sm:inline">价格列表</span>
                    </div>
                    <button
                        onClick={onBack}
                        className="flex items-center gap-2 px-4 py-2 text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
                    >
                        <ArrowLeft className="w-4 h-4"/>
                        返回首页
                    </button>
                </div>
            </header>

            {/* Content */}
            <main className="pt-24 pb-20 px-6">
                <div className="max-w-4xl mx-auto">
                    {/* Title */}
                    <div className="text-center mb-12">
                        <div
                            className="inline-flex items-center gap-2 px-4 py-2 bg-[var(--primary-lighter)] text-[var(--primary)] rounded-full text-sm font-medium mb-6">
                            <Coins className="w-4 h-4"/>
                            透明定价
                        </div>
                        <h1 className="text-3xl sm:text-4xl font-bold text-[var(--text-primary)] mb-4">
                            能力价格列表
                        </h1>
                        <p className="text-[var(--text-secondary)] max-w-2xl mx-auto">
                            以下是各能力的调用价格，价格按次计费，实际扣费以调用时配置为准
                        </p>
                    </div>

                    {/* Capabilities List */}
                    <div className="space-y-4">
                        {capabilities.length === 0 ? (
                            <div className="text-center py-20 text-[var(--text-secondary)]">
                                暂无可用能力
                            </div>
                        ) : (
                            capabilities.map(cap => (
                                <div
                                    key={cap.code}
                                    className="bg-[var(--surface-card)] rounded-2xl border border-[var(--border-soft)] shadow-sm overflow-hidden"
                                >
                                    {/* Capability Header */}
                                    <button
                                        onClick={() => toggleExpand(cap.code)}
                                        className="w-full px-6 py-5 flex items-center justify-between hover:bg-[var(--surface)] transition-colors"
                                    >
                                        <div className="flex items-center gap-4">
                                            <div
                                                className="w-12 h-12 bg-[var(--primary-lighter)] text-[var(--primary)] rounded-xl flex items-center justify-center">
                                                <Zap className="w-6 h-6"/>
                                            </div>
                                            <div className="text-left">
                                                <h3 className="text-lg font-bold text-[var(--text-primary)]">{cap.name}</h3>
                                                <p className="text-sm text-[var(--text-secondary)]">{cap.code}</p>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-3">
                                            <span className="text-sm text-[var(--text-secondary)]">
                                                {cap.channels.length} 个渠道
                                            </span>
                                            {expandedCaps.has(cap.code) ? (
                                                <ChevronUp className="w-5 h-5 text-[var(--text-secondary)]"/>
                                            ) : (
                                                <ChevronDown className="w-5 h-5 text-[var(--text-secondary)]"/>
                                            )}
                                        </div>
                                    </button>

                                    {/* Channels Table */}
                                    {expandedCaps.has(cap.code) && (
                                        <div className="border-t border-[var(--border-soft)]">
                                            {cap.description && (
                                                <div className="px-6 py-3 bg-[var(--surface)] text-sm text-[var(--text-secondary)]">
                                                    {cap.description}
                                                </div>
                                            )}
                                            {cap.channels.length === 0 ? (
                                                <div className="px-6 py-8 text-center text-[var(--text-secondary)] text-sm">
                                                    暂无可用渠道
                                                </div>
                                            ) : (
                                                <table className="w-full">
                                                    <thead>
                                                    <tr className="border-b border-[var(--border-soft)] bg-[var(--surface)]/50">
                                                        <th className="px-6 py-3 text-left text-sm font-semibold text-[var(--text-secondary)]">渠道</th>
                                                        <th className="px-6 py-3 text-left text-sm font-semibold text-[var(--text-secondary)]">模型</th>
                                                        <th className="px-6 py-3 text-left text-sm font-semibold text-[var(--text-secondary)]">名称</th>
                                                        <th className="px-6 py-3 text-right text-sm font-semibold text-[var(--text-secondary)]">价格</th>
                                                    </tr>
                                                    </thead>
                                                    <tbody>
                                                    {cap.channels.map((ch, idx) => (
                                                        <tr
                                                            key={`${ch.channel_code}-${ch.model}-${idx}`}
                                                            className="border-b border-gray-50 last:border-0 hover:bg-[var(--surface)]/50"
                                                        >
                                                            <td className="px-6 py-4">
                                                                <code
                                                                    className="px-2 py-1 bg-[var(--primary-lighter)] text-[var(--text-primary)] rounded text-sm">
                                                                    {ch.channel_code}
                                                                </code>
                                                            </td>
                                                            <td className="px-6 py-4 text-[var(--text-primary)]">
                                                                {ch.model || '-'}
                                                            </td>
                                                            <td className="px-6 py-4 text-[var(--text-primary)]">
                                                                {ch.name || '-'}
                                                            </td>
                                                            <td className="px-6 py-4 text-right">
                                                                    <span
                                                                        className={`font-medium ${ch.price === 0 ? 'text-green-600' : 'text-[var(--primary)]'}`}>
                                                                        {formatPrice(ch.price, ch.price_unit)}
                                                                    </span>
                                                            </td>
                                                        </tr>
                                                    ))}
                                                    </tbody>
                                                </table>
                                            )}
                                        </div>
                                    )}
                                </div>
                            ))
                        )}
                    </div>

                    {/* Note */}
                    <div className="mt-12 p-6 bg-amber-50 rounded-2xl border border-amber-100">
                        <h4 className="font-semibold text-amber-800 mb-2">说明</h4>
                        <ul className="text-sm text-amber-700 space-y-1">
                            <li>- 价格以人民币计价，按次调用扣费</li>
                            <li>- 调用失败不扣费，任务取消会退款</li>
                            <li>- 实际价格以调用时的配置为准</li>
                        </ul>
                    </div>
                </div>
            </main>

            {/* Footer */}
            <footer className="py-8 px-6 border-t border-[var(--border-soft)]">
                <div className="max-w-6xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-4">
                    <div className="flex items-center gap-3">
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

export default Pricing;
