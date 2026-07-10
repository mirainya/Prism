import React, { useEffect, useMemo, useState } from 'react';
import { Edit2, ChevronDown, ChevronRight, RefreshCw, Search, MessageSquare, Brain, Trash2, GripVertical, Layers } from 'lucide-react';
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Modal } from '../components/ui/Modal';
import {
  GwModel, GwAbility,
  fetchGwModels, fetchGwAbilities, upsertGwModelMeta, deleteGwModelMeta, deleteGwModel, reorderGwModels,
} from '../services/gatewayApi';
import { ThinkingConfig } from '../types';
import ThinkingConfigEditor from './capabilities/ThinkingConfigEditor';

const FEATURE_OPTIONS = ['tools', 'vision', 'json_mode', 'reasoning'];

// 元数据编辑弹窗: 显示名/最大tokens/特性/思考档(upsert gw_model_meta)
const MetaModal: React.FC<{
  model: GwModel | null;
  isOpen: boolean;
  onClose: () => void;
  onSaved: () => void;
}> = ({ model, isOpen, onClose, onSaved }) => {
  const [displayName, setDisplayName] = useState('');
  const [groupName, setGroupName] = useState('');
  const [maxTokens, setMaxTokens] = useState(0);
  const [features, setFeatures] = useState<string[]>([]);
  const [thinking, setThinking] = useState<ThinkingConfig | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (isOpen && model) {
      setDisplayName(model.display_name || model.model_name);
      setGroupName(model.group_name || '');
      setMaxTokens(model.max_tokens || 0);
      setFeatures(Array.isArray(model.features) ? model.features : []);
      setThinking(model.thinking_config && Object.keys(model.thinking_config).length > 0 ? model.thinking_config : null);
    }
  }, [isOpen, model]);

  if (!isOpen || !model) return null;

  const handleSave = async () => {
    setSaving(true);
    try {
      await upsertGwModelMeta(model.model_name, {
        model_name: model.model_name,
        display_name: displayName.trim() || model.model_name,
        group_name: groupName.trim(),
        max_tokens: maxTokens,
        features: features as any,
        thinking_config: thinking as any,
        status: 1,
      });
      onSaved();
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={true} onClose={onClose} title="编辑模型元数据" width="max-w-md">
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">模型 <code className="text-xs px-1.5 py-0.5 bg-[var(--primary-lighter)] rounded">{model.model_name}</code></label>
        </div>
        <div>
          <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">显示名</label>
          <input value={displayName} onChange={e => setDisplayName(e.target.value)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" />
        </div>
        <div>
          <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">分组 <span className="text-xs font-normal text-[var(--text-secondary)]">(留空则按源渠道自动分组)</span></label>
          <input value={groupName} onChange={e => setGroupName(e.target.value)} placeholder="如 主力 / 备用 / 便宜档" className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" />
        </div>
        <div>
          <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">最大 Tokens</label>
          <input type="number" value={maxTokens || ''} onChange={e => setMaxTokens(parseInt(e.target.value) || 0)} className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" placeholder="如 128000" />
        </div>
        <div>
          <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">能力特性</label>
          <div className="flex flex-wrap gap-3">
            {FEATURE_OPTIONS.map(f => (
              <label key={f} className="inline-flex items-center gap-1.5 text-sm text-[var(--text-primary)] cursor-pointer">
                <input type="checkbox" checked={features.includes(f)}
                  onChange={e => setFeatures(e.target.checked ? [...features, f] : features.filter(x => x !== f))}
                  className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded" />
                {f}
              </label>
            ))}
          </div>
        </div>
        <div>
          <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">思考模式</label>
          <ThinkingConfigEditor value={thinking} provider="" onChange={setThinking} />
        </div>
        <div className="flex justify-end gap-3 pt-4">
          <button onClick={onClose} className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200">取消</button>
          <button onClick={handleSave} disabled={saving} className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50">{saving ? '保存中...' : '保存'}</button>
        </div>
      </div>
    </Modal>
  );
};

// 可拖拽模型行外壳:useSortable 作用于外层行,render-prop 注入拖拽手柄到行头
const SortableModelRow: React.FC<{
  name: string;
  canDrag: boolean;
  children: (dragHandle: React.ReactNode) => React.ReactNode;
}> = ({ name, canDrag, children }) => {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: name, disabled: !canDrag });
  const style = { transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.4 : 1 };
  const dragHandle = canDrag ? (
    <span {...attributes} {...listeners} onClick={e => e.stopPropagation()}
      className="p-1 -ml-1 text-[var(--text-secondary)] hover:text-[var(--primary)] cursor-grab active:cursor-grabbing touch-none shrink-0"
      title="拖拽排序(组内)">
      <GripVertical size={16} />
    </span>
  ) : null;
  return (
    <div ref={setNodeRef} style={style} className="rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-sm overflow-hidden">
      {children(dragHandle)}
    </div>
  );
};

const GatewayModels: React.FC = () => {
  const [models, setModels] = useState<GwModel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [searchTerm, setSearchTerm] = useState('');

  // 展开时懒加载该模型的 abilities(只读视图,管理在网关渠道页)
  const [abilities, setAbilities] = useState<Record<string, GwAbility[]>>({});
  const [abLoading, setAbLoading] = useState<string | null>(null);

  // 元数据编辑弹窗
  const [metaModal, setMetaModal] = useState<{ open: boolean; model: GwModel | null }>({ open: false, model: null });

  const load = async () => {
    setIsLoading(true);
    try {
      setModels(await fetchGwModels());
    } finally {
      setIsLoading(false);
    }
  };
  useEffect(() => { load(); }, []);

  const loadAbilities = async (name: string) => {
    setAbLoading(name);
    try {
      const rows = await fetchGwAbilities({ model: name });
      setAbilities(prev => ({ ...prev, [name]: rows }));
    } finally {
      setAbLoading(null);
    }
  };

  const toggle = (name: string) => {
    if (expanded === name) { setExpanded(null); return; }
    setExpanded(name);
    if (!abilities[name]) loadAbilities(name);
  };

  const filtered = useMemo(() => {
    const kw = searchTerm.trim().toLowerCase();
    if (!kw) return models;
    return models.filter(m => m.model_name.toLowerCase().includes(kw) || (m.display_name || '').toLowerCase().includes(kw));
  }, [models, searchTerm]);

  const handleDeleteMeta = async (name: string) => {
    if (!confirm('清除该模型的元数据(显示名/思考档/特性)? 不影响路由,模型仍可用。')) return;
    await deleteGwModelMeta(name);
    load();
  };

  const handleDeleteModel = async (name: string) => {
    if (!confirm(`删除模型「${name}」？\n将移除该模型的所有路由能力(abilities)和元数据,模型从列表中消失,不可撤销。`)) return;
    await deleteGwModel(name);
    load();
  };

  // 分组:手动组名优先,否则按源渠道,再兜底「未分组」。组内已按 sort 排(后端 Order sort)。
  const groups = useMemo(() => {
    const map = new Map<string, GwModel[]>();
    filtered.forEach(m => {
      const key = (m.group_name || '').trim() || (m.source_channel || '').trim() || '未分组';
      const list = map.get(key) || [];
      list.push(m);
      map.set(key, list);
    });
    // 组内按 sort 升序排(吃乐观更新+后端 Order),再兜底 model_name;否则拖拽放手会弹回
    map.forEach(list => list.sort((a, b) => (a.sort || 0) - (b.sort || 0) || a.model_name.localeCompare(b.model_name)));
    return Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]));
  }, [filtered]);

  // 折叠的分组集合(默认全展开)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set());
  const toggleGroup = (key: string) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });
  };

  // 组内拖拽:搜索激活时禁拖(顺序会被过滤打乱)
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
  const canDrag = !searchTerm.trim();

  const handleDragEnd = (groupModels: GwModel[]) => async (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const oldIndex = groupModels.findIndex(m => m.model_name === active.id);
    const newIndex = groupModels.findIndex(m => m.model_name === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const reordered = arrayMove(groupModels, oldIndex, newIndex);
    // 乐观更新:按新次序重算组内 sort(升序),整体列表以组内新序替换
    const orderMap = new Map(reordered.map((m, i) => [m.model_name, i]));
    setModels(prev => {
      const updated = prev.map(m => orderMap.has(m.model_name) ? { ...m, sort: orderMap.get(m.model_name)! } : m);
      return updated;
    });
    try {
      await reorderGwModels(reordered.map(m => m.model_name));
    } catch {
      load(); // 失败回滚
    }
  };

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex flex-col gap-3 md:gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">
            对话模型
            <span className="ml-2 md:ml-3 text-xs md:text-base font-normal text-[var(--text-secondary)]">{models.length} 个可路由</span>
          </h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">
            仅展示 gw_abilities 里至少 1 个 Key 能跑的模型(与 /v2 路由同源)。此页只编辑元数据(显示名/思考档/特性),渠道与 Key 请去「网关渠道」管理。
          </p>
        </div>
        <button onClick={load} className="flex items-center gap-2 px-3 md:px-4 py-2 border border-[var(--border-soft)] rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface)] self-start">
          <RefreshCw size={16} className={isLoading ? 'animate-spin' : ''} /><span className="hidden md:inline">刷新</span>
        </button>
      </div>

      <div className="relative flex-1 md:max-w-md">
        <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" />
        <input type="text" value={searchTerm} onChange={e => setSearchTerm(e.target.value)}
          className="w-full pl-9 pr-4 py-2 border border-[var(--border-soft)] rounded-lg text-sm" placeholder="搜索模型..." />
      </div>

      {isLoading ? (
        <div className="animate-pulse space-y-4">{[1, 2, 3].map(i => <div key={i} className="bg-[var(--surface-card)] p-6 rounded-2xl border border-[var(--border-soft)] h-20" />)}</div>
      ) : filtered.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center text-[var(--text-secondary)]">
          <MessageSquare size={48} strokeWidth={1} className="text-gray-300" />
          <p className="mt-3 text-sm">{searchTerm.trim() ? '没有匹配的模型' : '暂无可路由模型,去「网关渠道」拉取模型后自动出现'}</p>
        </div>
      ) : (
        <div className="space-y-5">
          {groups.map(([groupKey, groupModels]) => {
            const isCollapsed = collapsedGroups.has(groupKey);
            return (
            <div key={groupKey} className="space-y-3">
              {/* 分组标题:可折叠 + 数量 */}
              <div className="flex items-center gap-2 cursor-pointer select-none" onClick={() => toggleGroup(groupKey)}>
                <span className="text-[var(--text-secondary)]">{isCollapsed ? <ChevronRight size={16} /> : <ChevronDown size={16} />}</span>
                <Layers size={15} className="text-[var(--primary)]" />
                <span className="font-bold text-sm text-[var(--text-primary)]">{groupKey}</span>
                <span className="text-xs px-2 py-0.5 rounded-full bg-[var(--primary-lighter)] text-[var(--text-secondary)]">{groupModels.length}</span>
              </div>
              {!isCollapsed && (
              <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd(groupModels)}>
                <SortableContext items={groupModels.map(m => m.model_name)} strategy={verticalListSortingStrategy}>
                  <div className="space-y-3">
                    {groupModels.map(m => {
                      const isExpanded = expanded === m.model_name;
                      const abs = abilities[m.model_name] || [];
                      const features: string[] = Array.isArray(m.features) ? m.features : [];
                      const hasThinking = m.thinking_config && Object.keys(m.thinking_config).length > 0;
                      return (
                        <SortableModelRow key={m.model_name} name={m.model_name} canDrag={canDrag}>
                          {(dragHandle) => (
                          <>
                            <div className="p-4 flex items-center justify-between cursor-pointer hover:bg-[var(--surface)]" onClick={() => toggle(m.model_name)}>
                              <div className="flex items-center gap-3 min-w-0 flex-1">
                                {dragHandle}
                                <span className="text-[var(--text-secondary)]">{isExpanded ? <ChevronDown size={18} /> : <ChevronRight size={18} />}</span>
                                <div className="p-2 bg-[var(--primary-lighter)] rounded-xl"><MessageSquare size={18} className="text-[var(--primary)]" /></div>
                                <div className="min-w-0 flex-1">
                                  <div className="flex items-center gap-2 flex-wrap">
                                    <span className="font-bold text-[var(--text-primary)]">{m.display_name || m.model_name}</span>
                                    <code className="text-xs px-2 py-0.5 bg-[var(--primary-lighter)] rounded text-[var(--text-secondary)]">{m.model_name}</code>
                                    {m.key_available > 0 ? (
                                      <span className="text-xs px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-600">{m.key_available} 可用 / {m.key_total} Key</span>
                                    ) : (
                                      <span className="text-xs px-2 py-0.5 rounded-full bg-amber-50 text-amber-700">无可用 Key(共 {m.key_total})</span>
                                    )}
                                    {hasThinking && <span className="text-xs px-2 py-0.5 rounded-full bg-violet-50 text-violet-600 inline-flex items-center gap-1"><Brain size={11} />思考档</span>}
                                    {features.map(f => <span key={f} className="text-[10px] px-1.5 py-0.5 rounded bg-sky-50 text-sky-600">{f}</span>)}
                                  </div>
                                </div>
                              </div>
                              <div className="flex items-center gap-2 shrink-0">
                                <button onClick={e => { e.stopPropagation(); setMetaModal({ open: true, model: m }); }} className="p-2 text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg" title="编辑元数据"><Edit2 size={16} /></button>
                                <button onClick={e => { e.stopPropagation(); handleDeleteModel(m.model_name); }} className="p-2 text-red-600 hover:bg-red-50 rounded-lg" title="删除模型"><Trash2 size={16} /></button>
                              </div>
                            </div>
                            {isExpanded && (
                              <div className="border-t border-[var(--border-soft)] bg-[var(--surface)]/70 p-4">
                                <h4 className="text-sm font-bold text-[var(--text-primary)] mb-3">路由能力 <span className="font-normal text-[var(--text-secondary)]">(只读,管理请去「网关渠道」)</span></h4>
                                {abLoading === m.model_name ? (
                                  <p className="text-sm text-[var(--text-secondary)] text-center py-6">加载中...</p>
                                ) : abs.length === 0 ? (
                                  <p className="text-sm text-[var(--text-secondary)] text-center py-6">暂无路由能力</p>
                                ) : (
                                  <div className="space-y-2">
                                    {abs.map(ab => (
                                      <div key={ab.id} className="bg-[var(--surface-card)] p-3 rounded-xl border border-[var(--border-soft)] flex items-center justify-between gap-3">
                                        <div className="flex items-center gap-2 flex-wrap min-w-0">
                                          <span className="text-sm font-medium text-[var(--text-primary)]">{ab.channel_name}</span>
                                          <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-50 text-blue-600">{ab.protocol}</span>
                                          <span className="text-xs text-[var(--text-secondary)]">/ {ab.key_name || `key#${ab.key_id}`}</span>
                                          {ab.vendor_model !== ab.model_name && <code className="text-xs px-1.5 py-0.5 bg-gray-100 rounded">{ab.vendor_model}</code>}
                                          <span className={`text-xs px-1.5 py-0.5 rounded-full ${ab.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>{ab.status === 1 ? '启用' : '禁用'}</span>
                                        </div>
                                        <div className="flex gap-3 text-xs text-[var(--text-secondary)] shrink-0">
                                          <span>P{ab.priority}</span>
                                          <span>入¥{ab.input_price}/出¥{ab.output_price}</span>
                                        </div>
                                      </div>
                                    ))}
                                  </div>
                                )}
                              </div>
                            )}
                          </>
                          )}
                        </SortableModelRow>
                      );
                    })}
                  </div>
                </SortableContext>
              </DndContext>
              )}
            </div>
            );
          })}
        </div>
      )}

      <MetaModal model={metaModal.model} isOpen={metaModal.open}
        onClose={() => setMetaModal({ open: false, model: null })}
        onSaved={() => { setMetaModal({ open: false, model: null }); load(); }} />
    </div>
  );
};

export default GatewayModels;
