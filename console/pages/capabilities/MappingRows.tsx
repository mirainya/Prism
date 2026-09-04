import React from 'react';
import { X } from 'lucide-react';

export const FieldMappingRow: React.FC<{
    stdField: string;
    stdName: string;
    vendorField: string;
    onChange: (value: string) => void;
    onRemove: () => void;
}> = ({stdField, stdName, vendorField, onChange, onRemove}) => (
    <div className="modal-mapping-row flex items-center gap-2 mb-2">
        <div className="flex-1 px-3 py-2 bg-[var(--surface)] rounded-lg text-sm">
            <span className="text-[var(--text-secondary)]">{stdName}</span>
            <code className="ml-2 text-xs text-[var(--text-secondary)]">{stdField}</code>
        </div>
        <span className="text-[var(--text-secondary)]">→</span>
        <input
            type="text"
            value={vendorField}
            onChange={e => onChange(e.target.value)}
            className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
            placeholder="三方字段名或路径"
        />
        <button type="button" onClick={onRemove}
                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg">
            <X size={14}/>
        </button>
    </div>
);

export const ValueMappingRow: React.FC<{
    stdValue: string;
    vendorValue: string;
    onChange: (value: string) => void;
    onRemove: () => void;
}> = ({stdValue, vendorValue, onChange, onRemove}) => (
    <div className="modal-mapping-row flex items-center gap-2 mb-2">
        <div className="w-32 px-3 py-2 bg-[var(--surface)] rounded-lg text-sm text-[var(--text-secondary)]">{stdValue}</div>
        <span className="text-[var(--text-secondary)]">→</span>
        <input
            type="text"
            value={vendorValue}
            onChange={e => onChange(e.target.value)}
            className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
            placeholder="三方对应值"
        />
        <button type="button" onClick={onRemove}
                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg">
            <X size={14}/>
        </button>
    </div>
);

export const FixedParamRow: React.FC<{
    paramName: string;
    paramValue: string;
    onNameChange: (value: string) => void;
    onValueChange: (value: string) => void;
    onRemove: () => void;
}> = ({paramName, paramValue, onNameChange, onValueChange, onRemove}) => (
    <div className="modal-mapping-row flex items-center gap-2 mb-2">
        <input
            type="text"
            value={paramName}
            onChange={e => onNameChange(e.target.value)}
            className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
            placeholder="参数名"
        />
        <span className="text-[var(--text-secondary)]">=</span>
        <input
            type="text"
            value={paramValue}
            onChange={e => onValueChange(e.target.value)}
            className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
            placeholder="固定值"
        />
        <button type="button" onClick={onRemove}
                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg">
            <X size={14}/>
        </button>
    </div>
);
