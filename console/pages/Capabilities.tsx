import React, { useEffect, useMemo, useState } from 'react';
import { Plus, Settings, Cpu, Edit2, Trash2, ChevronDown, ChevronRight, Power, RefreshCw, Search, GripVertical } from 'lucide-react';
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, DragStartEvent, DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, rectSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
    fetchCapabilities, fetchChannelCapabilities, fetchChannels,
    updateCapability, deleteCapability, reorderCapabilities,
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

// 可拖拽能力卡外壳:useSortable 作用于外层卡片,通过 render-prop 把拖拽手柄注入卡头
const SortableCapabilityCard: React.FC<{
    code: string;
    isExpanded: boolean;
    canDrag: boolean;
    children: (dragHandle: React.ReactNode) => React.ReactNode;
}> = ({ code, isExpanded, canDrag, children }) => {
    const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: code, disabled: !canDrag });
    const style = { transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.4 : 1 };
    const dragHandle = canDrag ? (
        <span {...attributes} {...listeners} onClick={e => e.stopPropagation()}
            className="p-1 -ml-1 text-[var(--text-secondary)] hover:text-[var(--primary)] cursor-grab active:cursor-grabbing touch-none shrink-0"
            title="拖拽排序(同类型内)">
            <GripVertical size={16} />
        </span>
    ) : null;
    return (
        <div ref={setNodeRef} style={style}
            className={`rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-sm overflow-hidden flex flex-col ${isExpanded ? 'md:col-span-2 xl:col-span-3' : ''}`}>
            {children(dragHandle)}
        </div>
    );
};

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

    // 扁平卡片列表:按类型顺序聚类、类型内按名称排序(去掉分组盒子,类型仅作卡内徽章+筛选)
    const sortedCapabilities = useMemo(() => {
        const typeIndex = (cap: Capability) => {
            const type = (cap.type || 'other') as typeof CAPABILITY_TYPE_ORDER[number];
            const idx = CAPABILITY_TYPE_ORDER.indexOf(type);
            return idx === -1 ? CAPABILITY_TYPE_ORDER.length : idx;
        };
        return filteredCapabilities.slice().sort((a, b) => {
            const diff = typeIndex(a) - typeIndex(b);
            if (diff !== 0) return diff;
            if ((b.sort || 0) !== (a.sort || 0)) return (b.sort || 0) - (a.sort || 0);
            return a.name.localeCompare(b.name);
        });
    }, [filteredCapabilities]);

    const resetFilters = () => { setSearchTerm(''); setFilterType(''); setFilterStatus('all'); setFilterChannel(''); setFilterResultMode(''); };

    useEffect(() => { loadData(); }, []);

  // silent=true: 静默刷新(不显示骨架屏),保留列表 DOM → 滚动位置与展开态不丢失
  const loadData = async (silent = false) => {
    if (!silent) setIsLoading(true);
    try {
      const [caps, ccs, chs] = await Promise.all([fetchCapabilities(), fetchChannelCapabilities(), fetchChannels()]);
      setCapabilities(caps);
      setChannelCapabilities(ccs);
      setChannels(chs);
    } finally {
      if (!silent) setIsLoading(false);
    }
  };

  const handleDeleteCapability = async (code: string) => {
      if (!confirm('确定删除该能力定义? 相关的渠道配置也会被删除。')) return;
    await deleteCapability(code);
    loadData(true);
  };

  const handleDeleteChannelCapability = async (id: string) => {
    if (!confirm('确定删除该渠道能力配置?')) return;
    await deleteChannelCapability(id);
    loadData(true);
  };

    const handleToggleCapabilityStatus = async (cap: Capability) => {
        await updateCapability(cap.code, {status: cap.status === 1 ? 0 : 1});
        loadData(true);
    };

    const handleToggleCcStatus = async (cc: ChannelCapability) => {
        await updateChannelCapability(cc.id, {status: cc.status === 1 ? 0 : 1});
        loadData(true);
    };

    // 拖拽排序:仅同类型内允许(跨类型忽略);搜索/筛选激活时禁拖(顺序会被筛选打乱)
    const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
    const filterActive = !!searchTerm.trim() || !!filterType || filterStatus !== 'all' || !!filterChannel || !!filterResultMode;
    const canDrag = !filterActive;

    const handleDragStart = (_e: DragStartEvent) => {
        setExpandedCapability(null); // 拖拽时收起展开卡,避免网格错位
    };

    const handleDragEnd = async (e: DragEndEvent) => {
        const { active, over } = e;
        if (!over || active.id === over.id) return;
        const activeCap = capabilities.find(c => c.code === active.id);
        const overCap = capabilities.find(c => c.code === over.id);
        if (!activeCap || !overCap) return;
        // 仅同类型内拖拽
        if ((activeCap.type || 'other') !== (overCap.type || 'other')) return;

        const oldIndex = sortedCapabilities.findIndex(c => c.code === active.id);
        const newIndex = sortedCapabilities.findIndex(c => c.code === over.id);
        if (oldIndex < 0 || newIndex < 0) return;
        const reordered = arrayMove<Capability>(sortedCapabilities, oldIndex, newIndex);
        // 乐观更新:按新次序重算同类型内的 sort(降序,首个最大)
        const sameType = reordered.filter(c => (c.type || 'other') === (activeCap.type || 'other'));
        const sortMap = new Map(sameType.map((c, i) => [c.code, sameType.length - i]));
        setCapabilities(prev => prev.map(c => sortMap.has(c.code) ? { ...c, sort: sortMap.get(c.code)! } : c));
        try {
            await reorderCapabilities(sameType.map(c => c.code));
        } catch {
            loadData(true); // 失败回滚
        }
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
              <button onClick={() => loadData()}
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
        ) : sortedCapabilities.length === 0 ? (
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
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
          <SortableContext items={sortedCapabilities.map(c => c.code)} strategy={rectSortingStrategy}>
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-start">
                {sortedCapabilities.map(cap => {
                  const isExpanded = expandedCapability === cap.code;
                  const ccFilterActive = !!filterChannel || !!filterResultMode;
                  const relatedCCs = (ccFilterActive ? visibleCCsByCodeMap.get(cap.code) : channelCapabilitiesByCodeMap.get(cap.code)) || [];
                  const enabledCCCount = relatedCCs.filter(cc => cc.status === 1).length;
                  const standardParamCount = Object.keys(cap.standardParams || {}).length;

                  return (
                    <SortableCapabilityCard key={cap.code} code={cap.code} isExpanded={isExpanded} canDrag={canDrag}>
                      {(dragHandle) => (
                      <>
                      {/* 卡头:图标+名称+状态,点击展开/收起 */}
                      <div className="p-4 flex items-start gap-3 cursor-pointer hover:bg-[var(--surface)]"
                        onClick={() => setExpandedCapability(isExpanded ? null : cap.code)}>
                        {dragHandle}
                        <div className="p-2.5 bg-[var(--primary-lighter)] text-[var(--primary)] rounded-xl shrink-0"><Cpu size={20} /></div>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <h3 className="text-base font-bold text-[var(--text-primary)] truncate" title={cap.name}>{cap.name}</h3>
                            <span className={`text-xs px-2 py-0.5 rounded-full shrink-0 ${cap.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                              {cap.status === 1 ? '启用' : '禁用'}
                            </span>
                          </div>
                          <div className="text-xs text-[var(--text-secondary)] mt-1 truncate" title={cap.code}>{cap.code}</div>
                        </div>
                        <div className="pt-1 text-[var(--text-secondary)] shrink-0">
                          {isExpanded ? <ChevronDown size={18} /> : <ChevronRight size={18} />}
                        </div>
                      </div>

                      {/* 标签:类型 + 渠道配置数 + 标准参数数 */}
                      <div className="flex flex-wrap items-center gap-2 px-4 text-xs">
                        <span className={`px-2 py-1 rounded ${getCapabilityTypeBadgeClass(cap.type)}`}>{getCapabilityTypeLabel(cap.type)}</span>
                        <span className="px-2 py-1 rounded bg-[var(--primary-lighter)] text-[var(--text-secondary)]">渠道配置 {relatedCCs.length}</span>
                        <span className="px-2 py-1 rounded bg-[var(--primary-lighter)] text-[var(--text-secondary)]">标准参数 {standardParamCount}</span>
                      </div>

                      {/* 描述 */}
                      <p className="px-4 pt-3 text-sm text-[var(--text-secondary)] leading-6 line-clamp-2" title={cap.description || ''}>
                        {cap.description || '暂无描述'}
                      </p>

                      {/* 操作:禁用/启用 · 编辑 · 删除 */}
                      <div className="flex items-center gap-2 px-4 py-3 mt-3 bg-[var(--surface)]/60 border-t border-[var(--border-soft)]">
                        <button onClick={() => handleToggleCapabilityStatus(cap)}
                          className={`inline-flex items-center justify-center gap-1 flex-1 px-2 py-1.5 rounded-lg text-sm ${cap.status === 1 ? 'text-yellow-700 bg-yellow-50 hover:bg-yellow-100' : 'text-green-700 bg-green-50 hover:bg-green-100'}`}
                          title={cap.status === 1 ? '禁用' : '启用'}>
                          <Power size={14} />
                          {cap.status === 1 ? '禁用' : '启用'}
                        </button>
                        <button onClick={() => setCapabilityModal({open: true, capability: cap})}
                          className="inline-flex items-center justify-center gap-1 flex-1 px-2 py-1.5 text-sm text-[var(--primary)] bg-[var(--primary-lighter)] hover:bg-indigo-100 rounded-lg">
                          <Edit2 size={14} />
                          编辑
                        </button>
                        <button onClick={() => handleDeleteCapability(cap.code)}
                          className="inline-flex items-center justify-center gap-1 flex-1 px-2 py-1.5 text-sm text-red-600 bg-red-50 hover:bg-red-100 rounded-lg">
                          <Trash2 size={14} />
                          删除
                        </button>
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
                            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
                              {relatedCCs.map(cc => (
                                <div key={cc.id} className="bg-[var(--surface-card)] rounded-2xl border border-[var(--border-soft)] shadow-sm flex flex-col overflow-hidden">
                                  {/* 头部:图标 + 名称 + 状态 */}
                                  <div className="flex items-center gap-2.5 p-4 pb-3">
                                    <div className="p-2 bg-[var(--primary-lighter)] rounded-xl shrink-0">
                                      <Settings size={16} className="text-[var(--text-secondary)]" />
                                    </div>
                                    <span className="font-semibold text-[var(--text-primary)] truncate flex-1" title={cc.name || cc.model || '未命名'}>
                                      {cc.name || cc.model || '未命名'}
                                    </span>
                                    <span className={`text-xs px-2 py-1 rounded-full shrink-0 ${cc.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                      {cc.status === 1 ? '启用' : '禁用'}
                                    </span>
                                  </div>
                                  {/* 标签:渠道 + 模式 */}
                                  <div className="flex flex-wrap items-center gap-2 px-4">
                                    <span className="text-xs px-2 py-1 rounded-full bg-blue-50 text-blue-600 truncate max-w-full" title={channelNameMap.get(cc.channelId) || String(cc.channelId)}>
                                      {channelNameMap.get(cc.channelId) || cc.channelId}
                                    </span>
                                    <span className="text-xs px-2 py-1 rounded-full bg-purple-50 text-purple-600">{cc.resultMode}</span>
                                  </div>
                                  {/* 明细:方法路径 / 价格 / 模型 */}
                                  <div className="px-4 pt-3 pb-4 mt-3 space-y-1.5 text-sm text-[var(--text-secondary)] border-t border-[var(--border-soft)]">
                                    <div className="truncate" title={`${cc.requestMethod} ${cc.requestPath || ''}`}>
                                      <span className="text-[var(--text-primary)] font-medium mr-1.5">{cc.requestMethod}</span>
                                      {cc.requestPath || '未配置路径'}
                                    </div>
                                    <div>价格 {formatPrice(cc.price)}/{cc.priceUnit || 'request'}</div>
                                    {cc.model ? <div className="truncate" title={cc.model}>模型 {cc.model}</div> : null}
                                  </div>
                                  {/* 操作 */}
                                  <div className="flex items-center gap-2 px-4 py-3 mt-auto bg-[var(--surface)]/60 border-t border-[var(--border-soft)]">
                                    <button onClick={() => handleToggleCcStatus(cc)}
                                      className={`inline-flex items-center justify-center gap-1 flex-1 px-2 py-1.5 rounded-lg text-sm ${cc.status === 1 ? 'text-yellow-700 bg-yellow-50 hover:bg-yellow-100' : 'text-green-700 bg-green-50 hover:bg-green-100'}`}
                                      title={cc.status === 1 ? '禁用' : '启用'}>
                                      <Power size={14} />
                                      {cc.status === 1 ? '禁用' : '启用'}
                                    </button>
                                    <button onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc})}
                                      className="inline-flex items-center justify-center gap-1 flex-1 px-2 py-1.5 text-sm text-[var(--primary)] bg-[var(--primary-lighter)] hover:bg-indigo-100 rounded-lg">
                                      <Edit2 size={14} />
                                      编辑
                                    </button>
                                    <button onClick={() => handleDeleteChannelCapability(cc.id)}
                                      className="inline-flex items-center justify-center gap-1 flex-1 px-2 py-1.5 text-sm text-red-600 bg-red-50 hover:bg-red-100 rounded-lg">
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
                      </>
                      )}
                    </SortableCapabilityCard>
                  );
                })}
          </div>
          </SortableContext>
          </DndContext>
        )}
      </div>

        <CapabilityModal
            isOpen={capabilityModal.open}
            capability={capabilityModal.capability}
            onClose={() => setCapabilityModal({open: false, capability: null})}
            onSave={() => loadData(true)}
        />
        <ChannelCapabilityModal
            isOpen={ccModal.open}
            capabilityCode={ccModal.capabilityCode}
            channelCapability={ccModal.cc}
            channels={channels}
            capabilities={capabilities}
            onClose={() => setCcModal({open: false, capabilityCode: '', cc: null})}
            onSave={() => loadData(true)}
        />
      </>)}
    </div>
  );
};

export default Capabilities;
