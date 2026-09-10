import React, { useMemo, useRef, useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import {
  createCatalogSource,
  reviewCatalogPriceCandidate,
  type CatalogSourceContract,
  type UnifiedCatalogPriceCandidate,
  type UnifiedCatalogSourceOptions,
} from '../../services/unifiedCatalogSourceApi';
import type { UnifiedCurrency } from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({
  label,
  children,
}) => (
  <label className="block min-w-0 space-y-2 text-sm font-semibold">
    <span>{label}</span>
    {children}
  </label>
);

export const CatalogSourceDialog: React.FC<{
  options: UnifiedCatalogSourceOptions;
  onClose: () => void;
  onSaved: () => void;
}> = ({ options, onClose, onSaved }) => {
  const [channelID, setChannelID] = useState(
    options.channels[0] ? String(options.channels[0].id) : '',
  );
  const credentials = useMemo(
    () =>
      options.credentials.filter(
        (item) => item.channel_id === Number(channelID),
      ),
    [channelID, options.credentials],
  );
  const [credentialID, setCredentialID] = useState('');
  const [sourceCode, setSourceCode] = useState('');
  const [contract, setContract] = useState<CatalogSourceContract>(
    'aicost_models_v1',
  );
  const [baseURL, setBaseURL] = useState('https://www.aicost.me');
  const [externalGroup, setExternalGroup] = useState('');
  const [timeout, setTimeoutValue] = useState('30000');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const lock = useRef(false);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const parsedChannel = Number(channelID);
    const parsedCredential = Number(credentialID);
    const parsedTimeout = Number(timeout);
    let parsedURL: URL | null = null;
    try {
      parsedURL = new URL(baseURL);
    } catch {
      parsedURL = null;
    }
    if (
      lock.current ||
      !Number.isSafeInteger(parsedChannel) ||
      parsedChannel < 1 ||
      !Number.isSafeInteger(parsedCredential) ||
      parsedCredential < 1 ||
      !/^[a-z][a-z0-9_.-]{0,127}$/.test(sourceCode) ||
      !externalGroup.trim() ||
      externalGroup.trim().length > 128 ||
      !Number.isSafeInteger(parsedTimeout) ||
      parsedTimeout < 1000 ||
      parsedTimeout > 120000 ||
      !parsedURL ||
      !['http:', 'https:'].includes(parsedURL.protocol) ||
      parsedURL.username !== '' ||
      parsedURL.password !== '' ||
      parsedURL.search !== '' ||
      parsedURL.hash !== '' ||
      !['', '/'].includes(parsedURL.pathname)
    ) {
      setError('请检查目录来源配置');
      return;
    }
    lock.current = true;
    setPending(true);
    setError('');
    try {
      await createCatalogSource({
        channel_id: parsedChannel,
        credential_id: parsedCredential,
        source_code: sourceCode,
        contract_code: contract,
        base_url: parsedURL.origin,
        external_group: externalGroup.trim(),
        request_timeout_ms: parsedTimeout,
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      lock.current = false;
      setPending(false);
    }
  };

  return (
    <Modal open title="新建目录来源" onClose={() => !pending && onClose()}>
      <form onSubmit={submit} className="space-y-4">
        {error && <ErrorNotice message={error} />}
        <fieldset disabled={pending} className="grid gap-4 sm:grid-cols-2">
          <Field label="渠道">
            <Select
              value={channelID}
              onChange={(value) => {
                setChannelID(value);
                setCredentialID('');
              }}
              options={options.channels.map((item) => ({
                value: String(item.id),
                label: item.name,
              }))}
            />
          </Field>
          <Field label="发现凭据">
            <Select
              value={credentialID}
              onChange={setCredentialID}
              options={[
                { value: '', label: '选择凭据' },
                ...credentials.map((item) => ({
                  value: String(item.id),
                  label: item.code,
                })),
              ]}
            />
          </Field>
          <Field label="来源标识">
            <input
              required
              maxLength={128}
              spellCheck={false}
              className={`${inputClass} font-mono`}
              value={sourceCode}
              onChange={(event) =>
                setSourceCode(event.target.value.toLowerCase())
              }
            />
          </Field>
          <Field label="数据合同">
            <Select
              value={contract}
              onChange={(value) => setContract(value as CatalogSourceContract)}
              options={options.contracts.map((item) => ({
                value: item.code,
                label: item.name,
              }))}
            />
          </Field>
          <Field label="服务地址">
            <input
              required
              type="url"
              maxLength={2048}
              spellCheck={false}
              className={inputClass}
              value={baseURL}
              onChange={(event) => setBaseURL(event.target.value)}
            />
          </Field>
          <Field label="上游分组">
            <input
              required
              maxLength={128}
              className={inputClass}
              value={externalGroup}
              onChange={(event) => setExternalGroup(event.target.value)}
            />
          </Field>
          <Field label="请求超时（毫秒）">
            <input
              required
              type="number"
              min={1000}
              max={120000}
              step={1000}
              className={inputClass}
              value={timeout}
              onChange={(event) => setTimeoutValue(event.target.value)}
            />
          </Field>
        </fieldset>
        <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4">
          <Button type="button" variant="ghost" disabled={pending} onClick={onClose}>
            取消
          </Button>
          <Button type="submit" loading={pending}>
            <Save size={15} />保存
          </Button>
        </div>
      </form>
    </Modal>
  );
};

export const CatalogPriceReviewDialog: React.FC<{
  candidate: UnifiedCatalogPriceCandidate;
  currencies: UnifiedCurrency[];
  onClose: () => void;
  onSaved: () => void;
}> = ({ candidate, currencies, onClose, onSaved }) => {
  const [unit, setUnit] = useState('request');
  const [currency, setCurrency] = useState(
    currencies[0]
      ? `${currencies[0].currency_code}:${currencies[0].definition_version}`
      : '',
  );
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const lock = useRef(false);
  const confirm = async (event: React.FormEvent) => {
    event.preventDefault();
    const [currencyCode, rawVersion] = currency.split(':');
    const version = Number(rawVersion);
    if (
      lock.current ||
      !['request', 'second'].includes(unit) ||
      !currencyCode ||
      !Number.isSafeInteger(version) ||
      version < 1
    ) {
      setError('请选择计费单位和币种');
      return;
    }
    lock.current = true;
    setPending(true);
    setError('');
    try {
      await reviewCatalogPriceCandidate(candidate.id, {
        decision: 'confirmed',
        unit_code: unit,
        currency_code: currencyCode,
        currency_version: version,
        reason_code: 'unit_confirmed',
      });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      lock.current = false;
      setPending(false);
    }
  };
  return (
    <Modal open title="确认上游价格" onClose={() => !pending && onClose()}>
      <form onSubmit={confirm} className="space-y-4">
        {error && <ErrorNotice message={error} />}
        <dl className="grid grid-cols-[auto,1fr] gap-x-4 gap-y-2 border-y border-[var(--border-soft)] py-3 text-sm">
          <dt className="text-[var(--text-secondary)]">模型</dt>
          <dd className="break-all font-mono font-semibold">{candidate.model_code}</dd>
          <dt className="text-[var(--text-secondary)]">上游单价</dt>
          <dd className="font-mono font-semibold">{candidate.model_price}</dd>
          <dt className="text-[var(--text-secondary)]">分组</dt>
          <dd className="break-all">{candidate.external_group}</dd>
        </dl>
        <fieldset disabled={pending} className="grid gap-4 sm:grid-cols-2">
          <Field label="计费单位">
            <Select
              value={unit}
              onChange={setUnit}
              options={[
                { value: 'request', label: '按次' },
                { value: 'second', label: '按秒' },
              ]}
            />
          </Field>
          <Field label="币种版本">
            <Select
              value={currency}
              onChange={setCurrency}
              options={currencies.map((item) => ({
                value: `${item.currency_code}:${item.definition_version}`,
                label: `${item.currency_code} · v${item.definition_version}`,
              }))}
            />
          </Field>
        </fieldset>
        <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4">
          <Button type="button" variant="ghost" disabled={pending} onClick={onClose}>
            取消
          </Button>
          <Button type="submit" loading={pending} disabled={!currencies.length}>
            <Save size={15} />生成价格证据
          </Button>
        </div>
      </form>
    </Modal>
  );
};
