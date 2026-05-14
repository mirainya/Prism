import React, { useEffect, useState } from 'react';
import { Zap, Search, ChevronDown, ChevronRight, Image, Video, MessageSquare, Box } from 'lucide-react';
import { fetchCapabilityPrices, CapabilityPrice } from '../services/api';

const TYPE_MAP: Record<string, { label: string; color: string; icon: React.ReactNode }> = {
    image: { label: '图像', color: 'bg-blue-100 text-blue-700', icon: <Image size={14} /> },
    video: { label: '视频', color: 'bg-purple-100 text-purple-700', icon: <Video size={14} /> },
    chat: { label: '对话', color: 'bg-green-100 text-green-700', icon: <MessageSquare size={14} /> },
    other: { label: '其他', color: 'bg-[var(--primary-lighter)] text-[var(--text-primary)]', icon: <Box size={14} /> },
};

const PRICE_UNIT_MAP: Record<string, string> = {
    request: '次',
    token: 'token',
    second: '秒',
    minute: '分钟',
};

const CapabilityCard: React.FC<{ capability: CapabilityPrice }> = ({ capability }) => {
    const [expanded, setExpanded] = useState(false);
    const typeInfo = TYPE_MAP[capability.type] || TYPE_MAP.other;

    return (
        <div className="bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] shadow-sm overflow-hidden">
            <div
                className="p-4 flex items-center justify-between cursor-pointer hover:bg-[var(--surface)]"
                onClick={() => setExpanded(!expanded)}
            >
                <div className="flex items-center gap-3">
                    <div className="w-10 h-10 bg-indigo-100 rounded-lg flex items-center justify-center">
                        <Zap size={20} className="text-[var(--primary)]" />
                    </div>
                    <div>
                        <div className="flex items-center gap-2">
                            <span className="font-medium text-[var(--text-primary)]">{capability.name}</span>
                            <code className="text-xs px-2 py-0.5 bg-[var(--primary-lighter)] rounded text-[var(--text-secondary)]">{capability.code}</code>
                            <span className={`text-xs px-2 py-0.5 rounded flex items-center gap-1 ${typeInfo.color}`}>
                                {typeInfo.icon}
                                {typeInfo.label}
                            </span>
                        </div>
                        {capability.description && (
                            <p className="text-sm text-[var(--text-secondary)] mt-1">{capability.description}</p>
                        )}
                    </div>
                </div>
                <div className="flex items-center gap-3">
                    <span className="text-sm text-[var(--text-secondary)]">{capability.prices.length} 个渠道</span>
                    {expanded ? (
                        <ChevronDown size={18} className="text-[var(--text-secondary)]" />
                    ) : (
                        <ChevronRight size={18} className="text-[var(--text-secondary)]" />
                    )}
                </div>
            </div>

            {expanded && capability.prices.length > 0 && (
                <div className="border-t border-[var(--border-soft)] bg-[var(--surface)] p-4">
                    <table className="w-full text-sm">
                        <thead>
                            <tr className="text-[var(--text-secondary)]">
                                <th className="text-left font-medium pb-2">渠道</th>
                                <th className="text-left font-medium pb-2">模型</th>
                                <th className="text-right font-medium pb-2">价格</th>
                            </tr>
                        </thead>
                        <tbody>
                            {capability.prices.map((price, idx) => (
                                <tr key={idx} className="border-t border-[var(--border-soft)]">
                                    <td className="py-2">
                                        <code className="text-[var(--primary)]">{price.channel}</code>
                                    </td>
                                    <td className="py-2 text-[var(--text-secondary)]">{price.model || '-'}</td>
                                    <td className="py-2 text-right">
                                        <span className="font-medium text-[var(--text-primary)]">¥{price.price.toFixed(4)}</span>
                                        <span className="text-[var(--text-secondary)] ml-1">/{PRICE_UNIT_MAP[price.price_unit] || price.price_unit}</span>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}

            {expanded && capability.prices.length === 0 && (
                <div className="border-t border-[var(--border-soft)] bg-[var(--surface)] p-4 text-center text-[var(--text-secondary)] text-sm">
                    暂无渠道配置
                </div>
            )}
        </div>
    );
};

const CapabilityPrices: React.FC = () => {
    const [capabilities, setCapabilities] = useState<CapabilityPrice[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [searchTerm, setSearchTerm] = useState('');
    const [filterType, setFilterType] = useState<string>('');

    useEffect(() => {
        loadData();
    }, []);

    const loadData = async () => {
        setIsLoading(true);
        try {
            const data = await fetchCapabilityPrices();
            setCapabilities(data);
        } catch (err) {
            console.error('Failed to load capability prices:', err);
        } finally {
            setIsLoading(false);
        }
    };

    const filteredCapabilities = capabilities.filter(cap => {
        const matchSearch = !searchTerm ||
            cap.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
            cap.code.toLowerCase().includes(searchTerm.toLowerCase());
        const matchType = !filterType || cap.type === filterType;
        return matchSearch && matchType;
    });

    const types = [...new Set(capabilities.map(c => c.type))];

    if (isLoading) {
        return (
            <div className="flex items-center justify-center h-64">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-indigo-600"></div>
            </div>
        );
    }

    return (
        <div className="space-y-6">
            <div className="flex items-center justify-between">
                <div>
                    <h1 className="text-2xl font-bold text-[var(--text-primary)]">能力列表</h1>
                    <p className="text-[var(--text-secondary)] mt-1">查看所有可用能力及其价格</p>
                </div>
            </div>

            <div className="flex items-center gap-4">
                <div className="relative flex-1 max-w-md">
                    <Search size={18} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" />
                    <input
                        type="text"
                        placeholder="搜索能力名称或编码..."
                        value={searchTerm}
                        onChange={e => setSearchTerm(e.target.value)}
                        className="w-full pl-10 pr-4 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)] text-sm"
                    />
                </div>
                <select
                    value={filterType}
                    onChange={e => setFilterType(e.target.value)}
                    className="px-4 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)] text-sm"
                >
                    <option value="">全部类型</option>
                    {types.map(type => (
                        <option key={type} value={type}>{TYPE_MAP[type]?.label || type}</option>
                    ))}
                </select>
            </div>

            <div className="space-y-3">
                {filteredCapabilities.map(cap => (
                    <CapabilityCard key={cap.code} capability={cap} />
                ))}
            </div>

            {filteredCapabilities.length === 0 && (
                <div className="text-center py-12 text-[var(--text-secondary)]">
                    {searchTerm || filterType ? '没有找到匹配的能力' : '暂无可用能力'}
                </div>
            )}
        </div>
    );
};

export default CapabilityPrices;
