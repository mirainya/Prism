import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  Braces,
  CheckCircle2,
  ChevronDown,
  Clock3,
  Database,
  KeyRound,
  Layers3,
  LoaderCircle,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Search,
  Save,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  X,
} from 'lucide-react';
import { PageHeader } from '../components/shell';
import { Badge, Button, Drawer, Modal, Pagination, Select, useAppDialog } from '../components/ui';
import {
  fetchUnifiedCalls,
  fetchUnifiedCallDetail,
  fetchUnifiedCatalog,
  fetchUnifiedCatalogRelease,
  fetchUnifiedCatalogModelEntries,
  fetchUnifiedCatalogProducts,
  fetchUnifiedCatalogRates,
  fetchUnifiedGatewayOverview,
  fetchUnifiedRouteStates,
  changeUnifiedProduct,
  changeUnifiedSellRate,
  setUnifiedOfferingRuntimeState,
  type UnifiedCall,
  type UnifiedCatalogProduct,
  type UnifiedCatalogRelease,
  type UnifiedCatalogRate,
  type UnifiedCatalogSKU,
  type UnifiedCredential,
  type UnifiedCatalogModelEntry,
  type UnifiedCatalogModelStatus,
  type UnifiedCatalogModelType,
  type UnifiedCatalogModelSelection,
  fetchUnifiedModelCredentials,
  fetchUnifiedChannelRelations,
  type UnifiedRelationLink,
  type UnifiedGatewayOverview,
  type UnifiedGatewayPage,
} from '../services/unifiedGatewayApi';
import {
  fetchUnifiedChannels,
  fetchUnifiedPools,
  createUnifiedChannel,
  createUnifiedPool,
  updateUnifiedPool,
  type UnifiedChannel,
  type UnifiedPool,
} from '../services/unifiedChannelApi';
import { fetchManagedCredentials } from '../services/unifiedCredentialApi';
import { ErrorNotice, errorMessage } from './unified_gateway/Feedback';
import { CredentialDialog } from './unified_gateway/CredentialDialog';
import { describeRelationBlock } from './unified_gateway/presentation';
import {
  formatCredentialDisplayName,
  formatOpsDate,
  formatRateNumber,
  formatSKUDisplayName,
  formatSKUServiceSummary,
  formatSKUServiceTiers,
  opsStatusLabel,
  opsStatusVariant,
  rateChargeEventLabel,
  rateComponentLabel,
  rateCurrencyLabel,
  rateParentLabel,
  ratePricingModeLabel,
  rateQuantitySourceLabel,
  rateUnitLabel,
  selectedEntityTitle,
  shortDigest,
  type OpsConsoleTab,
  type OpsSelectedEntity,
} from './ops_console/presentation';
import { CostRateEditor, RoutingEditor, type CatalogChangeContext } from './ops_console/CatalogChangeForms';
import { AddUpstreamModelDialog } from './ops_console/AddUpstreamModelDialog';
import { AllowedHostsDialog } from './ops_console/AllowedHostsDialog';
import {
  filterGatewayModels,
  gatewayProductKey,
  gatewayRelationProductKey,
  groupGatewayModels,
  relationBelongsToProduct,
} from './ops_console/gatewayView';

// CodeMirror 被 vite.config.ts 单独切成 chunk 且排除出 modulepreload，静态 import 会让它
// 跟着运维台一起下载。映射编辑是低频动作，等真打开再取。
const JsonEditor = React.lazy(() => import('../components/ui/JsonEditor'));

type OpsData =
  | { tab: 'models'; page: UnifiedGatewayPage<UnifiedCatalogModelEntry>; release: UnifiedCatalogRelease | null }
  | { tab: 'upstream'; page: UnifiedGatewayPage<UnifiedChannel>; release: UnifiedCatalogRelease | null }
  | { tab: 'calls'; page: UnifiedGatewayPage<UnifiedCall> };

interface ModelDetails {
  skus: UnifiedCatalogSKU[];
  products: UnifiedCatalogProduct[];
  total: number;
}

type ModelType = UnifiedCatalogModelType;

interface CallDetails {
  attemptTotal: number;
  requestLogsAvailable: boolean;
}

const tabs: { key: OpsConsoleTab; label: string; icon: React.ElementType }[] = [
  { key: 'upstream', label: '网关管理', icon: Server },
  { key: 'models', label: '对外模型', icon: Layers3 },
  { key: 'calls', label: '调用记录', icon: Activity },
];

const statusFilters = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '已启用' },
  { value: 'inactive', label: '未启用' },
  { value: 'disabled', label: '已停用' },
  { value: 'pending', label: '待处理' },
  { value: 'failed', label: '失败' },
] as const;

const modelStatusFilters = statusFilters.filter((option) => (
  option.value === '' || option.value === 'active' || option.value === 'inactive' || option.value === 'disabled'
));

const upstreamStatusFilters = statusFilters.filter((option) => (
  option.value === '' || option.value === 'active' || option.value === 'disabled'
));

const modelTypeFilters: { value: '' | ModelType; label: string }[] = [
  { value: '', label: '全部类型' },
  { value: 'llm', label: 'LLM' },
  { value: 'image', label: '图片' },
  { value: 'video', label: '视频' },
  { value: 'other', label: '其他' },
];

const modelTypeLabel: Record<ModelType, string> = {
  llm: 'LLM',
  image: '图片',
  video: '视频',
  other: '其他',
};

const gatewayTypeFilters: { value: '' | ModelType; label: string }[] = [
  { value: '', label: '全部网关' },
  { value: 'llm', label: 'LLM' },
  { value: 'image', label: '图片' },
  { value: 'video', label: '视频' },
  { value: 'other', label: '其他' },
];

const configurationInputClass = 'mt-1 w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-3 py-2 text-sm font-normal';

const readinessReasonLabel: Record<string, string> = {
  channels_missing: '还没有配置渠道',
  models_missing: '还没有配置模型',
  credentials_missing: '还没有配置 API Key',
  catalog_missing: '还没有模型配置',
  routes_missing: '还没有配置可用入口',
  sell_rates_missing: '售价配置还不完整',
  cost_rates_missing: '成本配置还不完整',
  currency_missing: '货币配置还不完整',
  catalog_inactive: '模型配置尚未启用',
  deployment_inactive: '当前服务实例尚未就绪',
  legacy_data_present: '旧配置还未完成清理',
  runtime_not_ready: '当前实例还不能接收模型请求',
};

const modelSKUs = (entry: UnifiedCatalogModelEntry): UnifiedCatalogSKU[] => Array.isArray(entry.skus) ? entry.skus : [];
const modelProducts = (entry: UnifiedCatalogModelEntry): UnifiedCatalogProduct[] => Array.isArray(entry.products) ? entry.products : [];

const uniqueValues = (values: string[]) => [...new Set(values.map((value) => value.trim()).filter(Boolean))];

const selectedEntityKey = (entity: OpsSelectedEntity | null) => {
  if (!entity) return '';
  if (entity.kind === 'model') return `model:${entity.item.id}:${entity.item.model_entry.id}`;
  return `${entity.kind}:${entity.item.id}`;
};

const collectGatewayPages = async <T,>(load: (page: number) => Promise<UnifiedGatewayPage<T>>) => {
  const items: T[] = [];
  let page = 1;
  while (true) {
    const result = await load(page);
    items.push(...result.items);
    if (items.length >= result.total || result.items.length === 0) return items;
    page += 1;
  }
};

const productGatewayType = (product: UnifiedCatalogProduct): ModelType => {
  if (product.model_type && modelTypeLabel[product.model_type]) return product.model_type;
  const signature = [product.adapter_code, product.protocol, product.transport_code, product.product_code]
    .map((value) => String(value || '').toLowerCase())
    .join(' ');
  if (/(^|[_.\s-])(video|seedance)([_.\s-]|$)/.test(signature)) return 'video';
  if (/(^|[_.\s-])(image|images)([_.\s-]|$)/.test(signature)) return 'image';
  if (/(openai|anthropic|gemini|google|chat|responses|messages|completion)/.test(signature)) return 'llm';
  return 'other';
};

const productsFromRelations = (relations: UnifiedRelationLink[]): UnifiedCatalogProduct[] => {
  const grouped = new Map<string, { product: UnifiedCatalogProduct; routeIDs: Set<number>; credentialIDs: Set<number> }>();
  relations.forEach((relation) => {
    const key = gatewayRelationProductKey(relation);
    let current = grouped.get(key);
    if (!current) {
      current = {
        product: {
          id: relation.product_id || 0,
          product_code: relation.product_code,
          vendor_model: relation.vendor_model,
          channel_id: relation.channel_id,
          channel_name: relation.channel_name,
          product_transport_id: relation.product_transport_id || 0,
          channel_transport_id: relation.channel_transport_id || 0,
          transport_code: relation.transport_code,
          base_url: relation.base_url || '',
          protocol: relation.protocol || '',
          request_method: relation.request_method || 'POST',
          request_path: relation.request_path || '',
          task_scope: relation.task_scope || '',
          cancel_mode: '',
          source_url_policy: '',
          adapter_code: relation.adapter_code || '',
          adapter_version: relation.adapter_version || 1,
          offering_id: relation.offering_id,
          offering_state: ['active', 'draining', 'disabled'].includes(relation.offering_state)
            ? relation.offering_state as UnifiedCatalogProduct['offering_state']
            : undefined,
          offering_state_version: relation.offering_state_version,
          credential_pool_id: relation.credential_pool_id,
          pool_code: relation.pool_code,
          pool_name: relation.pool_name,
          cost_plan_id: 0,
          cost_plan_code: '',
          route_count: 0,
          cost_rate_count: 0,
          commercial_state: relation.commercial_valid ? 'valid' : 'invalid',
          entitled_credential_count: 0,
          capability_constraints: relation.capability_constraints ?? {},
          model_type: relation.model_type,
        },
        routeIDs: new Set<number>(),
        credentialIDs: new Set<number>(),
      };
      grouped.set(key, current);
    }
    current.routeIDs.add(relation.route_id);
    current.credentialIDs.add(relation.credential_id);
    current.product.route_count = current.routeIDs.size;
    current.product.entitled_credential_count = current.credentialIDs.size;
  });
  return [...grouped.values()].map(({ product }) => product);
};

const mergeChannelProducts = (catalogProducts: UnifiedCatalogProduct[], relations: UnifiedRelationLink[]) => {
  const relatedProducts = productsFromRelations(relations);
  const byTransport = new Map<string, UnifiedCatalogProduct>();
  relatedProducts.forEach((product) => {
    const key = gatewayProductKey(product);
    byTransport.set(key, product);
  });
  catalogProducts.forEach((product) => {
    const key = gatewayProductKey(product);
    const related = byTransport.get(key);
    byTransport.set(key, { ...related, ...product, model_type: related?.model_type || product.model_type });
  });
  return [...byTransport.values()];
};

const formatMappingJSON = (value: unknown) => {
  if (value === null || value === undefined || value === '') return '{}';
  try {
    const parsed = typeof value === 'string' ? JSON.parse(value) : value;
    return JSON.stringify(parsed, null, 2);
  } catch {
    return String(value);
  }
};

const mappingSections = (value: unknown) => {
  const text = formatMappingJSON(value);
  try {
    const parsed = JSON.parse(text) as Record<string, unknown>;
    return Object.keys(parsed).filter((key) => ['request', 'response', 'poll', 'callback', 'submit', 'query', 'cancel'].some((name) => key.toLowerCase().includes(name)));
  } catch {
    return [];
  }
};

const OpsMetricBar: React.FC<{ items: { label: string; value: React.ReactNode; icon: React.ElementType }[] }> = ({ items }) => (
  <section className="ops-metric-bar" aria-label="网关资源概览">
    {items.map(({ label, value, icon: Icon }) => (
      <div key={label} className="ops-metric-item">
        <span className="ops-metric-icon"><Icon size={15} /></span>
        <span className="ops-metric-label">{label}</span>
        <strong className="ops-metric-value">{value}</strong>
      </div>
    ))}
  </section>
);

const OpsConsole: React.FC = () => {
  const [tab, setTab] = useState<OpsConsoleTab>('upstream');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [revision, setRevision] = useState(0);
  const [overview, setOverview] = useState<UnifiedGatewayOverview | null>(null);
  const [overviewLoading, setOverviewLoading] = useState(true);
  const [overviewError, setOverviewError] = useState('');
  const [brokenModels, setBrokenModels] = useState<Set<string>>(new Set());
  const [data, setData] = useState<OpsData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<OpsSelectedEntity | null>(null);
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [modelTypeFilter, setModelTypeFilter] = useState<'' | ModelType>('');
  const [gatewayTypeFilter, setGatewayTypeFilter] = useState<'' | ModelType>('');
  const [modelDetails, setModelDetails] = useState<ModelDetails | null>(null);
  const [pools, setPools] = useState<UnifiedPool[]>([]);
  const [upstreamProducts, setUpstreamProducts] = useState<UnifiedCatalogProduct[]>([]);
  const [upstreamRelations, setUpstreamRelations] = useState<UnifiedRelationLink[]>([]);
  const [callDetails, setCallDetails] = useState<CallDetails | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [creatingChannel, setCreatingChannel] = useState(false);
  const detailEntityKeyRef = useRef('');

  const reload = () => {
    setOverviewLoading(true);
    setRevision((value) => value + 1);
  };
  const reloadModel = reload;

  useEffect(() => {
    const controller = new AbortController();
    setOverviewLoading(true);
    setOverviewError('');
    fetchUnifiedGatewayOverview(controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setOverview(value);
      })
      .catch((requestError: unknown) => {
        if (!controller.signal.aborted) setOverviewError(errorMessage(requestError));
      })
      .finally(() => {
        if (!controller.signal.aborted) setOverviewLoading(false);
      });
    return () => controller.abort();
  }, [revision]);

  // 熔断行按 model_name（对外路由键）记账，列表这一层只需要回答「这个模型现在有没有线路被跳过」。
  // 读失败就不标记——它是附加信息，不该把整页模型列表打成错误。
  useEffect(() => {
    const controller = new AbortController();
    if (tab !== 'models') {
      setBrokenModels(new Set());
      return () => controller.abort();
    }
    fetchUnifiedRouteStates(1, 100, {}, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setBrokenModels(new Set(result.items.filter((state) => state.active).map((state) => state.model_name)));
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, [tab, revision]);

  useEffect(() => {
    if (overviewLoading) return;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    const load = async (): Promise<OpsData> => {
      if (tab === 'models') {
        let release: UnifiedCatalogRelease | null = null;
        if (overview?.runtime.active_release_id) {
          release = await fetchUnifiedCatalogRelease(overview.runtime.active_release_id, controller.signal);
        } else {
          const releases = await fetchUnifiedCatalog(1, 1, controller.signal);
          release = releases.items[0] || null;
        }
        if (!release) {
          return { tab, release: null, page: { items: [], page, page_size: pageSize, total: 0 } };
        }
        const modelStatus: UnifiedCatalogModelStatus | '' = statusFilter === 'active' || statusFilter === 'inactive' || statusFilter === 'disabled'
          ? statusFilter
          : '';
        return {
          tab,
          release,
          page: await fetchUnifiedCatalogModelEntries(release.id, page, pageSize, {
            q: search,
            type: modelTypeFilter,
            status: modelStatus,
          }, controller.signal),
        };
      }
      if (tab === 'upstream') {
        const releaseID = overview?.runtime.active_release_id;
        const release = releaseID ? await fetchUnifiedCatalogRelease(releaseID, controller.signal) : null;
        return {
          tab,
          page: await fetchUnifiedChannels(page, pageSize, search, statusFilter, controller.signal, gatewayTypeFilter),
          release,
        };
      }
      return { tab, page: await fetchUnifiedCalls(page, pageSize, controller.signal) };
    };
    load()
      .then((value) => {
        if (controller.signal.aborted) return;
        const lastPage = Math.max(1, Math.ceil(value.page.total / value.page.page_size));
        if (page > lastPage) {
          setPage(lastPage);
          return;
        }
        setData(value);
        setSelected((current) => {
          // Keep the drawer bound to the refreshed model record.
          if (value.tab === 'models' && current?.kind === 'model' && value.release) {
            const modelItems = value.page.items as UnifiedCatalogModelEntry[];
            const model = modelItems.find((item) => (
              String(item.id) === String(current.item.model_entry.id)
              || item.model_code === current.item.model_entry.model_code
            ));
            return model ? { kind: 'model', item: { ...value.release, model_entry: model } } : null;
          }
          if (value.tab === 'upstream' && current?.kind === 'upstream') {
            const channel = (value.page.items as UnifiedChannel[]).find((item) => item.id === current.item.id);
            return channel ? { kind: 'upstream', item: channel } : null;
          }
          // The detail view is an on-demand drawer. Do not open it implicitly
          // when the list first loads; the operator chooses the record to inspect.
          return current;
        });
      })
      .catch((requestError: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(requestError));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [page, pageSize, revision, tab, overviewLoading, overview?.runtime.active_release_id, search, statusFilter, modelTypeFilter, gatewayTypeFilter]);

  const selectedKey = selectedEntityKey(selected);
  useEffect(() => {
    const entity = selected;
    const controller = new AbortController();
    const entityChanged = detailEntityKeyRef.current !== selectedKey;
    detailEntityKeyRef.current = selectedKey;
    setDetailError('');
    if (entityChanged) {
      setModelDetails(null);
      setPools([]);
      setUpstreamProducts([]);
      setUpstreamRelations([]);
      setCallDetails(null);
    }
    if (!entity) {
      setDetailLoading(false);
      return () => controller.abort();
    }
    if (overviewLoading) {
      setDetailLoading(false);
      return () => controller.abort();
    }
    setDetailLoading(true);
    const load = async () => {
      if (entity.kind === 'model') {
        const entry = entity.item.model_entry;
        const baseSKUs = Array.isArray(entry.skus) ? entry.skus : [];
        const baseProducts = Array.isArray(entry.products) ? entry.products : [];
        let resolvedEntry = { ...entry, skus: baseSKUs, products: baseProducts };
        try {
          const modelPage = await fetchUnifiedCatalogModelEntries(entity.item.id, 1, 100, { q: entry.model_code }, controller.signal);
          const match = modelPage.items.find(item => String(item.id) === String(entry.id) || item.model_code === entry.model_code);
          if (match) {
            const skus = Array.isArray(match.skus) ? match.skus : [];
            const products = Array.isArray(match.products) ? match.products : [];
            resolvedEntry = {
              ...entry,
              ...match,
              skus: skus.length > 0 || baseSKUs.length === 0 ? skus : baseSKUs,
              products: products.length > 0 || baseProducts.length === 0 ? products : baseProducts,
            };
          }
        } catch {
          // The compact model row remains a valid fallback when the detail request fails.
        }
        if (!controller.signal.aborted) setModelDetails({ skus: resolvedEntry.skus, products: resolvedEntry.products, total: resolvedEntry.sku_count });
      } else if (entity.kind === 'upstream') {
        const releaseID = overview?.runtime.active_release_id;
        const [allPools, relationResult, catalogProducts] = await Promise.all([
          collectGatewayPages((nextPage) => fetchUnifiedPools(entity.item.id, nextPage, 100, controller.signal)),
          fetchUnifiedChannelRelations(entity.item.id, controller.signal),
          releaseID
            ? collectGatewayPages((nextPage) => fetchUnifiedCatalogProducts(releaseID, nextPage, 100, controller.signal))
                .then((items) => items.filter((product) => product.channel_id === entity.item.id))
            : Promise.resolve([]),
        ]);
        if (!controller.signal.aborted) {
          setPools(allPools);
          setUpstreamRelations(relationResult.items);
          setUpstreamProducts(mergeChannelProducts(catalogProducts, relationResult.items));
        }
      } else {
        const result = await fetchUnifiedCallDetail(entity.item.id, 1, 1, controller.signal);
        if (!controller.signal.aborted) {
          setCallDetails({
            attemptTotal: result.attempts.total,
            requestLogsAvailable: Boolean(result.request_logs_available),
          });
        }
      }
    };
    load()
      .catch((requestError: unknown) => {
        if (!controller.signal.aborted) setDetailError(errorMessage(requestError));
      })
      .finally(() => {
        if (!controller.signal.aborted) setDetailLoading(false);
      });
    return () => controller.abort();
  }, [revision, selectedKey, overviewLoading, overview?.runtime.active_release_id]);

  const currentPage = data?.tab === tab ? data.page : null;
  const searchText = search.trim().toLowerCase();
  const visiblePage = currentPage && tab === 'calls' && (searchText || statusFilter)
      ? ({
      ...currentPage,
      items: currentPage.items.filter(item => {
        const effectiveStatus = String((item as { status?: string }).status || '');
        const matchesStatus = !statusFilter || effectiveStatus === statusFilter;
        const searchable = Object.values(item as unknown as Record<string, unknown>);
        const matchesSearch = !searchText || searchable.some(value => String(value ?? '').toLowerCase().includes(searchText));
        return matchesStatus && matchesSearch;
      }),
      } as unknown as typeof currentPage)
      : currentPage;
  const statusReady = Boolean(overview?.runtime_ready) && !overviewError;
  // Editing the control-plane configuration must remain available while the
  // runtime is being repaired. `runtime_ready` only describes traffic serving;
  // it is not an authorization or configuration-management gate.
  const managementReady = overview !== null
    && !overviewLoading
    && !overviewError
    && (overview.management_revision ?? 0) >= 2;
  const statusText = overviewLoading
    ? '正在检查模型调用状态'
    : overviewError
      ? '模型调用状态读取失败'
      : statusReady
        ? '模型调用已开放'
        : '模型调用暂不可用';
  const firstBlocker = overview?.blockers?.find(Boolean);
  const statusReason = overviewLoading
    ? '正在检查配置和服务状态'
    : overviewError
      ? '请刷新后重试'
      : statusReady
        ? '当前实例可以接收模型请求'
        : (firstBlocker && readinessReasonLabel[firstBlocker]) || '当前实例还不能接收模型请求';
  const activeTitle = useMemo(() => selectedEntityTitle(selected), [selected]);

  const changeTab = (nextTab: OpsConsoleTab) => {
    if (nextTab === tab) return;
    setTab(nextTab);
    setPage(1);
    setSelected(null);
    setData(null);
    setSearch('');
    setStatusFilter('');
    setModelTypeFilter('');
    setGatewayTypeFilter('');
  };

  return (
    <div className="ops-console min-w-0 space-y-3">
      <PageHeader
        icon={Layers3}
        title="运维台"
        meta="按网关管理连接、Key、上游模型与映射"
        actions={(
          <div className="ops-header-actions">
            <div
              role="status"
              className={`ops-status-chip ${statusReady ? 'ops-status-chip-ready' : overviewLoading ? 'ops-status-chip-loading' : 'ops-status-chip-pending'}`}
              title={statusReason}
            >
              {overviewLoading ? <LoaderCircle size={16} className="shrink-0 animate-spin" /> : statusReady ? <CheckCircle2 size={16} className="shrink-0" /> : <Clock3 size={16} className="shrink-0" />}
              <span className="ops-status-copy">
                <strong>{statusText}</strong>
                <small>{statusReason}</small>
              </span>
            </div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              title="刷新"
              aria-label="刷新"
              onClick={reload}
              disabled={loading || overviewLoading || detailLoading}
            >
              <RefreshCw size={15} className={loading || overviewLoading ? 'animate-spin' : ''} />
              刷新
            </Button>
          </div>
        )}
      />

      {overviewError && <ErrorNotice message={overviewError} onRetry={reload} />}

      <OpsMetricBar items={[
        { label: '上游渠道', value: overview?.target.channels ?? '-', icon: Database },
        { label: '模型', value: overview?.target.models ?? '-', icon: Layers3 },
        { label: 'API Key', value: overview?.target.credentials ?? '-', icon: KeyRound },
        { label: '调用次数', value: overview?.target.calls ?? '-', icon: Activity },
      ]} />

      <div className="ops-workspace min-h-0">
        <section className="ops-list-panel min-w-0 overflow-hidden">
          <div className="ops-list-toolbar">
            <div role="tablist" aria-label="运维台视图" className="ops-tabs">
            {tabs.map(({ key, label, icon: Icon }) => (
              <button
                key={key}
                type="button"
                role="tab"
                aria-selected={tab === key}
                onClick={() => changeTab(key)}
                className={`ops-tab inline-flex shrink-0 items-center gap-2 text-sm font-semibold transition-colors ${tab === key ? 'ops-tab-active' : ''}`}
              >
                <Icon size={15} />
                {label}
              </button>
            ))}
            </div>
            <div className="ops-list-actions">
              {tab === 'upstream' && <Button type="button" size="sm" onClick={() => setCreatingChannel(true)} disabled={!managementReady}><Plus size={14} />新建网关</Button>}
              <label className="ops-search">
                <Search size={15} aria-hidden="true" />
                <span className="sr-only">搜索当前列表</span>
                <input value={search} onChange={event => { setSearch(event.target.value); setSelected(null); setPage(1); }} placeholder="搜索当前列表" />
                {search && <button type="button" title="清除搜索" aria-label="清除搜索" onClick={() => setSearch('')}><X size={14} /></button>}
              </label>
              <div className="ops-filter-control" title="按状态筛选">
                <SlidersHorizontal size={15} aria-hidden="true" />
                <span className="sr-only">按状态筛选</span>
                <Select
                  value={statusFilter}
                  onChange={value => { setStatusFilter(value); setSelected(null); setPage(1); }}
                  options={(tab === 'models' ? modelStatusFilters : tab === 'upstream' ? upstreamStatusFilters : statusFilters).map(option => ({ value: option.value, label: option.label }))}
                  className="ops-filter-select"
                />
              </div>
              {tab === 'models' && <div className="ops-filter-control" title="按模型类型筛选">
                <Layers3 size={15} aria-hidden="true" />
                <span className="sr-only">按模型类型筛选</span>
                <Select
                  value={modelTypeFilter}
                  onChange={value => { setModelTypeFilter(value as '' | ModelType); setSelected(null); setPage(1); }}
                  options={modelTypeFilters.map(option => ({ value: option.value, label: option.label }))}
                  className="ops-filter-select"
                />
              </div>}
            </div>
          </div>
          {tab === 'upstream' && <div className="ops-capability-filter" role="group" aria-label="按网关能力筛选">
            {gatewayTypeFilters.map((option) => <button
              key={option.value || 'all'}
              type="button"
              aria-pressed={gatewayTypeFilter === option.value}
              className={gatewayTypeFilter === option.value ? 'ops-capability-filter-active' : ''}
              onClick={() => { setGatewayTypeFilter(option.value); setSelected(null); setPage(1); }}
            >{option.label}</button>)}
          </div>}
          <div className="ops-table-scroll min-w-0" aria-busy={loading}>
            {error ? (
              <div className="p-4"><ErrorNotice message={error} onRetry={reload} /></div>
            ) : loading || !currentPage ? (
              <div role="status" className="flex min-h-72 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"><LoaderCircle size={18} className="animate-spin" />正在读取</div>
            ) : (
              <OpsTable tab={tab} page={visiblePage || currentPage} selected={selected} onSelect={setSelected} modelRelease={data?.tab === 'models' ? data.release : null} brokenModels={brokenModels} />
            )}
          </div>
          {!error && currentPage && (
            <Pagination
              page={currentPage.page}
              pageSize={currentPage.page_size}
              total={currentPage.total}
              loading={loading}
              onPageChange={(nextPage) => { setSelected(null); setPage(nextPage); }}
              onPageSizeChange={(nextSize) => { setSelected(null); setPage(1); setPageSize(nextSize); }}
            />
          )}
        </section>

        <Drawer
          open={Boolean(selected)}
          onClose={() => setSelected(null)}
          title={activeTitle}
          subtitle={selected?.kind === 'model' ? '公开名称、售价与路由关系' : selected?.kind === 'upstream' ? '可访问模型、接入配置与 API Key' : '调用执行详情'}
          width="ops-detail-drawer-width"
          panelClassName="ops-detail-drawer-panel"
        >
          <OpsDetailSidebar
            selected={selected}
            title={activeTitle}
            modelDetails={modelDetails}
            pools={pools}
            upstreamProducts={selected?.kind === 'upstream' ? upstreamProducts : []}
            upstreamRelations={upstreamRelations}
            callDetails={callDetails}
            loading={detailLoading}
            error={detailError}
            activeReleaseId={overview?.runtime.active_release_id}
            activeRelease={data?.tab === 'upstream' ? data.release : null}
            canEdit={managementReady}
            onChanged={selected?.kind === 'model' ? reloadModel : reload}
            embedded
          />
        </Drawer>
      </div>
      {creatingChannel && <CreateChannelDialog onClose={() => setCreatingChannel(false)} onSaved={() => { setCreatingChannel(false); setSelected(null); setPage(1); reload(); }} />}
    </div>
  );
};

const OpsTable: React.FC<{
  tab: OpsConsoleTab;
  page: UnifiedGatewayPage<UnifiedCatalogModelEntry> | UnifiedGatewayPage<UnifiedChannel> | UnifiedGatewayPage<UnifiedCall>;
  selected: OpsSelectedEntity | null;
  onSelect: (entity: OpsSelectedEntity) => void;
  modelRelease?: UnifiedCatalogRelease | null;
  brokenModels?: Set<string>;
}> = ({ tab, page, selected, onSelect, modelRelease, brokenModels }) => {
  if (tab === 'models') {
    const modelPage = page as UnifiedGatewayPage<UnifiedCatalogModelEntry>;
    const release = modelRelease;
    const modelItems = modelPage.items;
    return (
      <DataTable headers={['模型 / 类型', '服务规格', '上游产品', 'Adapter / 映射', '下游入口', '状态']}>
        {modelItems.map((entry) => {
          const skus = modelSKUs(entry);
          const products = modelProducts(entry);
          const modelType = entry.model_type;
          const modelName = entry.display_name || entry.model_code || '未命名模型';
          const skuNames = uniqueValues(skus.map(formatSKUDisplayName));
          const skuSummaries = uniqueValues(skus.map(formatSKUServiceSummary));
          const skuTechnicalCodes = uniqueValues(skus.map((sku) => sku.sku_code));
          const upstreamNames = uniqueValues(products.map((product) => product.channel_name || product.product_code));
          const adapterNames = uniqueValues(products.map((product) => `${product.adapter_code}@${product.adapter_version}`));
          const mappingNames = uniqueValues(products.flatMap((product) => mappingSections(product.capability_constraints)));
          const paths = uniqueValues(skus.flatMap((sku) => sku.downstream_paths?.length ? sku.downstream_paths : [sku.route_template]));
          // 熔断只影响调用，不影响配置状态，所以单独挂一枚标记而不是改 StatusBadge。
          const brokenNames = uniqueValues(skus.map((sku) => sku.api_name).filter((name) => brokenModels?.has(name)));
          const selectedModel = selected?.kind === 'model' && selected.item.model_entry.id === entry.id;
          const selectModel = () => {
            if (release) onSelect({ kind: 'model', item: { ...release, model_entry: entry } });
          };
          return (
            <tr
              key={entry.id}
              tabIndex={0}
              aria-selected={selectedModel}
              onClick={selectModel}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault();
                  selectModel();
                }
              }}
              className={`cursor-pointer transition-colors hover:bg-[var(--surface-muted)]/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--primary)] ${selectedModel ? 'bg-[var(--surface-tint)]' : ''}`}
            >
              <td className="max-w-56 px-4 py-3"><SelectRowButton active={selectedModel} onClick={selectModel}>
                <span className="flex min-w-0 items-center gap-2"><span className="truncate">{modelName}</span><span className="shrink-0 rounded-md bg-[var(--primary-lighter)] px-1.5 py-0.5 text-[10px] font-bold text-[var(--primary)]">{modelTypeLabel[modelType]}</span></span>
                <span className="mt-1 block truncate text-[11px] font-normal text-[var(--text-secondary)]">{entry.model_code} · {entry.sku_count} 个服务规格</span>
              </SelectRowButton></td>
              <td className="max-w-48 px-4 py-3 text-xs"><span className="block truncate" title={skuTechnicalCodes.join(', ')}>{skuNames.length ? `${skuNames.slice(0, 2).join('、')}${skuNames.length > 2 ? ` +${skuNames.length - 2}` : ''}` : '未配置服务规格'}</span><span className="mt-1 block truncate text-[11px] text-[var(--text-secondary)]" title={skuSummaries.join(', ')}>{skuSummaries.length ? `${skuSummaries.slice(0, 1).join('')}${skuSummaries.length > 1 ? ` +${skuSummaries.length - 1}` : ''}` : '-'}</span></td>
              <td className="max-w-48 px-4 py-3 text-xs"><span className="block truncate" title={upstreamNames.join(', ')}>{upstreamNames.length ? upstreamNames.slice(0, 2).join('、') : '暂无上游产品'}</span><span className="mt-1 block truncate text-[11px] text-[var(--text-secondary)]">{products.length ? `${products.length} 个上游产品` : '未绑定产品'}</span></td>
              <td className="max-w-44 px-4 py-3 text-xs"><code className="block truncate" title={adapterNames.join(', ')}>{adapterNames.length ? adapterNames.slice(0, 2).join('、') : '未绑定产品'}</code><span className="mt-1 block truncate text-[11px] text-[var(--text-secondary)]">{mappingNames.length ? `JSON · ${mappingNames.slice(0, 2).join(' / ')}` : products.length ? '标准请求映射' : '未配置'}</span></td>
              <td className="max-w-48 px-4 py-3 text-xs"><span className="block truncate" title={paths.join(', ')}>{paths.length ? `${paths.slice(0, 2).join('、')}${paths.length > 2 ? ` +${paths.length - 2}` : ''}` : '暂无入口'}</span><span className="mt-1 block truncate text-[11px] text-[var(--text-secondary)]">{skus.length ? `${skus.length} 个服务项` : '-'}</span></td>
              <td className="px-4 py-3"><StatusBadge status={entry.status} />{brokenNames.length > 0 && <Link to={`/circuit-breakers?model=${encodeURIComponent(brokenNames[0])}`} onClick={(event) => event.stopPropagation()} title={`以下对外模型名有线路被熔断，调用会走剩余线路或直接 503：${brokenNames.join('、')}`} className="mt-1 flex items-center gap-1 text-[10px] font-bold text-amber-700 hover:underline"><AlertTriangle size={11} />有线路被熔断</Link>}</td>
            </tr>
          );
        })}
      </DataTable>
    );
  }
  if (tab === 'upstream') {
    const channelPage = page as UnifiedGatewayPage<UnifiedChannel>;
    return (
      <DataTable headers={['网关 / 能力', '协议与 Adapter', '上游模型', 'API Key', '状态']}>
        {channelPage.items.map((item) => {
          const types = Array.isArray(item.model_types) ? item.model_types : [];
          const transports = uniqueValues([
            ...(Array.isArray(item.protocols) ? item.protocols : []),
            ...(Array.isArray(item.adapters) ? item.adapters.map((adapter) => `Adapter ${adapter}`) : []),
          ]);
          const models = Array.isArray(item.vendor_models) ? item.vendor_models : [];
          const isSelected = selected?.kind === 'upstream' && selected.item.id === item.id;
          const select = () => onSelect({ kind: 'upstream', item });
          return <tr key={item.id} tabIndex={0} aria-selected={isSelected} onClick={select} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); select(); } }} className={`cursor-pointer transition-colors hover:bg-[var(--surface-muted)]/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--primary)] ${isSelected ? 'bg-[var(--surface-tint)]' : ''}`}>
            <td className="max-w-64 px-4 py-3"><SelectRowButton active={isSelected} onClick={select}><span className="block truncate">{item.display_name || item.channel_code}</span><span className="mt-1 flex flex-wrap gap-1">{types.length > 0 ? types.map((type) => <span key={type} className="rounded-md bg-[var(--primary-lighter)] px-1.5 py-0.5 text-[10px] font-bold text-[var(--primary)]">{modelTypeLabel[type]}</span>) : <span className="text-[10px] font-normal text-[var(--text-secondary)]">尚未接入模型</span>}</span></SelectRowButton></td>
            <td className="max-w-64 px-4 py-3 text-xs"><span className="block truncate" title={transports.join(', ')}>{transports.length ? transports.slice(0, 2).join('、') : '未配置接入协议'}</span><code className="mt-1 block truncate text-[10px] text-[var(--text-secondary)]">{item.channel_code}</code></td>
            <td className="max-w-64 px-4 py-3 text-xs"><span className="block truncate" title={models.join(', ')}>{models.length ? `${models.slice(0, 2).join('、')}${models.length > 2 ? ` +${models.length - 2}` : ''}` : '暂无上游模型'}</span><span className="mt-1 block text-[11px] text-[var(--text-secondary)]">{models.length} 个模型 · {item.product_count || 0} 条协议配置</span></td>
            <td className="px-4 py-3 text-xs"><strong className="tabular-nums text-[var(--text-primary)]">{item.credential_count}</strong><span className="mt-1 block text-[11px] text-[var(--text-secondary)]">{item.pool_count} 个 Key 池</span></td>
            <td className="px-4 py-3"><StatusBadge status={item.status} /></td>
          </tr>;
        })}
      </DataTable>
    );
  }
  const callPage = page as UnifiedGatewayPage<UnifiedCall>;
  return (
    <DataTable headers={['调用 ID', '状态', '报价', '返回方式', '创建时间']}>
        {callPage.items.map((item) => (
        <tr key={item.id} tabIndex={0} aria-selected={selected?.kind === 'call' && selected.item.id === item.id} onClick={() => onSelect({ kind: 'call', item })} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onSelect({ kind: 'call', item }); } }} className={`cursor-pointer transition-colors hover:bg-[var(--surface-muted)]/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--primary)] ${selected?.kind === 'call' && selected.item.id === item.id ? 'bg-[var(--surface-tint)]' : ''}`}>
          <td className="max-w-64 px-4 py-3"><SelectRowButton mono active={selected?.kind === 'call' && selected.item.id === item.id} onClick={() => onSelect({ kind: 'call', item })}>{item.public_id}</SelectRowButton></td>
          <td className="px-4 py-3"><StatusBadge status={item.status} /></td>
          <td className="px-4 py-3 tabular-nums">{item.quoted_amount} <span className="text-xs text-[var(--text-secondary)]">{item.price_currency}</span></td>
          <td className="px-4 py-3">{item.delivery_mode === 'managed_copy' ? '托管' : item.delivery_mode === 'reference' ? '直链' : item.delivery_mode}</td>
          <td className="px-4 py-3 text-xs text-[var(--text-secondary)]">{formatOpsDate(item.created_at)}</td>
        </tr>
      ))}
    </DataTable>
  );
};

const DataTable: React.FC<{ headers: string[]; children: React.ReactNode }> = ({ headers, children }) => {
  const rows = React.Children.toArray(children);
  return <div className="min-w-0">
    <table className="w-full min-w-[700px] text-left text-sm">
      <thead><tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">{headers.map((header) => <th key={header} className="whitespace-nowrap px-4 py-3 font-semibold">{header}</th>)}</tr></thead>
      <tbody className="divide-y divide-[var(--border-soft)]">
        {rows}
        {rows.length === 0 && <tr><td colSpan={headers.length} className="h-64 text-center text-sm text-[var(--text-secondary)]">暂无记录</td></tr>}
      </tbody>
    </table>
  </div>;
};

const SelectRowButton: React.FC<{ children: React.ReactNode; onClick: () => void; active?: boolean; mono?: boolean }> = ({ children, onClick, active, mono }) => (
  <button type="button" onClick={(event) => { event.stopPropagation(); onClick(); }} className={`max-w-full truncate text-left font-semibold ${mono ? 'font-mono text-xs' : ''} ${active ? 'text-[var(--primary)]' : 'text-[var(--text-primary)] hover:text-[var(--primary)]'}`}>
    {children}
  </button>
);

const StatusBadge: React.FC<{ status: string }> = ({ status }) => <Badge variant={opsStatusVariant(status)}>{opsStatusLabel(status)}</Badge>;

const OpsDetailSidebar: React.FC<{
  selected: OpsSelectedEntity | null;
  title: string;
  modelDetails: ModelDetails | null;
  pools: UnifiedPool[];
  upstreamProducts: UnifiedCatalogProduct[];
  upstreamRelations: UnifiedRelationLink[];
  callDetails: CallDetails | null;
  loading: boolean;
  error: string;
  activeReleaseId?: number | null;
  activeRelease?: UnifiedCatalogRelease | null;
  canEdit: boolean;
  onChanged: () => void;
  embedded?: boolean;
}> = ({ selected, title, modelDetails, pools, upstreamProducts, upstreamRelations, callDetails, loading, error, activeReleaseId, activeRelease, canEdit, onChanged, embedded = false }) => (
  <div className={`ops-detail-panel ${embedded ? 'ops-detail-panel-drawer' : 'min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)] lg:sticky lg:top-0'}`}>
    {!embedded && <div className="flex min-h-14 items-center gap-3 border-b border-[var(--border-soft)] px-4 py-3">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-[var(--primary-lighter)] text-[var(--primary)]"><ShieldCheck size={17} /></span>
      <div className="min-w-0"><div className="text-[11px] font-semibold text-[var(--text-secondary)]">详情</div><h2 className="truncate text-sm font-bold text-[var(--text-primary)]">{title}</h2></div>
    </div>}
    {!selected ? (
      <div className="flex min-h-64 flex-col items-center justify-center gap-2 px-6 text-center text-sm text-[var(--text-secondary)]"><Layers3 size={24} className="text-[var(--text-tertiary)]" /><span>选择一条记录查看详情</span></div>
    ) : (
      <div className="ops-detail-content divide-y divide-[var(--border-soft)]">
        {error && <div className="p-4"><ErrorNotice message={error} /></div>}
        {loading && <div role="status" className="flex items-center gap-2 px-4 py-3 text-xs text-[var(--text-secondary)]"><LoaderCircle size={15} className="animate-spin" />正在读取关联数据</div>}
        {selected.kind === 'model' && <ModelSidebar entity={selected.item} details={modelDetails} activeReleaseId={activeReleaseId} canEdit={canEdit && activeReleaseId === selected.item.id} onChanged={onChanged} />}
        {selected.kind === 'upstream' && <UpstreamSidebar entity={selected.item} pools={pools} products={upstreamProducts} relations={upstreamRelations} release={activeRelease || null} canEdit={canEdit && activeReleaseId === activeRelease?.id} onChanged={onChanged} />}
        {selected.kind === 'call' && <CallSidebar entity={selected.item} details={callDetails} />}
      </div>
    )}
  </div>
);

const AccordionSection: React.FC<{ title: string; children: React.ReactNode; open?: boolean }> = ({ title, children, open }) => (
  <details open={open} className="group px-4 py-3">
    <summary className="flex cursor-pointer list-none items-center gap-2 text-xs font-bold text-[var(--text-primary)] [&::-webkit-details-marker]:hidden"><ChevronDown size={14} className="text-[var(--text-secondary)] transition-transform group-open:rotate-180" />{title}</summary>
    <div className="ops-accordion-body mt-3 space-y-2">{children}</div>
  </details>
);

const DetailRow: React.FC<{ label: string; value: React.ReactNode; mono?: boolean }> = ({ label, value, mono }) => <div className="flex items-start justify-between gap-3 text-xs"><dt className="shrink-0 text-[var(--text-secondary)]">{label}</dt><dd className={`min-w-0 break-all text-right font-semibold text-[var(--text-primary)] ${mono ? 'font-mono text-[11px]' : ''}`}>{value}</dd></div>;

const ModelSidebar: React.FC<{ entity: UnifiedCatalogModelSelection; details: ModelDetails | null; activeReleaseId?: number | null; canEdit: boolean; onChanged: () => void }> = ({ entity, details, canEdit, onChanged }) => {
  const [mappingProduct, setMappingProduct] = useState<UnifiedCatalogProduct | null>(null);
  const [mappingText, setMappingText] = useState('');
  // 上游真名。后端 ProductChange 一直收这个字段，这里原本把读到的值原样送回，
  // 于是「同一个对外模型换到上游另一个模型名」在控制台里做不到。
  const [mappingVendor, setMappingVendor] = useState('');
  const [mappingError, setMappingError] = useState('');
  const [mappingNotice, setMappingNotice] = useState('');
  const [mappingSaving, setMappingSaving] = useState(false);
  const [rates, setRates] = useState<UnifiedCatalogRate[]>([]);
  const [ratesLoading, setRatesLoading] = useState(false);
  const [rateError, setRateError] = useState('');
  const [editingRate, setEditingRate] = useState<UnifiedCatalogRate | null>(null);
  const [ratePrice, setRatePrice] = useState('');
  const [rateMode, setRateMode] = useState<'flat' | 'expression'>('flat');
  const [rateExpression, setRateExpression] = useState('');
  const [rateSaving, setRateSaving] = useState(false);
  const [credentials, setCredentials] = useState<UnifiedRelationLink[]>([]);
  const [credentialsLoading, setCredentialsLoading] = useState(false);
  const [credentialsError, setCredentialsError] = useState('');
  const [credentialEditor, setCredentialEditor] = useState<UnifiedCredential | null>(null);
  const [credentialRevision, setCredentialRevision] = useState(0);
  const modelSKUKey = (details?.skus || []).map((sku) => sku.id).sort((left, right) => left - right).join(',');
  const openMapping = (product: UnifiedCatalogProduct) => {
    setMappingProduct(product);
    setMappingText(formatMappingJSON(product.capability_constraints));
    setMappingVendor(product.vendor_model || '');
    setMappingError('');
    setMappingNotice('');
  };
  useEffect(() => {
    const controller = new AbortController();
    const skuIDs = new Set(modelSKUKey.split(',').filter(Boolean).map(Number));
    setRatesLoading(true);
    setRateError('');
    if (skuIDs.size === 0) {
      setRates([]);
      setRatesLoading(false);
      return () => controller.abort();
    }
    fetchUnifiedCatalogRates(entity.id, 'sell', 1, 100, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setRates(result.items.filter((rate) => skuIDs.has(rate.parent_id)));
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setRateError(errorMessage(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setRatesLoading(false);
      });
    return () => controller.abort();
  }, [entity.id, modelSKUKey]);
  // 这里以前按 credential_pool_id 逐池拉一遍 Key，池多就多几个请求，而且拉回来的是
  // 「池里有哪些 Key」——不是「哪些 Key 真在服务这个模型」。关系接口一次 join 到底，
  // 并且把选路会 INNER JOIN 掉的那几环变成 blocked_by 带回来，于是抽屉里能直接看到
  // 「Key 在、但这条链路不会被选中」这种情况。
  const modelAPINames = uniqueValues((details?.skus || []).map((sku) => sku.api_name).filter(Boolean));
  const modelAPIKey = modelAPINames.join(',');
  useEffect(() => {
    const controller = new AbortController();
    if (modelAPINames.length === 0) {
      setCredentials([]);
      setCredentialsLoading(false);
      setCredentialsError('');
      return () => controller.abort();
    }
    setCredentialsLoading(true);
    setCredentialsError('');
    Promise.all(modelAPINames.map((name) => fetchUnifiedModelCredentials(name, controller.signal)))
      .then((results) => {
        if (controller.signal.aborted) return;
        // 一个模型条目可能挂多个对外名，同一条 route+credential 会重复出现，按此去重。
        const seen = new Set<string>();
        setCredentials(results.flatMap((result) => result.items).filter((link) => {
          const key = `${link.route_id}-${link.credential_id}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        }));
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setCredentialsError(errorMessage(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setCredentialsLoading(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelAPIKey, credentialRevision]);
  const beginRateEdit = (rate: UnifiedCatalogRate) => {
    setEditingRate(rate);
    setRatePrice(rate.unit_price);
    setRateMode(rate.pricing_mode === 'expression' ? 'expression' : 'flat');
    setRateExpression(rate.pricing_expr || '');
    setRateError('');
  };
  const saveRate = async () => {
    if (!editingRate || !canEdit || rateSaving) return;
    const sku = details?.skus.find((item) => item.id === editingRate.parent_id);
    if (!sku || !/^\d+(\.\d+)?$/.test(ratePrice.trim()) || (rateMode === 'expression' && !rateExpression.trim())) {
      setRateError('请填写有效的售价和计价表达式');
      return;
    }
    setRateSaving(true);
    setRateError('');
    try {
      await changeUnifiedSellRate({
        expected_active_release_id: entity.id,
        expected_config_version: entity.config_version,
        semantic_version: `ops-rate-${Date.now()}`,
        sku_code: sku.sku_code,
        component_code: editingRate.component_code,
        unit_price: ratePrice.trim(),
        pricing_mode: rateMode,
        pricing_expr: rateMode === 'expression' ? rateExpression.trim() : '',
        reason_code: 'console_price_change',
      });
      setEditingRate(null);
      setMappingNotice('已保存并生效');
      const refreshed = await fetchUnifiedCatalogRates(entity.id, 'sell', 1, 100);
      const skuIDs = new Set((details?.skus || []).map((item) => item.id));
      setRates(refreshed.items.filter((rate) => skuIDs.has(rate.parent_id)));
      onChanged();
    } catch (reason: unknown) {
      setRateError(errorMessage(reason));
    } finally {
      setRateSaving(false);
    }
  };
  const saveMapping = async () => {
    if (!mappingProduct || !canEdit || mappingSaving) return;
    const vendorModel = mappingVendor.trim();
    if (!vendorModel) {
      setMappingError('上游模型名不能为空——它是发给上游的真名，留空会直接把这条线路打废');
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(mappingText);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('映射配置必须是 JSON 对象');
    } catch (reason) {
      setMappingError(reason instanceof Error ? reason.message : '映射配置不是有效 JSON');
      return;
    }
    setMappingSaving(true);
    setMappingError('');
    setMappingNotice('');
    try {
      await changeUnifiedProduct({
        expected_active_release_id: entity.id,
        expected_config_version: entity.config_version,
        semantic_version: `ops-mapping-${Date.now()}`,
        product_code: mappingProduct.product_code,
        vendor_model: vendorModel,
        capability_constraints: parsed,
      });
      setMappingNotice('已保存并生效');
      setMappingProduct(null);
      onChanged();
    } catch (reason: unknown) {
      setMappingError(errorMessage(reason));
    } finally {
      setMappingSaving(false);
    }
  };
  // 各类模型配置共用一个通知条，避免抽屉里出现多套不同的反馈。
  const changeCtx: CatalogChangeContext = { catalogId: entity.id, configVersion: entity.config_version, canEdit, onChanged, onNotice: setMappingNotice };
  return <>
    {mappingError && <div className="p-4 pb-0"><ErrorNotice message={mappingError} /></div>}
    {mappingNotice && <div role="status" className="mx-4 mt-3 rounded-lg border border-emerald-300/40 bg-emerald-500/5 px-3 py-2 text-xs font-semibold text-emerald-600">{mappingNotice}</div>}
    <AccordionSection title="基本信息" open><dl className="space-y-2"><DetailRow label="模型" value={entity.model_entry.display_name || entity.model_entry.model_code} /><DetailRow label="模型标识" value={entity.model_entry.model_code} mono /><DetailRow label="状态" value={<StatusBadge status={entity.model_entry.status} />} /><DetailRow label="模型类型" value={modelTypeLabel[entity.model_entry.model_type]} /><DetailRow label="服务规格数" value={details?.total ?? entity.model_entry.sku_count} /></dl></AccordionSection>
    <AccordionSection title="模型与上游" open><div className="space-y-3"><div><div className="mb-1 text-[11px] font-semibold text-[var(--text-secondary)]">可访问模型</div><div className="flex flex-wrap gap-1.5">{uniqueValues(details?.skus.flatMap((sku) => [sku.display_name, sku.model_code, sku.api_name]) || []).map((value) => <span key={value} className="rounded-md bg-[var(--surface-muted)] px-2 py-1 text-[11px] text-[var(--text-primary)]">{value}</span>)}{details === null ? <span className="text-xs text-[var(--text-secondary)]">正在读取</span> : !details.skus.length && <span className="text-xs text-[var(--text-secondary)]">未配置公开服务项</span>}</div></div><div><div className="mb-1 text-[11px] font-semibold text-[var(--text-secondary)]">上游产品</div><ModelProductList products={details?.products || []} loading={details === null} canEdit={canEdit} onEditMapping={openMapping} /></div>{details && details.skus.length + details.products.length === 0 && <div className="rounded-md border border-amber-200 bg-amber-50/70 px-2.5 py-2 text-[11px] leading-4 text-amber-700">该模型目前只有目录记录，尚未配置模型服务项、上游产品或下游入口。</div>}</div></AccordionSection>
    <AccordionSection title="上游 API Key">
      {credentialsError && <ErrorNotice message={credentialsError} />}
      {credentialsLoading ? <p className="text-xs text-[var(--text-secondary)]">正在读取 Key 元数据</p> : credentials.length === 0 ? <p className="text-xs text-[var(--text-secondary)]">当前模型未绑定可用 Key</p> : <ul className="space-y-1.5">{credentials.map((credential, index) => <li key={`${credential.route_id}-${credential.credential_id}`} className={`flex items-center justify-between gap-3 rounded-md border px-2.5 py-2 text-xs ${credential.serving ? 'border-[var(--border-soft)]' : 'border-amber-300/60 bg-amber-50/50'}`}><div className="min-w-0"><strong className="block truncate text-[11px] text-[var(--text-primary)]" title={credential.credential_code}>{formatCredentialDisplayName(credential.credential_code, credential.pool_name, index)}</strong><span className="mt-1 block truncate text-[10px] text-[var(--text-secondary)]">{credential.pool_name || '未命名 Key 池'} · {credential.transport_code} · 权重 {credential.credential_weight}</span><code className="mt-1 block truncate text-[10px] text-[var(--text-tertiary)]" title={credential.credential_code}>标识：{credential.credential_code}</code>{!credential.serving && <span className="mt-1 block text-[10px] font-semibold text-amber-700">不在服务：{(credential.blocked_by || []).map(describeRelationBlock).join('、')}</span>}</div><div className="flex shrink-0 items-center gap-2"><div className="text-right text-[10px] text-[var(--text-secondary)]"><StatusBadge status={credential.credential_status} /><span className="mt-1 block">请求 {credential.request_limit ?? '不限'} · 任务 {credential.task_limit ?? '不限'}</span></div>{canEdit && (credential.credential_status === 'active' || credential.credential_status === 'draining') && <button type="button" title="管理 API Key" aria-label={`管理 ${credential.credential_code}`} className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-tint)] hover:text-[var(--primary)]" onClick={() => setCredentialEditor({ id: credential.credential_id, channel_id: credential.channel_id, credential_pool_id: credential.credential_pool_id, credential_code: credential.credential_code, status: credential.credential_status, config_version: credential.credential_config_version, request_limit: credential.request_limit, task_limit: credential.task_limit, weight: credential.credential_weight, pool_code: credential.pool_code, pool_name: credential.pool_name })}><Pencil size={14} /></button>}</div></li>)}</ul>}
    </AccordionSection>
    {credentialEditor && <CredentialDialog item={credentialEditor} readOnly={!canEdit} onClose={() => setCredentialEditor(null)} onSaved={() => { setCredentialEditor(null); setCredentialRevision((value) => value + 1); onChanged(); }} />}
    {mappingProduct && <section className="border-b border-[var(--border-soft)] px-4 py-3"><div className="flex items-center justify-between gap-2"><strong className="text-xs text-[var(--text-primary)]">编辑上游产品</strong><span className="shrink-0 rounded bg-emerald-100 px-1.5 py-0.5 text-[10px] font-bold text-emerald-700">保存后立即生效</span></div><code className="mt-1 block truncate text-[10px] text-[var(--text-secondary)]">{mappingProduct.product_code}</code><label className="mt-2 block text-[11px] font-semibold">上游模型名<input value={mappingVendor} onChange={(event) => setMappingVendor(event.target.value)} spellCheck={false} className="mt-1 w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-2 py-1.5 font-mono text-xs font-normal" placeholder="发给上游的真实模型名" /></label><label className="mt-2 block text-[11px] font-semibold">JSON 映射<div className="mt-1"><React.Suspense fallback={<div className="grid h-40 items-center rounded-lg border border-[var(--border-soft)] text-center text-[11px] text-[var(--text-secondary)]">正在加载编辑器</div>}><JsonEditor value={mappingText} onChange={setMappingText} height="16rem" /></React.Suspense></div></label><div className="mt-2 flex justify-end gap-2"><Button type="button" size="sm" variant="ghost" onClick={() => setMappingProduct(null)} disabled={mappingSaving}>取消</Button><Button type="button" size="sm" onClick={() => void saveMapping()} loading={mappingSaving}><Save size={14} />保存上游产品</Button></div></section>}
    <AccordionSection title="下游入口"><div className="flex flex-wrap gap-1.5">{uniqueValues(details?.skus.flatMap((sku) => (sku.downstream_paths?.length ? sku.downstream_paths : [sku.route_template]).map((path) => `${sku.api_name} · ${path}`)) || []).map((value) => <code key={value} className="rounded-md bg-[var(--surface-muted)] px-2 py-1 text-[11px] text-[var(--text-primary)]">{value}</code>)}{details === null ? <span className="text-xs text-[var(--text-secondary)]">正在读取</span> : !details.skus.length && <span className="text-xs text-[var(--text-secondary)]">未配置下游入口</span>}</div></AccordionSection>
    <AccordionSection title="用户售价">
      {rateError && <ErrorNotice message={rateError} />}
      {ratesLoading ? <p className="text-xs text-[var(--text-secondary)]">正在读取费率</p> : rates.length === 0 ? <p className="text-xs text-[var(--text-secondary)]">暂无已配置售价</p> : <ul className="space-y-2">{rates.map((rate) => <li key={rate.id} className="rounded-md border border-[var(--border-soft)] px-2.5 py-2 text-xs">
        <div className="flex items-center justify-between gap-2"><strong className="truncate" title={rate.parent_label}>{rateParentLabel(rate, details?.skus || [])} · {rateComponentLabel(rate.component_code)}</strong>{canEdit && <button type="button" className="shrink-0 text-[11px] font-semibold text-[var(--primary)]" onClick={() => beginRateEdit(rate)}>编辑</button>}</div>
        <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-[11px] text-[var(--text-secondary)]"><span>单价 {formatRateNumber(rate.unit_price)} {rateCurrencyLabel(rate.currency_code)} / {rateUnitLabel(rate.unit_code)}</span><span>{ratePricingModeLabel(rate.pricing_mode)}</span></div>
        <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-[10px] text-[var(--text-secondary)]"><span>计量：{rateQuantitySourceLabel(rate.quantity_source)}</span><span>计费：{rateChargeEventLabel(rate.charge_event)}</span><span>上限：{rate.max_quantity ? `${formatRateNumber(rate.max_quantity)} ${rateUnitLabel(rate.unit_code)}` : '不限'}</span></div>
        {rate.pricing_mode === 'expression' && rate.pricing_expr && <code className="mt-1 block break-all text-[10px] text-[var(--text-secondary)]">计价公式：{rate.pricing_expr}</code>}
      </li>)}</ul>}
      {editingRate && <div className="mt-3 rounded-md bg-[var(--surface-muted)] p-2.5"><div className="flex items-center justify-between gap-2"><strong className="text-[11px]">编辑 {rateComponentLabel(editingRate.component_code)}售价</strong><button type="button" className="text-[11px] text-[var(--text-secondary)]" onClick={() => setEditingRate(null)}>取消</button></div><div className="mt-2 grid gap-2 sm:grid-cols-2"><label className="text-[11px] font-semibold">单位价格<input inputMode="decimal" value={ratePrice} onChange={(event) => setRatePrice(event.target.value)} className="mt-1 w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-2 py-1.5 text-xs font-normal" /></label><label className="text-[11px] font-semibold">计价方式<select value={rateMode} onChange={(event) => setRateMode(event.target.value as 'flat' | 'expression')} className="mt-1 w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-2 py-1.5 text-xs font-normal"><option value="flat">固定单价</option><option value="expression">按公式计价</option></select></label></div>{rateMode === 'expression' && <><textarea value={rateExpression} onChange={(event) => setRateExpression(event.target.value)} spellCheck={false} className="mt-2 min-h-20 w-full resize-y rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] p-2 font-mono text-[11px] leading-4" placeholder="例如 input_tokens * 0.000003 + output_tokens * 0.000015" /><div className="mt-1 flex flex-wrap gap-1">{['input_tokens', 'output_tokens', 'seconds', 'priority', 'has_video_ref'].map((variable) => <button key={variable} type="button" className="rounded bg-[var(--surface-card-solid)] px-1.5 py-1 font-mono text-[10px] text-[var(--text-secondary)] hover:text-[var(--primary)]" onClick={() => setRateExpression((value) => `${value}${value && !/[+*/?(,: ]$/.test(value) ? ' ' : ''}${variable}`)}>{variable}</button>)}</div></>}<div className="mt-2 flex justify-end"><Button type="button" size="sm" onClick={() => void saveRate()} loading={rateSaving}><Save size={13} />保存售价</Button></div></div>}
    </AccordionSection>
    <AccordionSection title="上游成本">
      <CostRateEditor ctx={changeCtx} products={details?.products || []} />
    </AccordionSection>
    <AccordionSection title="路由与规格">
      <RoutingEditor ctx={changeCtx} skus={details?.skus || []} products={details?.products || []} />
    </AccordionSection>
    <AccordionSection title="参数约束"><dl className="space-y-2"><DetailRow label="服务等级" value={details ? formatSKUServiceTiers(details.skus.flatMap((sku) => sku.service_tiers)) || '-' : '读取中'} /><DetailRow label="可见性" value={details?.skus.length ? [...new Set(details.skus.map((sku) => sku.visibility))].join(' / ') || '-' : details ? '-' : '读取中'} /></dl></AccordionSection>
    <AccordionSection title="技术标识"><dl className="space-y-2"><DetailRow label="规格标识" value={details ? uniqueValues(details.skus.map((sku) => sku.sku_code)).join(' / ') || '-' : '读取中'} mono /><DetailRow label="内容摘要" value={shortDigest(entity.content_hash, 20)} mono /><DetailRow label="更新时间" value={formatOpsDate(entity.updated_at)} /></dl></AccordionSection>
    <AccordionSection title="审计事件"><p className="text-xs leading-5 text-[var(--text-secondary)]">配置保存、启停和调用过程会记录在审计日志中。</p></AccordionSection>
  </>;
};

const ModelProductList: React.FC<{ products: UnifiedCatalogProduct[]; loading?: boolean; canEdit?: boolean; onEditMapping?: (product: UnifiedCatalogProduct) => void }> = ({ products, loading = false, canEdit = false, onEditMapping }) => {
  if (products.length === 0) return <span className="text-xs text-[var(--text-secondary)]">{loading ? '正在读取' : '未绑定上游产品'}</span>;
  return <ul className="space-y-1.5">{products.map((product) => {
    const mappingKeys = mappingSections(product.capability_constraints);
    const configLabel = product.adapter_code === 'generic' ? 'JSON 映射' : '参数配置 JSON';
    return <li key={product.id} className="rounded-md border border-[var(--border-soft)] px-2.5 py-2 text-xs">
      <div className="flex items-center justify-between gap-2"><strong className="truncate">{product.channel_name || product.product_code}</strong><code className="shrink-0 text-[10px] text-[var(--text-secondary)]">{product.adapter_code}@{product.adapter_version}</code></div>
      <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-[11px] text-[var(--text-secondary)]"><span className="truncate">{product.vendor_model || product.product_code}</span><span>{product.pool_name || '未绑定 Key 池'}</span><span>{product.route_count} 条线路</span><span>{product.task_scope === 'task' ? '异步任务' : '同步请求'}</span></div>
      <details className="group mt-2 rounded-md bg-[var(--surface-muted)] px-2 py-1.5">
        <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[11px] font-semibold text-[var(--text-secondary)] [&::-webkit-details-marker]:hidden"><Braces size={13} />{configLabel}{mappingKeys.length > 0 && <span className="font-normal">· {mappingKeys.join(' / ')}</span>}<ChevronDown size={13} className="ml-auto transition-transform group-open:rotate-180" /></summary>
        <pre className="mt-2 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded border border-[var(--border-soft)] bg-[var(--surface-card-solid)] p-2 text-[10px] leading-4 text-[var(--text-secondary)]">{formatMappingJSON(product.capability_constraints)}</pre>
        {canEdit && onEditMapping && <button type="button" className="mt-2 inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-[11px] font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)]" onClick={() => onEditMapping(product)}><Pencil size={13} />编辑配置</button>}
      </details>
    </li>;
  })}</ul>;
};

const UpstreamSidebar: React.FC<{
  entity: UnifiedChannel;
  pools: UnifiedPool[];
  products: UnifiedCatalogProduct[];
  relations: UnifiedRelationLink[];
  release: UnifiedCatalogRelease | null;
  canEdit: boolean;
  onChanged: () => void;
}> = ({ entity, pools, products, relations, release, canEdit, onChanged }) => {
  const { askConfirmation } = useAppDialog();
  const [editingPool, setEditingPool] = useState<UnifiedPool | null>(null);
  const [ratio, setRatio] = useState('1');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [credentialPoolID, setCredentialPoolID] = useState('');
  const [credentials, setCredentials] = useState<UnifiedCredential[]>([]);
  const [credentialsLoading, setCredentialsLoading] = useState(false);
  const [credentialsError, setCredentialsError] = useState('');
  const [credentialRevision, setCredentialRevision] = useState(0);
  const [selectedCredentialID, setSelectedCredentialID] = useState<number | null>(null);
  const [credentialEditor, setCredentialEditor] = useState<{ item?: UnifiedCredential; poolId: number } | null>(null);
  const [creatingPool, setCreatingPool] = useState(false);
  const [creatingProduct, setCreatingProduct] = useState(false);
  const [modelQuery, setModelQuery] = useState('');
  const [selectedModelKey, setSelectedModelKey] = useState('');
  const [selectedProductKey, setSelectedProductKey] = useState('');
  const [mappingProduct, setMappingProduct] = useState<UnifiedCatalogProduct | null>(null);
  const [hostsProduct, setHostsProduct] = useState<UnifiedCatalogProduct | null>(null);
  const [mappingVendor, setMappingVendor] = useState('');
  const [mappingText, setMappingText] = useState('{}');
  const [mappingSaving, setMappingSaving] = useState(false);
  const [mappingError, setMappingError] = useState('');
  const [runtimeSaving, setRuntimeSaving] = useState(false);
  const [runtimeError, setRuntimeError] = useState('');

  useEffect(() => {
    setMappingProduct(null);
    setCreatingProduct(false);
    setEditingPool(null);
    setSelectedCredentialID(null);
    setModelQuery('');
    setSelectedModelKey('');
    setSelectedProductKey('');
    setNotice('');
    setError('');
    setRuntimeError('');
  }, [entity.id]);

  useEffect(() => {
    setCredentialPoolID((current) => pools.some((pool) => String(pool.id) === current) ? current : pools[0] ? String(pools[0].id) : '');
  }, [pools]);

  useEffect(() => {
    const controller = new AbortController();
    const poolID = Number(credentialPoolID);
    if (!poolID) {
      setCredentials([]);
      setSelectedCredentialID(null);
      setCredentialsLoading(false);
      setCredentialsError('');
      return () => controller.abort();
    }
    setCredentialsLoading(true);
    setCredentialsError('');
    collectGatewayPages((nextPage) => fetchManagedCredentials(nextPage, 100, poolID, controller.signal))
      .then((items) => {
        if (!controller.signal.aborted) {
          setCredentials(items);
          setSelectedCredentialID((current) => current && items.some(item => item.id === current) ? current : null);
        }
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setCredentialsError(errorMessage(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setCredentialsLoading(false);
      });
    return () => controller.abort();
  }, [credentialPoolID, credentialRevision]);

  const selectedCredentialPool = pools.find((pool) => String(pool.id) === credentialPoolID);
  const modelGroups = useMemo(() => groupGatewayModels(products, relations), [products, relations]);
  const visibleModels = useMemo(
    () => filterGatewayModels(modelGroups, modelQuery, selectedCredentialID),
    [modelGroups, modelQuery, selectedCredentialID],
  );

  useEffect(() => {
    setSelectedModelKey(current => visibleModels.some(group => group.key === current) ? current : visibleModels[0]?.key || '');
  }, [visibleModels]);

  const selectedModel = visibleModels.find(group => group.key === selectedModelKey) || visibleModels[0] || null;

  useEffect(() => {
    const selectedProducts = selectedModel?.products || [];
    setSelectedProductKey(current => selectedProducts.some(product => gatewayProductKey(product) === current)
      ? current
      : selectedProducts[0] ? gatewayProductKey(selectedProducts[0]) : '');
  }, [selectedModel]);

  useEffect(() => setRuntimeError(''), [selectedProductKey]);

  const selectedProduct = selectedModel?.products.find(product => gatewayProductKey(product) === selectedProductKey)
    || selectedModel?.products[0]
    || null;
  const selectedProductRelations = selectedProduct
    ? relations.filter(relation => relationBelongsToProduct(relation, selectedProduct))
    : [];
  const selectedScopedRelations = selectedCredentialID
    ? selectedProductRelations.filter(relation => relation.credential_id === selectedCredentialID)
    : selectedProductRelations;
  const selectedPublicNames = uniqueValues(selectedProductRelations.map(relation => relation.api_name));
  const selectedActions = uniqueValues(selectedProductRelations.flatMap(relation => relation.actions || []).map(action => action.action_code));
  const selectedCredentialCount = new Set(selectedScopedRelations.map(relation => relation.credential_id)).size;
  const selectedServingCredentialCount = new Set(selectedScopedRelations.filter(relation => relation.serving).map(relation => relation.credential_id)).size;
  const selectedMappingSections = selectedProduct ? mappingSections(selectedProduct.capability_constraints) : [];
  const selectedRuntimeState = selectedProduct?.offering_state || selectedProductRelations[0]?.offering_state || '';
  const selectedRuntimeVersion = selectedProduct?.offering_state_version || selectedProductRelations[0]?.offering_state_version || 0;

  const credentialsChanged = () => {
    setCredentialRevision((value) => value + 1);
    onChanged();
  };

  const beginEdit = (pool: UnifiedPool) => {
    setEditingPool(pool);
    setRatio(pool.cost_group_ratio || '1');
    setError('');
    setNotice('');
  };

  const saveRatio = async () => {
    if (!editingPool || saving || !canEdit) return;
    const value = ratio.trim();
    if (!/^\d+(\.\d{1,8})?$/.test(value) || Number(value) <= 0) {
      setError('请输入大于 0 的倍率，最多 8 位小数');
      return;
    }
    setSaving(true);
    setError('');
    setNotice('');
    try {
      await updateUnifiedPool(editingPool, {
        display_name: editingPool.display_name,
        request_limit: editingPool.request_limit,
        task_limit: editingPool.task_limit,
        cost_group_ratio: value,
      });
      setEditingPool(null);
      setNotice(`已更新 ${editingPool.display_name} 的成本倍率`);
      onChanged();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  const openMapping = (product: UnifiedCatalogProduct) => {
    setMappingProduct(product);
    setMappingVendor(product.vendor_model || '');
    setMappingText(formatMappingJSON(product.capability_constraints));
    setMappingError('');
  };

  const saveMapping = async () => {
    if (!mappingProduct || !release || !canEdit || mappingSaving) return;
    const vendorModel = mappingVendor.trim();
    if (!vendorModel) {
      setMappingError('请填写上游模型名');
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(mappingText);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('配置必须是 JSON 对象');
    } catch (reason) {
      setMappingError(reason instanceof Error ? reason.message : 'JSON 格式无效');
      return;
    }
    setMappingSaving(true);
    setMappingError('');
    try {
      await changeUnifiedProduct({
        expected_active_release_id: release.id,
        expected_config_version: release.config_version,
        semantic_version: `gateway-product-${Date.now()}`,
        product_code: mappingProduct.product_code,
        vendor_model: vendorModel,
        capability_constraints: parsed,
      });
      setMappingProduct(null);
      setNotice('上游模型配置已保存并生效');
      onChanged();
    } catch (reason: unknown) {
      setMappingError(errorMessage(reason));
    } finally {
      setMappingSaving(false);
    }
  };

  const changeOfferingRuntimeState = async (state: 'active' | 'disabled') => {
    if (!selectedProduct || !canEdit || runtimeSaving) return;
    if (!selectedRuntimeVersion) {
      setRuntimeError('线路状态版本不可用，请刷新页面后重试');
      return;
    }
    if (state === 'disabled') {
      const confirmed = await askConfirmation({
        title: '停用这条上游线路？',
        description: '保存后新请求不会再选择此线路，正在执行的请求不受影响。',
        confirmLabel: '停用线路',
        tone: 'danger',
      });
      if (!confirmed) return;
    }
    setRuntimeSaving(true);
    setRuntimeError('');
    try {
      await setUnifiedOfferingRuntimeState(selectedProduct.offering_id, {
        state,
        expected_version: selectedRuntimeVersion,
        reason_code: state === 'active' ? 'console_offering_restore' : 'console_offering_disable',
      });
      setNotice(state === 'active' ? '上游线路已恢复' : '上游线路已停用');
      onChanged();
    } catch (reason: unknown) {
      setRuntimeError(errorMessage(reason));
    } finally {
      setRuntimeSaving(false);
    }
  };

  const gatewayTypes = entity.model_types.length > 0
    ? entity.model_types
    : uniqueValues(products.map(productGatewayType)) as ModelType[];
  const startCreateProduct = () => {
    if (!release || !canEdit || entity.status !== 'active') return;
    if (!pools.some((pool) => pool.status === 'active')) {
      setNotice('先创建 Key 池，再接入上游模型');
      setCreatingPool(true);
      return;
    }
    setCreatingProduct(true);
  };

  return <>
    <div className="ops-gateway-shell">
      <div className="ops-gateway-summary">
        <div className="min-w-0"><div className="flex flex-wrap items-center gap-1.5"><StatusBadge status={entity.status} />{gatewayTypes.map((type) => <span key={type} className="rounded-md bg-[var(--primary-lighter)] px-1.5 py-0.5 text-[10px] font-bold text-[var(--primary)]">{modelTypeLabel[type]}</span>)}</div><code className="mt-2 block truncate text-[10px] text-[var(--text-secondary)]">{entity.channel_code}</code></div>
        <dl><div><dt>上游模型</dt><dd>{modelGroups.length}</dd></div><div><dt>协议配置</dt><dd>{products.length}</dd></div><div><dt>API Key</dt><dd>{entity.credential_count}</dd></div></dl>
      </div>
      {notice && <div role="status" className="mx-4 mt-3 rounded-lg border border-emerald-300/40 bg-emerald-500/5 px-3 py-2 text-xs font-semibold text-emerald-600">{notice}</div>}
      <div className="ops-gateway-workbench">
        <aside className="ops-gateway-key-pane" aria-label="上游 Key">
          <div className="ops-gateway-pane-heading"><div><h3>上游 Key</h3><p>{pools.length > 1 ? `${pools.length} 个 Key 分组` : `${entity.credential_count} 个 Key`}</p></div><div className="ops-gateway-compact-actions"><button type="button" title="新建 Key 分组" aria-label="新建 Key 分组" disabled={!canEdit || entity.status !== 'active'} onClick={() => setCreatingPool(true)}><Layers3 size={14} /></button><button type="button" title="添加 API Key" aria-label="添加 API Key" disabled={!canEdit || selectedCredentialPool?.status !== 'active'} onClick={() => selectedCredentialPool && setCredentialEditor({ poolId: selectedCredentialPool.id })}><Plus size={15} /></button></div></div>
          {credentialsError && <ErrorNotice message={credentialsError} />}
          {pools.length === 0 ? <div className="ops-gateway-empty"><KeyRound size={20} /><span>此渠道还没有 API Key</span></div> : <>
            {pools.length > 1 ? <Select value={credentialPoolID} onChange={(value) => { setCredentialPoolID(value); setSelectedCredentialID(null); }} options={pools.map(pool => ({ value: String(pool.id), label: `${pool.display_name} · ${pool.credential_count}` }))} className="w-full" /> : <div className="ops-gateway-default-pool"><span>默认 Key 分组</span><StatusBadge status={selectedCredentialPool?.status || 'disabled'} /></div>}
            <button type="button" className={`ops-gateway-key-filter ${selectedCredentialID === null ? 'ops-gateway-key-filter-active' : ''}`} aria-pressed={selectedCredentialID === null} onClick={() => setSelectedCredentialID(null)}><span className="ops-gateway-key-mark"><Layers3 size={14} /></span><span><strong>全部模型</strong><small>{modelGroups.length} 个上游模型</small></span></button>
            <div className="ops-gateway-key-list" aria-busy={credentialsLoading}>
              {credentialsLoading ? <p className="py-4 text-xs text-[var(--text-secondary)]">正在读取 API Key</p> : credentials.length === 0 ? <div className="ops-gateway-empty"><KeyRound size={20} /><span>当前分组没有 Key</span></div> : credentials.map((credential, index) => {
                const servedModels = new Set(relations.filter(relation => relation.credential_id === credential.id).map(relation => (relation.vendor_model || relation.product_code).trim().toLocaleLowerCase())).size;
                const active = selectedCredentialID === credential.id;
                return <div key={credential.id} className={`ops-gateway-key-item ${active ? 'ops-gateway-key-item-active' : ''}`}><button type="button" className="ops-gateway-key-filter" aria-pressed={active} onClick={() => setSelectedCredentialID(credential.id)}><span className="ops-gateway-key-mark"><KeyRound size={14} /></span><span><strong>{formatCredentialDisplayName(credential.credential_code, selectedCredentialPool?.display_name, index)}</strong><small>{opsStatusLabel(credential.status)} · {servedModels} 个关联模型 · 权重 {credential.weight}</small></span></button>{canEdit && (credential.status === 'active' || credential.status === 'draining') && <button type="button" className="ops-gateway-item-edit" title="管理 API Key" aria-label={`管理 ${credential.credential_code}`} onClick={() => setCredentialEditor({ item: credential, poolId: selectedCredentialPool!.id })}><Pencil size={13} /></button>}</div>;
              })}
            </div>
            {selectedCredentialPool && <div className="ops-gateway-pool-summary"><span>{pools.length > 1 ? selectedCredentialPool.display_name : 'Key 分组设置'}</span><span>成本 ×{selectedCredentialPool.cost_group_ratio || '1'}{canEdit && selectedCredentialPool.status === 'active' && <button type="button" onClick={() => beginEdit(selectedCredentialPool)}>修改</button>}</span></div>}
            {error && <ErrorNotice message={error} />}
            {editingPool && <div className="ops-gateway-pool-editor"><div className="flex items-center justify-between gap-2"><strong>成本倍率</strong><button type="button" onClick={() => setEditingPool(null)} disabled={saving}>取消</button></div><input inputMode="decimal" value={ratio} onChange={(event) => setRatio(event.target.value)} className={configurationInputClass} aria-label="成本倍率" /><div className="mt-2 flex justify-end"><Button type="button" size="sm" onClick={() => void saveRatio()} loading={saving}><Save size={13} />保存</Button></div></div>}
          </>}
        </aside>

        <section className="ops-gateway-model-pane" aria-label="上游模型">
          <div className="ops-gateway-model-toolbar"><div><h3>上游模型</h3><p>{selectedCredentialID ? `当前 Key 关联 ${visibleModels.length} / ${modelGroups.length}` : `${modelGroups.length} 个模型 · ${products.length} 条协议配置`}</p></div><label className="ops-gateway-model-search"><Search size={14} aria-hidden="true" /><span className="sr-only">搜索上游模型</span><input value={modelQuery} onChange={event => setModelQuery(event.target.value)} placeholder="搜索模型、协议或端点" />{modelQuery && <button type="button" title="清除搜索" aria-label="清除搜索" onClick={() => setModelQuery('')}><X size={13} /></button>}</label><Button type="button" size="sm" onClick={startCreateProduct} disabled={!release || !canEdit || entity.status !== 'active'}><Plus size={14} />接入模型</Button></div>
          {products.length === 0 ? <div className="ops-gateway-empty"><Layers3 size={20} /><span>此渠道尚未接入上游模型</span></div> : visibleModels.length === 0 ? <div className="ops-gateway-empty"><Search size={20} /><span>{selectedCredentialID ? '此 Key 暂无可访问模型' : '没有匹配的上游模型'}</span></div> : <div className="ops-gateway-model-browser">
            <div className="ops-gateway-model-list" role="listbox" aria-label="上游模型列表">{visibleModels.map(group => {
              const active = group.key === selectedModel?.key;
              return <button key={group.key} type="button" role="option" aria-selected={active} className={active ? 'ops-gateway-model-row-active' : ''} onClick={() => setSelectedModelKey(group.key)}><span><strong title={group.vendorModel}>{group.vendorModel}</strong><small>{group.products.length} 条协议配置 · {group.publicNames.length} 个调用名</small></span><ChevronDown size={14} aria-hidden="true" /></button>;
            })}</div>
            {selectedModel && <div className="ops-gateway-model-detail">
              <div className="ops-gateway-model-title"><div className="min-w-0"><div className="flex min-w-0 items-center gap-2"><h3 className="truncate">{selectedModel.vendorModel}</h3><span>{modelTypeLabel[productGatewayType(selectedModel.products[0])]}</span></div><div className="ops-model-tags">{selectedModel.publicNames.length ? selectedModel.publicNames.map(name => <span key={name}>{name}</span>) : <span>尚未关联对外调用名</span>}</div></div></div>
              <div className="ops-gateway-interface-list" role="listbox" aria-label={`${selectedModel.vendorModel} 的协议配置`}>{selectedModel.products.map(product => {
                const productRelations = relations.filter(relation => relationBelongsToProduct(relation, product));
                const scopedRelations = selectedCredentialID
                  ? productRelations.filter(relation => relation.credential_id === selectedCredentialID)
                  : productRelations;
                const available = scopedRelations.some(relation => relation.serving);
                const runtimeState = product.offering_state || productRelations[0]?.offering_state || '';
                const runtimeAvailable = (!runtimeState || runtimeState === 'active') && available;
                const availabilityLabel = runtimeState && runtimeState !== 'active' ? opsStatusLabel(runtimeState) : runtimeAvailable ? '可调用' : '未就绪';
                const active = gatewayProductKey(product) === gatewayProductKey(selectedProduct!);
                return <button key={gatewayProductKey(product)} type="button" role="option" aria-selected={active} className={active ? 'ops-gateway-interface-row-active' : ''} onClick={() => setSelectedProductKey(gatewayProductKey(product))}><span className="ops-gateway-interface-main"><strong>{product.protocol || product.adapter_code}</strong><code>{product.request_method} {product.request_path}</code></span><span className="ops-gateway-interface-side"><small>{product.adapter_code}@{product.adapter_version}</small><em className={runtimeAvailable ? 'ops-interface-ready' : ''}>{availabilityLabel}</em></span></button>;
              })}</div>
              {selectedProduct && <div className="ops-gateway-interface-detail">
                <div className="ops-gateway-interface-heading">
                  <div><div className="flex items-center gap-2"><h4>接口配置</h4>{selectedRuntimeState && <StatusBadge status={selectedRuntimeState} />}</div><p>{selectedProduct.product_code}</p></div>
                  {canEdit && <div className="flex flex-wrap justify-end gap-2">
                    {selectedRuntimeState && selectedRuntimeState !== 'disabled' && <Button type="button" size="sm" variant="danger" onClick={() => void changeOfferingRuntimeState('disabled')} loading={runtimeSaving}><Power size={13} />停用线路</Button>}
                    {selectedRuntimeState && selectedRuntimeState !== 'active' && <Button type="button" size="sm" variant="secondary" onClick={() => void changeOfferingRuntimeState('active')} loading={runtimeSaving}><RefreshCw size={13} />恢复线路</Button>}
                    <Button type="button" size="sm" variant="secondary" onClick={() => setHostsProduct(selectedProduct)} disabled={runtimeSaving}><ShieldCheck size={13} />结果域名</Button>
                    <Button type="button" size="sm" variant="secondary" onClick={() => openMapping(selectedProduct)} disabled={runtimeSaving}><Pencil size={13} />编辑配置</Button>
                  </div>}
                </div>
                {runtimeError && <ErrorNotice message={runtimeError} />}
                <dl className="ops-gateway-connection-grid"><DetailRow label="完整端点" value={`${selectedProduct.base_url}${selectedProduct.request_path}`} mono /><DetailRow label="协议" value={selectedProduct.protocol || '-'} mono /><DetailRow label="Adapter" value={`${selectedProduct.adapter_code}@${selectedProduct.adapter_version}`} mono /><DetailRow label="Key 分组" value={selectedProduct.pool_name || '未绑定'} /><DetailRow label="执行方式" value={selectedProduct.task_scope === 'task' ? selectedActions.includes('query') ? '异步提交 + 轮询' : '异步提交' : '同步请求'} /><DetailRow label="可用 Key" value={`${selectedServingCredentialCount} / ${selectedCredentialCount}`} /></dl>
                <div className="ops-gateway-public-names"><span>对外调用名</span><div>{selectedPublicNames.length ? selectedPublicNames.map(name => <code key={name}>{name}</code>) : <small>尚未关联</small>}</div></div>
                <div className="ops-gateway-json"><div><Braces size={14} /><strong>{productGatewayType(selectedProduct) === 'video' ? '上下游 JSON 映射' : '参数与能力配置'}</strong>{selectedMappingSections.length > 0 && <span>{selectedMappingSections.join(' / ')}</span>}</div><pre>{formatMappingJSON(selectedProduct.capability_constraints)}</pre></div>
              </div>}
            </div>}
          </div>}
        </section>
      </div>
    </div>

    {creatingPool && <CreatePoolDialog channelId={entity.id} onClose={() => setCreatingPool(false)} onSaved={() => { setCreatingPool(false); credentialsChanged(); }} />}
    {creatingProduct && release && <AddUpstreamModelDialog channel={entity} release={release} pools={pools} onClose={() => setCreatingProduct(false)} onSaved={() => { setCreatingProduct(false); setNotice('上游模型已接入并生效'); onChanged(); }} />}
    {credentialEditor && <CredentialDialog item={credentialEditor.item} poolId={credentialEditor.poolId} readOnly={!canEdit} onClose={() => setCredentialEditor(null)} onSaved={() => { setCredentialEditor(null); credentialsChanged(); }} />}
    {hostsProduct && release && <AllowedHostsDialog release={release} product={hostsProduct} onClose={() => setHostsProduct(null)} onSaved={() => { setHostsProduct(null); setNotice('结果域名已保存并生效'); onChanged(); }} />}
    {mappingProduct && <Modal open title={`编辑 ${mappingProduct.vendor_model || mappingProduct.product_code}`} onClose={() => { if (!mappingSaving) setMappingProduct(null); }} width="max-w-3xl"><div className="space-y-4">{mappingError && <ErrorNotice message={mappingError} />}<label className="block text-sm font-semibold">上游模型名<input value={mappingVendor} onChange={(event) => setMappingVendor(event.target.value)} spellCheck={false} className={`${configurationInputClass} font-mono`} /></label><label className="block text-sm font-semibold">{productGatewayType(mappingProduct) === 'video' ? '上下游 JSON 映射' : '参数与能力配置'}<div className="mt-1"><React.Suspense fallback={<div className="grid h-64 place-items-center rounded-lg border border-[var(--border-soft)] text-xs text-[var(--text-secondary)]">正在加载编辑器</div>}><JsonEditor value={mappingText} onChange={setMappingText} height="24rem" /></React.Suspense></div></label><div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" onClick={() => setMappingProduct(null)} disabled={mappingSaving}>取消</Button><Button type="button" onClick={() => void saveMapping()} loading={mappingSaving}><Save size={14} />保存并生效</Button></div></div></Modal>}
  </>;
};

const validConfigurationCode = (value: string) => /^[a-z][a-z0-9_.-]{0,127}$/.test(value);
const validConfigurationName = (value: string) => value.length > 0 && value.length <= 128 && !/[\x00\n\r\t]/.test(value);
const optionalLimit = (value: string) => value === '' ? null : Number(value);
const validLimit = (value: number | null) => value === null || Number.isSafeInteger(value) && value >= 1 && value <= 1000000;

const CreateChannelDialog: React.FC<{ onClose: () => void; onSaved: () => void }> = ({ onClose, onSaved }) => {
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const normalizedCode = code.trim();
    const normalizedName = name.trim();
    if (!validConfigurationCode(normalizedCode) || !validConfigurationName(normalizedName)) {
      setError('请填写有效的渠道标识和显示名');
      return;
    }
    setSaving(true);
    setError('');
    try {
      await createUnifiedChannel({ channel_code: normalizedCode, display_name: normalizedName });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };
  return <Modal open title="新建上游渠道" onClose={() => { if (!saving) onClose(); }} width="max-w-md">
    <form className="space-y-4" onSubmit={submit}>
      {error && <ErrorNotice message={error} />}
      <label className="block text-sm font-semibold">渠道名称<input autoFocus maxLength={128} value={name} onChange={(event) => setName(event.target.value)} className={configurationInputClass} placeholder="例如 OpenAI 官方" /></label>
      <label className="block text-sm font-semibold">渠道标识<input maxLength={128} value={code} onChange={(event) => setCode(event.target.value.toLowerCase())} className={`${configurationInputClass} font-mono`} placeholder="例如 openai" spellCheck={false} /></label>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" onClick={onClose} disabled={saving}>取消</Button><Button type="submit" loading={saving}><Plus size={15} />创建渠道</Button></div>
    </form>
  </Modal>;
};

const CreatePoolDialog: React.FC<{ channelId: number; onClose: () => void; onSaved: () => void }> = ({ channelId, onClose, onSaved }) => {
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [requests, setRequests] = useState('');
  const [tasks, setTasks] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const normalizedCode = code.trim();
    const normalizedName = name.trim();
    const requestLimit = optionalLimit(requests);
    const taskLimit = optionalLimit(tasks);
    if (!validConfigurationCode(normalizedCode) || !validConfigurationName(normalizedName) || !validLimit(requestLimit) || !validLimit(taskLimit)) {
      setError('请填写有效标识、名称和并发上限');
      return;
    }
    setSaving(true);
    setError('');
    try {
      await createUnifiedPool(channelId, { pool_code: normalizedCode, display_name: normalizedName, request_limit: requestLimit, task_limit: taskLimit });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };
  return <Modal open title="新建 Key 池" onClose={() => { if (!saving) onClose(); }} width="max-w-md">
    <form className="space-y-4" onSubmit={submit}>
      {error && <ErrorNotice message={error} />}
      <label className="block text-sm font-semibold">Key 池名称<input autoFocus maxLength={128} value={name} onChange={(event) => setName(event.target.value)} className={configurationInputClass} placeholder="例如 默认模型调用" /></label>
      <label className="block text-sm font-semibold">Key 池标识<input maxLength={128} value={code} onChange={(event) => setCode(event.target.value.toLowerCase())} className={`${configurationInputClass} font-mono`} placeholder="例如 default" spellCheck={false} /></label>
      <div className="grid gap-3 sm:grid-cols-2"><label className="block text-sm font-semibold">请求并发<input type="number" min={1} max={1000000} value={requests} onChange={(event) => setRequests(event.target.value)} className={configurationInputClass} placeholder="不限" /></label><label className="block text-sm font-semibold">任务并发<input type="number" min={1} max={1000000} value={tasks} onChange={(event) => setTasks(event.target.value)} className={configurationInputClass} placeholder="不限" /></label></div>
      <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" onClick={onClose} disabled={saving}>取消</Button><Button type="submit" loading={saving}><Plus size={15} />创建 Key 池</Button></div>
    </form>
  </Modal>;
};

const CallSidebar: React.FC<{ entity: UnifiedCall; details: CallDetails | null }> = ({ entity, details }) => (
  <>
    <AccordionSection title="基本信息" open><dl className="space-y-2"><DetailRow label="调用 ID" value={entity.public_id} mono /><DetailRow label="状态" value={<StatusBadge status={entity.status} />} /><DetailRow label="返回方式" value={entity.delivery_mode === 'managed_copy' ? '托管' : entity.delivery_mode === 'reference' ? '直链' : entity.delivery_mode} /><DetailRow label="创建时间" value={formatOpsDate(entity.created_at)} /></dl></AccordionSection>
    <AccordionSection title="计价"><dl className="space-y-2"><DetailRow label="报价" value={`${entity.quoted_amount} ${entity.price_currency}`} /><DetailRow label="用户" value={`#${entity.user_id}`} mono /><DetailRow label="令牌" value={`#${entity.token_id}`} mono /></dl></AccordionSection>
    <AccordionSection title="执行链路"><dl className="space-y-2"><DetailRow label="尝试次数" value={details?.attemptTotal ?? '读取中'} /><DetailRow label="请求日志" value={details ? details.requestLogsAvailable ? '可用' : '不可用' : '读取中'} /></dl><p className="mt-3 text-xs leading-5 text-[var(--text-secondary)]">完整请求内容和尝试记录已保留在调用审计数据中。</p></AccordionSection>
    <AccordionSection title="技术标识"><dl className="space-y-2"><DetailRow label="调用 ID" value={entity.id} mono /><DetailRow label="更新时间" value={formatOpsDate(entity.updated_at)} /></dl></AccordionSection>
  </>
);

export default OpsConsole;
