import React, { useEffect, useMemo, useState } from 'react';
import { Plus, Save, Trash2 } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import {
  changeUnifiedTransportAllowedHosts,
  fetchUnifiedTransportAllowedHosts,
  type UnifiedAllowedHost,
  type UnifiedCatalogProduct,
  type UnifiedCatalogRelease,
} from '../../services/unifiedGatewayApi';
import { ErrorNotice, errorMessage } from '../unified_gateway/Feedback';

const inputClass = 'w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-2.5 py-2 text-sm';

const normalizeHost = (value: string) => value.trim().toLowerCase().replace(/\.$/, '');

const hostKey = (host: UnifiedAllowedHost) => `${host.protocol}\u0000${normalizeHost(host.host)}\u0000${host.port}`;

const baseHost = (baseURL: string): UnifiedAllowedHost | null => {
  try {
    const value = new URL(baseURL);
    if (value.protocol !== 'http:' && value.protocol !== 'https:') return null;
    return {
      protocol: value.protocol.slice(0, -1) as UnifiedAllowedHost['protocol'],
      host: normalizeHost(value.hostname),
      port: Number(value.port || (value.protocol === 'https:' ? 443 : 80)),
    };
  } catch {
    return null;
  }
};

export const AllowedHostsDialog: React.FC<{
  release: UnifiedCatalogRelease;
  product: UnifiedCatalogProduct;
  onClose: () => void;
  onSaved: () => void;
}> = ({ release, product, onClose, onSaved }) => {
  const [hosts, setHosts] = useState<UnifiedAllowedHost[]>([]);
  const [configVersion, setConfigVersion] = useState(release.config_version);
  const [base, setBase] = useState<UnifiedAllowedHost | null>(() => baseHost(product.base_url));
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    fetchUnifiedTransportAllowedHosts(release.id, product.channel_transport_id, controller.signal)
      .then((result) => {
        if (controller.signal.aborted) return;
        setHosts(result.allowed_hosts);
        setConfigVersion(result.config_version);
        setBase(baseHost(result.base_url));
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [product.channel_transport_id, release.id]);

  const baseKey = base ? hostKey(base) : '';
  const duplicate = useMemo(() => {
    const keys = hosts.map(hostKey);
    return new Set(keys).size !== keys.length;
  }, [hosts]);

  const update = (index: number, patch: Partial<UnifiedAllowedHost>) => {
    setHosts((current) => current.map((host, hostIndex) => hostIndex === index ? { ...host, ...patch } : host));
  };

  const add = () => setHosts((current) => [...current, { protocol: 'https', host: '', port: 443 }]);

  const remove = (index: number) => setHosts((current) => current.filter((_, hostIndex) => hostIndex !== index));

  const save = async () => {
    if (saving || loading) return;
    const normalized = hosts.map((host) => ({ ...host, host: normalizeHost(host.host), port: Number(host.port) }));
    if (normalized.some((host) => !host.host || host.host.length > 255 || host.host.includes('*') || /[\s/?#@\[\]]/.test(host.host) || !Number.isInteger(host.port) || host.port < 1 || host.port > 65535)) {
      setError('请填写有效的精确域名和端口');
      return;
    }
    if (duplicate) {
      setError('结果域名存在重复项');
      return;
    }
    setSaving(true);
    setError('');
    try {
      await changeUnifiedTransportAllowedHosts({
        expected_active_release_id: release.id,
        expected_config_version: configVersion,
        semantic_version: `transport-hosts-${Date.now()}`,
        transport_code: product.transport_code,
        allowed_hosts: normalized,
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setSaving(false);
    }
  };

  return <Modal open title="结果域名" width="max-w-2xl" onClose={() => { if (!saving) onClose(); }}>
    <div className="space-y-4">
      {error && <ErrorNotice message={error} />}
      <div className="overflow-hidden rounded-md border border-[var(--border-soft)]">
        <div className="hidden grid-cols-[6.5rem_minmax(0,1fr)_6rem_2.5rem] gap-2 bg-[var(--surface-muted)] px-3 py-2 text-[11px] font-semibold text-[var(--text-secondary)] sm:grid"><span>协议</span><span>域名</span><span>端口</span><span /></div>
        <div className="max-h-72 divide-y divide-[var(--border-soft)] overflow-y-auto">
          {loading ? <div className="px-3 py-6 text-center text-xs text-[var(--text-secondary)]">正在读取</div> : hosts.map((host, index) => {
            const fixed = hostKey(host) === baseKey;
            return <div key={index} className="grid grid-cols-[minmax(0,1fr)_6rem_2.5rem] items-end gap-2 px-3 py-3 sm:grid-cols-[6.5rem_minmax(0,1fr)_6rem_2.5rem] sm:items-start sm:py-2">
              <label className="col-span-3 block min-w-0 text-[11px] font-semibold text-[var(--text-secondary)] sm:col-span-1"><span className="mb-1 block sm:hidden">协议</span><Select value={host.protocol} disabled={fixed || saving} onChange={(protocol) => update(index, { protocol: protocol as UnifiedAllowedHost['protocol'], port: host.port === 80 || host.port === 443 ? (protocol === 'https' ? 443 : 80) : host.port })} options={[{ value: 'https', label: 'HTTPS' }, { value: 'http', label: 'HTTP' }]} /></label>
              <label className="block min-w-0 text-[11px] font-semibold text-[var(--text-secondary)]"><span className="mb-1 block sm:hidden">域名</span><input aria-label={`域名 ${index + 1}`} value={host.host} disabled={fixed || saving} onChange={(event) => update(index, { host: event.target.value })} spellCheck={false} className={`${inputClass} font-mono`} />{fixed && <span className="mt-1 block text-[10px] font-semibold text-[var(--primary)]">API 主域名</span>}</label>
              <label className="block text-[11px] font-semibold text-[var(--text-secondary)]"><span className="mb-1 block sm:hidden">端口</span><input aria-label={`端口 ${index + 1}`} type="number" min={1} max={65535} value={host.port} disabled={fixed || saving} onChange={(event) => update(index, { port: Number(event.target.value) })} className={inputClass} /></label>
              <button type="button" title={fixed ? 'API 主域名不可删除' : '删除域名'} aria-label={fixed ? 'API 主域名不可删除' : `删除域名 ${index + 1}`} disabled={fixed || saving} onClick={() => remove(index)} className="grid h-9 w-9 place-items-center rounded-md text-[var(--text-secondary)] hover:bg-red-500/10 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-35 sm:mt-0"><Trash2 size={15} /></button>
            </div>;
          })}
        </div>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="secondary" size="sm" onClick={add} disabled={loading || saving || hosts.length >= 32}><Plus size={14} />添加域名</Button><div className="flex gap-2"><Button type="button" variant="ghost" onClick={onClose} disabled={saving}>取消</Button><Button type="button" onClick={() => void save()} loading={saving} disabled={loading}><Save size={14} />保存并生效</Button></div></div>
    </div>
  </Modal>;
};
