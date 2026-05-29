import React, { useEffect, useMemo, useState } from 'react';
import { Plus, Settings, Cpu, Edit2, Trash2, ChevronDown, ChevronRight, Power, RefreshCw, Search } from 'lucide-react';
import {
    fetchCapabilities, fetchChannelCapabilities, fetchChannels,
    updateCapability, deleteCapability,
    updateChannelCapability, deleteChannelCapability
} from '../services/api';
import { Capability, ChannelCapability, Channel } from '../types';
import {
    CAPABILITY_TYPES, CAPABILITY_TYPE_ORDER, RESULT_MODES,
    normalizeText, getCapabilityTypeLabel, getCapabilityTypeBadgeClass, formatPrice,
} from './capabilities/constants';
import CapabilityModal from './capabilities/CapabilityModal';
import ChannelCapabilityModal from './capabilities/ChannelCapabilityModal';
import { Select } from '../components/ui';

const Capabilities: React.FC = () => {
  const [capabilities, setCapabilities] = useState<Capability[]>([]);
  const [channelCapabilities, setChannelCapabilities] = useState<ChannelCapability[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [expandedCapability, setExpandedCapability] = useState<string | null>(null);
  const [searchTerm, setSearchTerm] = useState('');
  const [filterType, setFilterType] = useState('');
  const [filterStatus, setFilterStatus] = useState('all');
  const [filterChannel, setFilterChannel] = useState('');
  const [filterResultMode, setFilterResultMode] = useState('');

    const [capabilityModal, setCapabilityModal] = useState<{
        open: boolean;
        capability: Capability | null
    }>({open: false, capability: null});
    const [ccModal, setCcModal] = useState<{
        open: boolean;
        capabilityCode: string;
        cc: ChannelCapability | null
    }>({open: false, capabilityCode: '', cc: null});

    const channelNameMap = useMemo(() => {
        const map = new Map<string, string>();
        channels.forEach(channel => { map.set(channel.id, channel.name); });
        return map;
    }, [channels]);

    const channelCapabilitiesByCodeMap = useMemo(() => {
        const map = new Map<string, ChannelCapability[]>();
        channelCapabilities.forEach(cc => {
            const list = map.get(cc.capabilityCode) || [];
            list.push(cc);
            map.set(cc.capabilityCode, list);
        });
        return map;
    }, [channelCapabilities]);

    // 应用 渠道 + 交互模式 筛选后，每个能力的可见渠道配置
    const visibleCCsByCodeMap = useMemo(() => {
        const map = new Map<string, ChannelCapability[]>();
        channelCapabilitiesByCodeMap.forEach((list, code) => {
            map.set(code, list.filter(cc =>
                (!filterChannel || String(cc.channelId) === filterChannel) &&
                (!filterResultMode || cc.resultMode === filterResultMode)
            ));
        });
        return map;
    }, [channelCapabilitiesByCodeMap, filterChannel, filterResultMode]);

    const stats = useMemo(() => ({
        totalCapabilities: capabilities.length,
        enabledCapabilities: capabilities.filter(cap => cap.status === 1).length,
        totalChannelCapabilities: channelCapabilities.length,
        enabledChannelCapabilities: channelCapabilities.filter(cc => cc.status === 1).length,
    }), [capabilities, channelCapabilities]);

    const filteredCapabilities = useMemo(() => {
        const keyword = searchTerm.trim().toLowerCase();
        const ccFilterActive = !!filterChannel || !!filterResultMode;
        return capabilities.filter(cap => {
            const normalizedType = cap.type || 'other';
            const relatedCCs = channelCapabilitiesByCodeMap.get(cap.code) || [];
            const matchesType = !filterType || normalizedType === filterType;
            const matchesStatus = filterStatus === 'all'
                || (filterStatus === 'enabled' && cap.status === 1)
                || (filterStatus === 'disabled' && cap.status !== 1);
            const matchesCcFilter = !ccFilterActive || (visibleCCsByCodeMap.get(cap.code)?.length || 0) > 0;
            const matchesKeyword = !keyword || [
                cap.name, cap.code, cap.description, normalizedType,
                ...relatedCCs.flatMap(cc => [cc.name, cc.model, cc.requestPath, cc.resultMode, channelNameMap.get(cc.channelId)]),
            ].some(field => normalizeText(field).includes(keyword));
            return matchesType && matchesStatus && matchesCcFilter && matchesKeyword;
        });
    }, [capabilities, channelCapabilitiesByCodeMap, visibleCCsByCodeMap, channelNameMap, filterStatus, filterType, filterChannel, filterResultMode, searchTerm]);

    const groupedCapabilities = useMemo(() => {
        const groups = new Map<string, Capability[]>();
        filteredCapabilities.forEach(cap => {
            const type = CAPABILITY_TYPE_ORDER.includes((cap.type || 'other') as typeof CAPABILITY_TYPE_ORDER[number])
                ? (cap.type || 'other') : 'other';
            const list = groups.get(type) || [];
            list.push(cap);
            groups.set(type, list);
        });
        return CAPABILITY_TYPE_ORDER
            .filter(type => groups.has(type))
            .map(type => {
                const items = (groups.get(type) || []).slice().sort((a, b) => a.name.localeCompare(b.name));
                return {
                    type, label: getCapabilityTypeLabel(type), items,
                    capabilityCount: items.length,
                    enabledCount: items.filter(cap => cap.status === 1).length,
                    channelCapabilityCount: items.reduce((sum, cap) => sum + (visibleCCsByCodeMap.get(cap.code)?.length || 0), 0),
                };
            });
    }, [filteredCapabilities, channelCapabilitiesByCodeMap, visibleCCsByCodeMap]);

    const resetFilters = () => { setSearchTerm(''); setFilterType(''); setFilterStatus('all'); setFilterChannel(''); setFilterResultMode(''); };

    useEffect(() => { loadData(); }, []);

  const loadData = async () => {
    setIsLoading(true);
    try {
      const [caps, ccs, chs] = await Promise.all([fetchCapabilities(), fetchChannelCapabilities(), fetchChannels()]);
      setCapabilities(caps);
      setChannelCapabilities(ccs);
      setChannels(chs);
    } finally {
      setIsLoading(false);
    }
  };

  const handleDeleteCapability = async (code: string) => {
      if (!confirm('确定删除该能力定义? 相关的渠道配置也会被删除。')) return;
    await deleteCapability(code);
    loadData();
  };

  const handleDeleteChannelCapability = async (id: string) => {
    if (!confirm('确定删除该渠道能力配置?')) return;
    await deleteChannelCapability(id);
    loadData();
  };

    const handleToggleCapabilityStatus = async (cap: Capability) => {
        await updateCapability(cap.code, {status: cap.status === 1 ? 0 : 1});
        loadData();
    };

    const handleToggleCcStatus = async (cc: ChannelCapability) => {
        await updateChannelCapability(cc.id, {status: cc.status === 1 ? 0 : 1});
        loadData();
    };

  return (
    <div className="space-y-4 md:space-y-6">

      {isLoading ? (
        <div className="animate-pulse space-y-4">
          {[1, 2, 3].map(i => (
            <div key={i} className="bg-[var(--surface-card)] p-4 md:p-6 rounded-2xl border border-[var(--border-soft)] h-24"></div>
          ))}
        </div>
      ) : (<>

      <div className="flex flex-col gap-3 md:gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">
            能力配置
            <span className="ml-2 md:ml-3 text-xs md:text-base font-normal text-[var(--text-secondary)]">
              {stats.totalCapabilities} 能力 / {stats.enabledCapabilities} 启用
            </span>
          </h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">管理平台能力定义和渠道能力映射</p>
        </div>
          <div className="flex flex-wrap gap-2">
              <button onClick={loadData}
                      className="flex items-center gap-2 px-3 md:px-4 py-2 border border-[var(--border-soft)] rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface)]">
                  <RefreshCw size={16} className={isLoading ? 'animate-spin' : ''}/>
                  <span className="hidden md:inline">刷新</span>
              </button>
              <button
                  onClick={() => setCapabilityModal({open: true, capability: null})}
                  className="flex items-center gap-2 px-4 md:px-6 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 transition-all shadow-sm"
              >
                  <Plus size={18}/>
                  <span className="hidden md:inline">新建能力</span>
                  <span className="md:hidden">新建</span>
              </button>
          </div>
      </div>

      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] p-3 md:p-4 space-y-3 md:space-y-4">
        <div className="flex flex-col lg:flex-row gap-3 md:gap-4">
          <div className="relative flex-1">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" />
            <input type="text" value={searchTerm} onChange={e => setSearchTerm(e.target.value)}
              placeholder="搜索能力名、编码..."
              className="w-full pl-9 pr-4 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)] text-sm" />
          </div>
          <div className="lg:w-52">
            <Select value={filterType} onChange={setFilterType} placeholder="全部类型"
              options={[{ label: '全部类型', value: '' }, ...CAPABILITY_TYPES]} />
          </div>
          <div className="lg:w-52">
            <Select value={filterStatus} onChange={setFilterStatus} placeholder="全部状态"
              options={[{ label: '全部状态', value: 'all' }, { label: '仅启用', value: 'enabled' }, { label: '仅禁用', value: 'disabled' }]} />
          </div>
          <div className="lg:w-52">
            <Select value={filterChannel} onChange={setFilterChannel} placeholder="全部渠道"
              options={[{ label: '全部渠道', value: '' }, ...channels.map(ch => ({ label: ch.name, value: String(ch.id) }))]} />
          </div>
          <div className="lg:w-52">
            <Select value={filterResultMode} onChange={setFilterResultMode} placeholder="全部交互模式"
              options={[{ label: '全部交互模式', value: '' }, ...RESULT_MODES]} />
          </div>
          <button type="button" onClick={resetFilters}
            className="px-4 py-2 border border-[var(--border-soft)] rounded-lg text-sm text-[var(--text-primary)] hover:bg-[var(--surface)] transition-colors">
            重置筛选
          </button>
        </div>
        <div className="text-sm text-[var(--text-secondary)]">
          共 {stats.totalCapabilities} 个能力，当前显示 {filteredCapabilities.length} 个
        </div>
      </div>

      <div className="space-y-4">
        {capabilities.length === 0 ? (
          <div className="bg-[var(--surface-card)] rounded-2xl border border-[var(--border-soft)] p-12 text-center">
            <Cpu size={48} className="mx-auto text-gray-300 mb-4" />
            <h3 className="text-lg font-bold text-[var(--text-primary)] mb-2">暂无能力定义</h3>
            <p className="text-[var(--text-secondary)] mb-4">点击上方按钮创建第一个能力</p>
            <button onClick={() => setCapabilityModal({open: true, capability: null})}
              className="inline-flex items-center gap-2 px-4 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90">
              <Plus size={16} />
              新建能力
            </button>
          </div>
        ) : groupedCapabilities.length === 0 ? (
          <div className="bg-[var(--surface-card)] rounded-2xl border border-[var(--border-soft)] p-12 text-center">
            <Search size={40} className="mx-auto text-gray-300 mb-4" />
            <h3 className="text-lg font-bold text-[var(--text-primary)] mb-2">无匹配能力</h3>
            <p className="text-[var(--text-secondary)] mb-4">请调整搜索词或筛选条件后重试</p>
            <button type="button" onClick={resetFilters}
              className="inline-flex items-center gap-2 px-4 py-2 border border-[var(--border-soft)] rounded-lg text-sm text-[var(--text-primary)] hover:bg-[var(--surface)]">
              重置筛选
            </button>
          </div>
        ) : (
          groupedCapabilities.map(group => (
            <div key={group.type} className="bg-[var(--surface-card)] rounded-2xl border border-[var(--border-soft)] shadow-sm overflow-hidden">
              <div className="p-5 border-b border-[var(--border-soft)] bg-[var(--surface)]/70">
                <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                  <div className="flex items-center gap-3">
                    <div className={`px-3 py-1.5 rounded-xl text-sm font-semibold ${getCapabilityTypeBadgeClass(group.type)}`}>{group.label}</div>
                    <div>
                      <div className="text-base font-bold text-[var(--text-primary)]">{group.label}能力</div>
                      <div className="text-sm text-[var(--text-secondary)] mt-1">
                        共 {group.capabilityCount} 个能力，已启用 {group.enabledCount} 个，关联 {group.channelCapabilityCount} 个渠道配置
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <div className="p-4 space-y-4">
                {group.items.map(cap => {
                  const isExpanded = expandedCapability === cap.code;
                  const ccFilterActive = !!filterChannel || !!filterResultMode;
                  const relatedCCs = (ccFilterActive ? visibleCCsByCodeMap.get(cap.code) : channelCapabilitiesByCodeMap.get(cap.code)) || [];
                  const enabledCCCount = relatedCCs.filter(cc => cc.status === 1).length;
                  const standardParamCount = Object.keys(cap.standardParams || {}).length;

                  return (
                    <div key={cap.code} className="rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-sm overflow-hidden">
                      <div className="p-5 flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between cursor-pointer hover:bg-[var(--surface)]"
                        onClick={() => setExpandedCapability(isExpanded ? null : cap.code)}>
                        <div className="flex items-start gap-4 min-w-0 flex-1">
                          <div className="pt-1 text-[var(--text-secondary)] shrink-0">
                            {isExpanded ? <ChevronDown size={20} /> : <ChevronRight size={20} />}
                          </div>
                          <div className="p-2.5 bg-[var(--primary-lighter)] text-[var(--primary)] rounded-xl shrink-0"><Cpu size={20} /></div>
                          <div className="min-w-0 flex-1 space-y-3">
                            <div className="flex flex-wrap items-center gap-2">
                              <h3 className="text-lg font-bold text-[var(--text-primary)]">{cap.name}</h3>
                              <span className={`text-xs px-2 py-1 rounded-full ${cap.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                {cap.status === 1 ? '启用' : '禁用'}
                              </span>
                            </div>
                            <div className="flex flex-wrap items-center gap-2 text-sm">
                              <code className="px-2 py-1 bg-[var(--primary-lighter)] rounded text-[var(--text-secondary)]">{cap.code}</code>
                              <span className={`px-2 py-1 rounded ${getCapabilityTypeBadgeClass(cap.type)}`}>{getCapabilityTypeLabel(cap.type)}</span>
                              <span className="px-2 py-1 rounded bg-[var(--primary-lighter)] text-[var(--text-secondary)]">渠道配置 {relatedCCs.length}</span>
                              <span className="px-2 py-1 rounded bg-[var(--primary-lighter)] text-[var(--text-secondary)]">标准参数 {standardParamCount}</span>
                            </div>
                            <p className="text-sm text-[var(--text-secondary)] leading-6">{cap.description || '暂无描述'}</p>
                          </div>
                        </div>
                        <div className="flex items-center gap-2 shrink-0 self-end xl:self-start">
                          <button onClick={e => { e.stopPropagation(); handleToggleCapabilityStatus(cap); }}
                            className={`inline-flex items-center gap-1 px-3 py-2 rounded-lg text-sm ${cap.status === 1 ? 'text-yellow-700 bg-yellow-50 hover:bg-yellow-100' : 'text-green-700 bg-green-50 hover:bg-green-100'}`}
                            title={cap.status === 1 ? '禁用' : '启用'}>
                            <Power size={16} />
                            {cap.status === 1 ? '禁用' : '启用'}
                          </button>
                          <button onClick={e => { e.stopPropagation(); setCapabilityModal({open: true, capability: cap}); }}
                            className="inline-flex items-center gap-1 px-3 py-2 text-sm text-[var(--primary)] bg-[var(--primary-lighter)] hover:bg-indigo-100 rounded-lg">
                            <Edit2 size={16} />
                            编辑
                          </button>
                          <button onClick={e => { e.stopPropagation(); handleDeleteCapability(cap.code); }}
                            className="inline-flex items-center gap-1 px-3 py-2 text-sm text-red-600 bg-red-50 hover:bg-red-100 rounded-lg">
                            <Trash2 size={16} />
                            删除
                          </button>
                        </div>
                      </div>

                      {isExpanded && (
                        <div className="border-t border-[var(--border-soft)] bg-[var(--surface)]/70 p-4">
                          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between mb-4">
                            <div>
                              <h4 className="text-sm font-bold text-gray-800">渠道能力配置</h4>
                              <p className="text-sm text-[var(--text-secondary)] mt-1">共 {relatedCCs.length} 个配置，已启用 {enabledCCCount} 个</p>
                            </div>
                            <button onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc: null})}
                              className="inline-flex items-center gap-2 px-4 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90">
                              <Plus size={14} />
                              添加渠道配置
                            </button>
                          </div>

                          {relatedCCs.length === 0 ? (
                            <div className="bg-[var(--surface-card)] border border-dashed border-[var(--border-soft)] rounded-2xl px-6 py-10 text-center">
                              <Settings size={32} className="mx-auto text-gray-300 mb-3" />
                              <div className="text-sm font-medium text-[var(--text-primary)]">当前能力还没有渠道配置</div>
                              <div className="text-sm text-[var(--text-secondary)] mt-1 mb-4">可以立即添加一个渠道配置来接入具体渠道能力</div>
                              <button onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc: null})}
                                className="inline-flex items-center gap-2 px-4 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90">
                                <Plus size={14} />
                                添加渠道配置
                              </button>
                            </div>
                          ) : (
                            <div className="space-y-3">
                              {relatedCCs.map(cc => (
                                <div key={cc.id} className="bg-[var(--surface-card)] p-4 rounded-2xl border border-[var(--border-soft)] shadow-sm flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
                                  <div className="flex items-start gap-4 min-w-0 flex-1">
                                    <div className="p-2 bg-[var(--primary-lighter)] rounded-xl shrink-0">
                                      <Settings size={16} className="text-[var(--text-secondary)]" />
                                    </div>
                                    <div className="min-w-0 flex-1 space-y-2">
                                      <div className="flex flex-wrap items-center gap-2">
                                        <span className="font-semibold text-[var(--text-primary)]">{cc.name || cc.model || '未命名'}</span>
                                        <span className="text-xs px-2 py-1 rounded-full bg-blue-50 text-blue-600">{channelNameMap.get(cc.channelId) || cc.channelId}</span>
                                        <span className="text-xs px-2 py-1 rounded-full bg-purple-50 text-purple-600">{cc.resultMode}</span>
                                        <span className={`text-xs px-2 py-1 rounded-full ${cc.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                          {cc.status === 1 ? '启用' : '禁用'}
                                        </span>
                                      </div>
                                      <div className="flex flex-wrap gap-3 text-sm text-[var(--text-secondary)]">
                                        <span>{cc.requestMethod} {cc.requestPath || '未配置路径'}</span>
                                        <span>价格 {formatPrice(cc.price)}/{cc.priceUnit || 'request'}</span>
                                        {cc.model ? <span>模型 {cc.model}</span> : null}
                                      </div>
                                    </div>
                                  </div>
                                  <div className="flex flex-wrap items-center gap-2 shrink-0">
                                    <button onClick={() => handleToggleCcStatus(cc)}
                                      className={`inline-flex items-center gap-1 px-3 py-2 rounded-lg text-sm ${cc.status === 1 ? 'text-yellow-700 bg-yellow-50 hover:bg-yellow-100' : 'text-green-700 bg-green-50 hover:bg-green-100'}`}
                                      title={cc.status === 1 ? '禁用' : '启用'}>
                                      <Power size={14} />
                                      {cc.status === 1 ? '禁用' : '启用'}
                                    </button>
                                    <button onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc})}
                                      className="inline-flex items-center gap-1 px-3 py-2 text-sm text-[var(--primary)] bg-[var(--primary-lighter)] hover:bg-indigo-100 rounded-lg">
                                      <Edit2 size={14} />
                                      编辑
                                    </button>
                                    <button onClick={() => handleDeleteChannelCapability(cc.id)}
                                      className="inline-flex items-center gap-1 px-3 py-2 text-sm text-red-600 bg-red-50 hover:bg-red-100 rounded-lg">
                                      <Trash2 size={14} />
                                      删除
                                    </button>
                                  </div>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ))
        )}
      </div>

        <CapabilityModal
            isOpen={capabilityModal.open}
            capability={capabilityModal.capability}
            onClose={() => setCapabilityModal({open: false, capability: null})}
            onSave={loadData}
        />
        <ChannelCapabilityModal
            isOpen={ccModal.open}
            capabilityCode={ccModal.capabilityCode}
            channelCapability={ccModal.cc}
            channels={channels}
            capabilities={capabilities}
            onClose={() => setCcModal({open: false, capabilityCode: '', cc: null})}
            onSave={loadData}
        />
      </>)}
    </div>
  );
};

export default Capabilities;
