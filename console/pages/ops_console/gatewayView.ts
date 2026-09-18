import type {
  UnifiedCatalogProduct,
  UnifiedRelationLink,
} from '../../services/unifiedGatewayApi';

export interface GatewayModelGroup {
  key: string;
  vendorModel: string;
  products: UnifiedCatalogProduct[];
  relations: UnifiedRelationLink[];
  publicNames: string[];
  protocols: string[];
  adapters: string[];
}

const unique = (values: string[]) => [...new Set(values.map(value => value.trim()).filter(Boolean))];

const normalizedModelKey = (value: string) => value.trim().toLocaleLowerCase();

export const gatewayProductKey = (product: Pick<
  UnifiedCatalogProduct,
  'id' | 'product_code' | 'product_transport_id' | 'offering_id' | 'credential_pool_id'
>) => {
  const productTransport = product.product_transport_id
    ? `transport:${product.product_transport_id}`
    : product.id ? `product:${product.id}` : `code:${product.product_code}`;
  const offering = product.offering_id
    ? `offering:${product.offering_id}`
    : product.credential_pool_id ? `pool:${product.credential_pool_id}` : 'offering:none';
  return `${productTransport}:${offering}`;
};

export const gatewayRelationProductKey = (relation: UnifiedRelationLink) => gatewayProductKey({
  id: relation.product_id || 0,
  product_code: relation.product_code,
  product_transport_id: relation.product_transport_id || 0,
  offering_id: relation.offering_id,
  credential_pool_id: relation.credential_pool_id,
});

export const relationBelongsToProduct = (
  relation: UnifiedRelationLink,
  product: UnifiedCatalogProduct,
) => gatewayRelationProductKey(relation) === gatewayProductKey(product);

export const groupGatewayModels = (
  products: UnifiedCatalogProduct[],
  relations: UnifiedRelationLink[],
): GatewayModelGroup[] => {
  const groups = new Map<string, GatewayModelGroup>();

  products.forEach(product => {
    const vendorModel = product.vendor_model?.trim() || product.product_code;
    const key = normalizedModelKey(vendorModel);
    const group = groups.get(key) || {
      key,
      vendorModel,
      products: [],
      relations: [],
      publicNames: [],
      protocols: [],
      adapters: [],
    };
    if (!group.products.some(item => gatewayProductKey(item) === gatewayProductKey(product))) {
      group.products.push(product);
    }
    groups.set(key, group);
  });

  relations.forEach(relation => {
    const vendorModel = relation.vendor_model?.trim() || relation.product_code;
    const key = normalizedModelKey(vendorModel);
    const group = groups.get(key);
    if (group) group.relations.push(relation);
  });

  groups.forEach(group => {
    group.publicNames = unique(group.relations.map(relation => relation.api_name));
    group.protocols = unique(group.products.map(product => product.protocol));
    group.adapters = unique(group.products.map(product => (
      product.adapter_code ? `${product.adapter_code}@${product.adapter_version}` : ''
    )));
  });

  return [...groups.values()].sort((left, right) => left.vendorModel.localeCompare(right.vendorModel));
};

export const filterGatewayModels = (
  groups: GatewayModelGroup[],
  query: string,
  credentialId: number | null,
) => {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  return groups.filter(group => {
    if (credentialId && !group.relations.some(relation => relation.credential_id === credentialId)) return false;
    if (!normalizedQuery) return true;
    const searchable = [
      group.vendorModel,
      ...group.publicNames,
      ...group.protocols,
      ...group.adapters,
      ...group.products.flatMap(product => [
        product.product_code,
        product.transport_code,
        product.base_url,
        product.request_path,
        product.pool_name,
      ]),
    ].join('\n').toLocaleLowerCase();
    return searchable.includes(normalizedQuery);
  });
};
