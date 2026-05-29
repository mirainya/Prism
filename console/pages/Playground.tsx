import React, { useState, useEffect } from 'react';
import { Play, Bot, Loader2, Zap } from 'lucide-react';
import { fetchTokens } from '../services/api';
import { ApiToken } from '../types';
import ChatTab from './playground/ChatTab';
import CapabilityTab from './playground/CapabilityTab';

type TabType = 'chat' | 'capability';

const Playground: React.FC = () => {
  const [activeTab, setActiveTab] = useState<TabType>('chat');
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [selectedTokenId, setSelectedTokenId] = useState('');
  const [isLoadingTokens, setIsLoadingTokens] = useState(true);

  useEffect(() => {
    setIsLoadingTokens(true);
    fetchTokens()
      .then(list => {
        const active = list.filter((t: ApiToken) => t.status === 'active');
        setTokens(active);
        if (active.length > 0 && !selectedTokenId) {
          setSelectedTokenId(active[0].id);
        }
      })
      .catch(() => setTokens([]))
      .finally(() => setIsLoadingTokens(false));
  }, []);

  const tabs = [
    { key: 'chat' as TabType, label: 'Chat 调试', icon: <Bot size={16} /> },
    { key: 'capability' as TabType, label: '能力调用', icon: <Zap size={16} /> },
  ];

  return (
    <div className="h-[calc(100dvh-7rem)] md:h-[calc(100dvh-8rem)] flex flex-col">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 mb-3 md:mb-4">
        <div className="flex items-center gap-1 bg-[var(--primary-lighter)] rounded-xl p-1">
          {tabs.map(tab => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`flex items-center gap-1.5 px-3 md:px-4 py-2 rounded-lg text-sm font-medium transition-all ${activeTab === tab.key ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}
            >
              {tab.icon} {tab.label}
            </button>
          ))}
        </div>

        <div className="flex items-center gap-2 min-w-0">
          <label className="text-sm text-[var(--text-secondary)] flex-shrink-0">令牌：</label>
          {isLoadingTokens ? (
            <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)]"><Loader2 size={14} className="animate-spin" /> 加载中...</div>
          ) : tokens.length === 0 ? (
            <span className="text-sm text-red-500">暂无可用令牌，请先创建</span>
          ) : (
            <select value={selectedTokenId} onChange={e => setSelectedTokenId(e.target.value)} className="px-3 py-1.5 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)] min-w-0 flex-1 max-w-[260px]">
              {tokens.map(t => <option key={t.id} value={t.id}>{t.name} (余额: ¥{t.balance.toFixed(2)})</option>)}
            </select>
          )}
        </div>
      </div>

      <div className="flex-1 min-h-0">
        {!selectedTokenId ? (
          <div className="h-full flex items-center justify-center text-[var(--text-secondary)]">
            <div className="text-center">
              <Play size={48} className="mx-auto mb-3 opacity-30" />
              <p>请先选择一个令牌开始试用</p>
            </div>
          </div>
        ) : activeTab === 'chat' ? (
          <ChatTab tokenId={selectedTokenId} />
        ) : (
          <CapabilityTab tokenId={selectedTokenId} />
        )}
      </div>
    </div>
  );
};

export default Playground;
