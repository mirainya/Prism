import React from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';

const JsonEditor: React.FC<{
    value: string;
    onChange: (val: string) => void;
    height?: string;
    placeholder?: string;
}> = ({ value, onChange, height = '280px', placeholder }) => (
    <CodeMirror
        value={value}
        height={height}
        extensions={[json()]}
        onChange={onChange}
        placeholder={placeholder}
        basicSetup={{ lineNumbers: true, foldGutter: true, bracketMatching: true, autocompletion: false }}
        className="border border-[var(--border-soft)] rounded-lg overflow-hidden text-xs"
    />
);

export default JsonEditor;
