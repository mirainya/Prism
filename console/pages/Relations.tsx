import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Link2, RefreshCw, Search } from 'lucide-react';
import {
  UnifiedCredential,
  UnifiedRelationLink,
  fetchUnifiedCredentialModels,
  fetchUnifiedCredentials,
  fetchUnifiedModelCredentials,
} from '../services/unifiedGatewayApi';
import { Badge, Button, Input, SegmentedControl, Select, Table, type TableColumn } from '../components/ui';
import { PageHeader } from '../components/shell';
import { describeRelationBlock } from './unified_gateway/presentation';

// Key 不指向模型。真实链路是
//   credential → pool ← offering → route → sku → model_operation → catalog_model → model_name
// 没有任何一个接口走得通，所以"这个 Key 能服务哪些模型"此前完全无法回答。
//
// 更要紧的是反过来那一问：配好了却不在服务。选路用的是 INNER JOIN，任何一环
// 不满足，这条链路就在结果里凭空消失，连选路自己都不知道少了什么。后端那条
// 关系查询把同样的条件改成 LEFT JOIN，于是消失的链路仍然会出现在这里，带着
// blocked_by 说明它卡在哪一环。

const CODE = 'font-mono text-xs';
const SUB = 'text-xs text-[var(--text-secondary)]';
const CREDENTIAL_PAGE_SIZE = 100;

// 后端给的是稳定的原因码，翻译共用 presentation 里那份——运维台抽屉也要显示同一批码。
const describeBlocked = describeRelationBlock;

const formatRemaining = (seconds: number) => {
  if (seconds <= 0) return '';
  if (seconds < 60) return `${seconds} 秒后恢复`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟后恢复`;
  return `${Math.floor(seconds / 3600)} 小时后恢复`;
};

const buildColumns = (direction: 'byKey' | 'byModel'): TableColumn<UnifiedRelationLink>[] => [
  {
    header: direction === 'byKey' ? '模型' : 'API Key',
    wrap: true,
    render: row => direction === 'byKey'
      ? <><span className="font-medium">{row.api_name}</span><span className={SUB}>{row.display_name || '-'} · {row.visibility}</span></>
      : <><span className={`${CODE} font-medium`}>{row.credential_code}</span><span className={SUB}>#{row.credential_id} · {row.credential_status}</span></>,
  },
  {
    header: 'SKU / 上游模型',
    wrap: true,
    render: row => <><span className={CODE}>{row.sku_code}</span><span className={SUB}>{row.vendor_model || '-'} · {row.delivery_mode}</span></>,
  },
  {
    header: direction === 'byKey' ? '通道 / Transport' : '通道 / 密钥池',
    wrap: true,
    render: row => <>
      <span>{row.channel_name || `#${row.channel_id}`}</span>
      <span className={SUB}>{direction === 'byKey' ? row.transport_code : `${row.pool_name || row.pool_code} · ${row.transport_code}`}</span>
    </>,
  },
  {
    header: '优先级 / 权重',
    wrap: true,
    render: row => <><span>{row.priority}</span><span className={SUB}>路由 {row.route_weight} · 凭据 {row.credential_weight}</span></>,
  },
  {
    header: '并发上限',
    wrap: true,
    render: row => <>
      <span>请求 {row.request_limit ?? '不限'}</span>
      <span className={SUB}>任务 {row.task_limit ?? '不限'}</span>
    </>,
  },
  {
    header: '状态',
    wrap: true,
    render: row => row.serving
      ? <Badge variant="success">在服务</Badge>
      : <div className="flex flex-wrap gap-1">
        {(row.blocked_by || []).map(code => (
          <Badge key={code} variant={code === 'circuit_broken' ? 'error' : 'warning'}>
            {describeBlocked(code)}
            {code === 'circuit_broken' && row.breaker_seconds > 0 ? `，${formatRemaining(row.breaker_seconds)}` : ''}
          </Badge>
        ))}
      </div>,
  },
];

const Relations: React.FC = () => {
  const [direction, setDirection] = useState<'byKey' | 'byModel'>('byKey');
  const [credentials, setCredentials] = useState<UnifiedCredential[]>([]);
  const [credentialId, setCredentialId] = useState<number | null>(null);
  const [modelInput, setModelInput] = useState('');
  const [modelName, setModelName] = useState('');
  const [rows, setRows] = useState<UnifiedRelationLink[]>([]);
  const [releaseId, setReleaseId] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [loaded, setLoaded] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    // page_size 后端上限是 100（unified_gateway.go:190），超了直接 400。
    // 凭据要全列出来选择器才有用，所以翻页取完。
    const loadAll = async () => {
      const first = await fetchUnifiedCredentials(1, CREDENTIAL_PAGE_SIZE);
      const items = [...(first.items || [])];
      const pages = Math.ceil((first.total || 0) / CREDENTIAL_PAGE_SIZE);
      for (let page = 2; page <= pages; page += 1) {
        const next = await fetchUnifiedCredentials(page, CREDENTIAL_PAGE_SIZE);
        items.push(...(next.items || []));
      }
      return items;
    };
    loadAll()
      .then(items => {
        setCredentials(items);
        setCredentialId(current => current ?? items[0]?.id ?? null);
      })
      .catch(err => setError(err instanceof Error ? err.message : '加载凭据列表失败'));
  }, []);

  const load = useCallback(async () => {
    if (direction === 'byKey' ? !credentialId : !modelName) return;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setError('');
    try {
      const data = direction === 'byKey'
        ? await fetchUnifiedCredentialModels(credentialId as number, controller.signal)
        : await fetchUnifiedModelCredentials(modelName, controller.signal);
      setRows(data.items || []);
      setReleaseId(data.active_release_id);
      setLoaded(true);
    } catch (err) {
      if (controller.signal.aborted) return;
      setError(err instanceof Error ? err.message : '加载关系失败');
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [direction, credentialId, modelName]);

  useEffect(() => {
    load();
    return () => abortRef.current?.abort();
  }, [load]);

  const servingCount = rows.filter(row => row.serving).length;

  return (
    <div className="space-y-6">
      <PageHeader
        icon={Link2}
        title="Key ↔ 模型"
        meta={releaseId
          ? <>按当前生效目录 #{releaseId} 计算；共 {rows.length} 条链路，{servingCount} 条在服务</>
          : '当前没有生效中的目录版本，任何链路都不会服务'}
        actions={<Button variant="secondary" onClick={load} disabled={loading}><RefreshCw size={16} className={loading ? 'animate-spin' : ''} />刷新</Button>}
      />

      <div className="flex flex-wrap items-center gap-3">
        <SegmentedControl
          value={direction}
          ariaLabel="关系查询方向"
          onChange={value => { setDirection(value as 'byKey' | 'byModel'); setRows([]); setLoaded(false); }}
          options={[
            { value: 'byKey', label: '这个 Key 服务哪些模型' },
            { value: 'byModel', label: '这个模型由哪些 Key 承载' },
          ]}
        />
        {direction === 'byKey' ? (
          <Select
            className="min-w-[240px]"
            value={credentialId ? String(credentialId) : ''}
            onChange={value => setCredentialId(Number(value) || null)}
            placeholder="选择一个 API Key"
            options={credentials.map(credential => ({
              value: String(credential.id),
              label: `${credential.credential_code}（#${credential.id}${credential.pool_name ? ` · ${credential.pool_name}` : ''}）`,
            }))}
          />
        ) : (
          <form
            className="flex items-center gap-2"
            onSubmit={event => { event.preventDefault(); setModelName(modelInput.trim()); }}
          >
            <Input value={modelInput} onChange={event => setModelInput(event.target.value)} placeholder="对外模型名，如 gpt-4o" />
            <Button type="submit" variant="secondary"><Search size={16} />查询</Button>
          </form>
        )}
      </div>

      {error && <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-600">{error}</div>}

      <div className="rounded-xl border border-[var(--border-soft)] bg-white/60 overflow-hidden">
        <Table
          columns={buildColumns(direction)}
          rows={rows}
          rowKey={row => `${row.route_id}-${row.credential_id}`}
          minWidth="960px"
          busy={loading}
          rowClassName={row => (row.serving ? '' : 'bg-amber-50/40')}
          empty={loading
            ? '正在读取'
            : !loaded
              ? (direction === 'byKey' ? '选择一个 API Key 开始' : '填入对外模型名后查询')
              : direction === 'byKey'
                ? '这个 Key 的密钥池上没有挂任何 Offering，或生效目录里没有指向它的 Route。'
                : '生效目录里没有这个模型名。注意这里要填对外 api_name，不是上游 vendor_model。'}
        />
      </div>

      {rows.length > 0 && servingCount === 0 && (
        <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700">
          链路都在，但没有一条会被选中。上表 blocked_by 列出了卡住的环节——最常见的是缺有效商业校验，
          它是 INNER JOIN，缺了这条 Offering 永远进不了候选。
        </div>
      )}
    </div>
  );
};

export default Relations;
