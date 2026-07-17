// 映射配置类型
export interface FieldMapping { stdField: string; vendorField: string }
export interface ValueMapping { field: string; stdValue: string; vendorValue: string }
export interface FixedParam { name: string; value: string }
export interface TypeConvert { field: string; type: 'string_to_array' | 'array_to_string'; separator: string }
export interface SuccessCondition {
    field: string;
    operator: 'eq' | 'ne' | 'exists' | 'not_exists' | 'in' | 'not_in' | 'gt' | 'gte' | 'lt' | 'lte';
    value?: string | number | boolean;
    values?: (string | number)[];
}

export const SUCCESS_CONDITION_OPERATORS = [
    {value: 'eq', label: '等于', needValue: true, needValues: false},
    {value: 'ne', label: '不等于', needValue: true, needValues: false},
    {value: 'exists', label: '存在', needValue: false, needValues: false},
    {value: 'not_exists', label: '不存在', needValue: false, needValues: false},
    {value: 'in', label: '在列表中', needValue: false, needValues: true},
    {value: 'not_in', label: '不在列表中', needValue: false, needValues: true},
    {value: 'gt', label: '大于', needValue: true, needValues: false},
    {value: 'gte', label: '大于等于', needValue: true, needValues: false},
    {value: 'lt', label: '小于', needValue: true, needValues: false},
    {value: 'lte', label: '小于等于', needValue: true, needValues: false},
];

export const parseParamMapping = (mapping: Record<string, any>) => {
    // 请求 value_mapping 的方向是标准值 -> Provider 值。
    const fieldMappings: FieldMapping[] = [];
    const valueMappings: ValueMapping[] = [];
    const fixedParams: FixedParam[] = [];
    const typeConverts: TypeConvert[] = [];
    if (mapping.field_mapping) {
        Object.entries(mapping.field_mapping).forEach(([std, vendor]) => {
            fieldMappings.push({stdField: std, vendorField: vendor as string});
        });
    }
    if (mapping.value_mapping) {
        Object.entries(mapping.value_mapping).forEach(([field, values]) => {
            Object.entries(values as Record<string, string>).forEach(([stdVal, vendorVal]) => {
                valueMappings.push({field, stdValue: stdVal, vendorValue: vendorVal});
            });
        });
    }
    if (mapping.fixed_params) {
        Object.entries(mapping.fixed_params).forEach(([name, value]) => {
            fixedParams.push({name, value: String(value)});
        });
    }
    if (mapping.type_convert) {
        Object.entries(mapping.type_convert).forEach(([field, config]) => {
            const c = config as { type: string; separator: string };
            typeConverts.push({ field, type: c.type as 'string_to_array' | 'array_to_string', separator: c.separator || ',' });
        });
    }
    return {fieldMappings, valueMappings, fixedParams, typeConverts};
};

export const parseResponseMapping = (mapping: Record<string, any>) => {
    // 响应 value_mapping 的方向相反：Provider 值 -> 标准值，编辑行仍统一展示两列语义。
    const fieldMappings: FieldMapping[] = [];
    const valueMappings: ValueMapping[] = [];
    const typeConverts: TypeConvert[] = [];
    let successCondition: SuccessCondition | null = null;
    if (mapping.field_mapping) {
        Object.entries(mapping.field_mapping).forEach(([std, vendor]) => {
            fieldMappings.push({stdField: std, vendorField: vendor as string});
        });
    }
    if (mapping.value_mapping) {
        Object.entries(mapping.value_mapping).forEach(([field, values]) => {
            Object.entries(values as Record<string, string>).forEach(([vendorVal, stdVal]) => {
                valueMappings.push({field, stdValue: stdVal, vendorValue: vendorVal});
            });
        });
    }
    if (mapping.type_convert) {
        Object.entries(mapping.type_convert).forEach(([field, config]) => {
            const c = config as { type: string; separator: string };
            typeConverts.push({ field, type: c.type as 'string_to_array' | 'array_to_string', separator: c.separator || ',' });
        });
    }
    if (mapping.success_condition) successCondition = mapping.success_condition as SuccessCondition;
    return {fieldMappings, valueMappings, typeConverts, successCondition};
};

export const buildParamMapping = (fieldMappings: FieldMapping[], valueMappings: ValueMapping[], fixedParams: FixedParam[], typeConverts: TypeConvert[] = []) => {
    const result: Record<string, any> = {};
    const fieldMap: Record<string, string> = {};
    fieldMappings.forEach(m => { if (m.stdField && m.vendorField) fieldMap[m.stdField] = m.vendorField; });
    if (Object.keys(fieldMap).length > 0) result.field_mapping = fieldMap;
    const valueMap: Record<string, Record<string, string>> = {};
    valueMappings.forEach(m => {
        if (m.field && m.stdValue && m.vendorValue) {
            if (!valueMap[m.field]) valueMap[m.field] = {};
            valueMap[m.field][m.stdValue] = m.vendorValue;
        }
    });
    if (Object.keys(valueMap).length > 0) result.value_mapping = valueMap;
    const fixedMap: Record<string, string> = {};
    fixedParams.forEach(p => { if (p.name && p.value) fixedMap[p.name] = p.value; });
    if (Object.keys(fixedMap).length > 0) result.fixed_params = fixedMap;
    const typeConvertMap: Record<string, { type: string; separator: string }> = {};
    typeConverts.forEach(tc => { if (tc.field && tc.type) typeConvertMap[tc.field] = {type: tc.type, separator: tc.separator || ','}; });
    if (Object.keys(typeConvertMap).length > 0) result.type_convert = typeConvertMap;
    return result;
};

export const buildResponseMapping = (fieldMappings: FieldMapping[], valueMappings: ValueMapping[], typeConverts: TypeConvert[] = [], successCondition: SuccessCondition | null = null) => {
    // build 与 parse 对称，并在这里完成响应值映射方向的反转。
    const result: Record<string, any> = {};
    const fieldMap: Record<string, string> = {};
    fieldMappings.forEach(m => { if (m.stdField && m.vendorField) fieldMap[m.stdField] = m.vendorField; });
    if (Object.keys(fieldMap).length > 0) result.field_mapping = fieldMap;
    const valueMap: Record<string, Record<string, string>> = {};
    valueMappings.forEach(m => {
        if (m.field && m.stdValue && m.vendorValue) {
            if (!valueMap[m.field]) valueMap[m.field] = {};
            valueMap[m.field][m.vendorValue] = m.stdValue;
        }
    });
    if (Object.keys(valueMap).length > 0) result.value_mapping = valueMap;
    const typeConvertMap: Record<string, { type: string; separator: string }> = {};
    typeConverts.forEach(tc => { if (tc.field && tc.type) typeConvertMap[tc.field] = {type: tc.type, separator: tc.separator || ','}; });
    if (Object.keys(typeConvertMap).length > 0) result.type_convert = typeConvertMap;
    if (successCondition && successCondition.field && successCondition.operator) {
        const cond: Record<string, any> = { field: successCondition.field, operator: successCondition.operator };
        const opConfig = SUCCESS_CONDITION_OPERATORS.find(o => o.value === successCondition.operator);
        if (opConfig?.needValue && successCondition.value !== undefined && successCondition.value !== '') {
            const numVal = Number(successCondition.value);
            cond.value = isNaN(numVal) ? successCondition.value : numVal;
        }
        if (opConfig?.needValues && successCondition.values && successCondition.values.length > 0) {
            cond.values = successCondition.values.map(v => { const numVal = Number(v); return isNaN(numVal) ? v : numVal; });
        }
        result.success_condition = cond;
    }
    return result;
};
