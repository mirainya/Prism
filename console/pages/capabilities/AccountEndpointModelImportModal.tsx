import React, { useEffect, useState } from 'react';
import { AlertCircle, CheckSquare, Download, RefreshCw, Square } from 'lucide-react';
import { Modal } from '../../components/ui';
import {
  AccountDiscoveredModel,
  discoverChannelAccountModels,
  importChannelAccountModels,
} from '../../services/channelApi';

type Draft = { modelCode: string; name: string; operations: Set<string> };
const operationLabels: Record<string, string> = {
  'images.generate': '生成',
  'images.edit': '编辑',
};

const AccountEndpointModelImportModal: React.FC<{
  accountId: string | null;
  accountName?: string;
  onClose: () => void;
  onImported: () => void;
}> = ({ accountId, accountName, onClose, onImported }) => {
  const [models, setModels] = useState<AccountDiscoveredModel[]>([]);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [operations, setOperations] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState('');

  const load = async () => {
    if (!accountId) return;
    setLoading(true);
    setError('');
    try {
      const response = await discoverChannelAccountModels(accountId);
      const available = response.operations || [];
      setOperations(available);
      setModels(response.models || []);
      setSelected(new Set((response.models || []).filter(model => !model.imported).map(model => model.id)));
      setDrafts(Object.fromEntries((response.models || []).map(model => [model.id, {
        modelCode: model.model_code || model.id,
        name: model.id,
        operations: new Set(available),
      }])));
    } catch (requestError: any) {
      setModels([]);
      setError(requestError?.message || '读取模型失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (accountId) {
      setResult('');
      load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId]);

  const toggleModel = (id: string) => setSelected(previous => {
    const next = new Set(previous);
    next.has(id) ? next.delete(id) : next.add(id);
    return next;
  });
  const toggleOperation = (id: string, operation: string) => setDrafts(previous => {
    const draft = previous[id];
    if (!draft) return previous;
    const next = new Set(draft.operations);
    next.has(operation) ? next.delete(operation) : next.add(operation);
    return { ...previous, [id]: { ...draft, operations: next } };
  });
  const updateDraft = (id: string, field: 'modelCode' | 'name', value: string) =>
    setDrafts(previous => ({ ...previous, [id]: { ...previous[id], [field]: value } }));

  const handleImport = async () => {
    if (!accountId) return;
    const items = models.filter(model => selected.has(model.id)).map(model => {
      const draft = drafts[model.id];
      return {
        id: model.id,
        model_code: draft?.modelCode.trim() || model.model_code || model.id,
        name: draft?.name.trim() || model.id,
        operations: Array.from(draft?.operations || []),
      };
    }).filter(item => item.operations.length > 0);
    if (items.length === 0) {
      setError('请选择模型和至少一个操作');
      return;
    }
    setImporting(true);
    setError('');
    try {
      const response = await importChannelAccountModels(accountId, items);
      setResult(`已新增 ${response.models_created} 个模型、${response.endpoints_created} 个端点，绑定 ${response.bindings_added} 个 Key`);
      await load();
      onImported();
    } catch (requestError: any) {
      setError(requestError?.message || '导入失败');
    } finally {
      setImporting(false);
    }
  };

  return (
    <Modal open={Boolean(accountId)} onClose={onClose} title={`发现上游模型 · ${accountName || accountId || ''}`} width="max-w-4xl">
      <div className="space-y-3">
        <div className="flex items-center justify-between text-xs text-[var(--text-secondary)]">
          <span>按当前 Key 请求渠道发现接口</span>
          <button type="button" onClick={load} disabled={loading} className="inline-flex items-center gap-1.5 text-[var(--primary)] disabled:opacity-50">
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />刷新
          </button>
        </div>
        {error && <div className="flex items-start gap-2 rounded-lg bg-red-50 p-2 text-xs text-red-700"><AlertCircle size={14} />{error}</div>}
        {result && <div className="rounded-lg bg-green-50 p-2 text-xs text-green-700">{result}</div>}
        {loading ? <div className="py-12 text-center text-sm text-[var(--text-secondary)]"><RefreshCw size={20} className="mx-auto mb-2 animate-spin" />读取中...</div> : models.length === 0 ? <div className="py-12 text-center text-sm text-[var(--text-secondary)]">未读取到模型</div> : (
          <div className="max-h-[55vh] space-y-2 overflow-y-auto pr-1">
            {models.map(model => {
              const checked = selected.has(model.id);
              const draft = drafts[model.id] || { modelCode: model.model_code, name: model.id, operations: new Set(operations) };
              return <div key={model.id} className={`rounded-lg border p-2 ${checked ? 'border-[var(--primary)]/40 bg-[var(--primary-lighter)]/30' : 'border-[var(--border-soft)]'}`}>
                <div className="flex items-center gap-2">
                  <button type="button" onClick={() => toggleModel(model.id)} className="text-[var(--primary)]">{checked ? <CheckSquare size={16} /> : <Square size={16} />}</button>
                  <span className="min-w-0 flex-1 truncate font-mono text-sm" title={model.id}>{model.id}</span>
                  {model.imported && <span className="rounded bg-sky-100 px-1.5 py-0.5 text-[10px] text-sky-700">已有绑定</span>}
                </div>
                {checked && <div className="grid gap-2 px-7 pb-1 pt-2 md:grid-cols-[1fr_1fr_1.4fr]">
                  <label className="text-[10px] text-[var(--text-secondary)]">模型编码<input value={draft.modelCode} onChange={event => updateDraft(model.id, 'modelCode', event.target.value)} className="mt-0.5 w-full rounded border border-[var(--border-soft)] px-2 py-1 text-xs font-mono" /></label>
                  <label className="text-[10px] text-[var(--text-secondary)]">显示名称<input value={draft.name} onChange={event => updateDraft(model.id, 'name', event.target.value)} className="mt-0.5 w-full rounded border border-[var(--border-soft)] px-2 py-1 text-xs" /></label>
                  <div className="flex flex-wrap items-center gap-3 text-xs text-[var(--text-primary)]"><span>操作</span>{operations.map(operation => <label key={operation} className="inline-flex items-center gap-1"><input type="checkbox" checked={draft.operations.has(operation)} onChange={() => toggleOperation(model.id, operation)} />{operationLabels[operation] || operation}</label>)}</div>
                </div>}
              </div>;
            })}
          </div>
        )}
        <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-3"><button type="button" onClick={onClose} className="px-4 py-2 text-sm text-[var(--text-secondary)]">关闭</button><button type="button" onClick={handleImport} disabled={loading || importing || selected.size === 0} className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--primary)] px-4 py-2 text-sm font-bold text-white disabled:opacity-50"><Download size={15} />导入选中 ({selected.size})</button></div>
      </div>
    </Modal>
  );
};

export default AccountEndpointModelImportModal;
