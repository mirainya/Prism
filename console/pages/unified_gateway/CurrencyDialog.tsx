import React, { useRef, useState } from 'react';
import { Save } from 'lucide-react';
import { Button, Modal, Select } from '../../components/ui';
import { createUnifiedCurrency } from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';

export const CurrencyDialog: React.FC<{ onClose: () => void; onSaved: () => void }> = ({ onClose, onSaved }) => {
  const [code, setCode] = useState('USD');
  const [version, setVersion] = useState('1');
  const [digits, setDigits] = useState('8');
  const [rounding, setRounding] = useState('half_even');
  const [maximum, setMaximum] = useState('1000000000');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const locked = useRef(false);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const parsedVersion = Number(version);
    const parsedDigits = Number(digits);
    if (locked.current || !/^[A-Z][A-Z0-9._-]{2,15}$/.test(code) || !Number.isInteger(parsedVersion) || parsedVersion < 1 || !Number.isInteger(parsedDigits) || parsedDigits < 0 || parsedDigits > 18 || !/^[1-9]\d*(\.\d+)?$/.test(maximum)) {
      setError('请检查币种定义');
      return;
    }
    locked.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedCurrency({ currency_code: code, definition_version: parsedVersion, fraction_digits: parsedDigits, rounding_mode: rounding, max_amount: maximum });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  return <Modal open onClose={() => !pending && onClose()} title="新建币种定义"><form onSubmit={submit} className="space-y-4">{error && <ErrorNotice message={error} />}<div className="grid gap-4 sm:grid-cols-2"><Field label="币种代码"><input required maxLength={16} value={code} onChange={event => setCode(event.target.value.toUpperCase())} className={`${inputClass} font-mono`} /></Field><Field label="定义版本"><input required type="number" min={1} step={1} value={version} onChange={event => setVersion(event.target.value)} className={inputClass} /></Field><Field label="最小单位小数位"><input required type="number" min={0} max={18} step={1} value={digits} onChange={event => setDigits(event.target.value)} className={inputClass} /></Field><Field label="舍入规则"><Select value={rounding} onChange={setRounding} options={[{ value: 'half_even', label: '银行家舍入' }, { value: 'half_up', label: '四舍五入' }, { value: 'floor', label: '向下取整' }, { value: 'ceiling', label: '向上取整' }]} /></Field></div><Field label="最大金额"><input required inputMode="decimal" value={maximum} onChange={event => setMaximum(event.target.value)} className={inputClass} /></Field><div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending}><Save size={15} />保存</Button></div></form></Modal>;
};
const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => <label className="block min-w-0 space-y-2 text-sm font-semibold"><span>{label}</span>{children}</label>;
