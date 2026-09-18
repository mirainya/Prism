import { request } from './request';

export interface PublicPricingRoute {
  method: string;
  path: string;
}

export interface PublicRateComponent {
  id: number;
  component_code: string;
  unit_code: string;
  quantity_source: string;
  charge_event: string;
  unit_price: string;
  unit_scale: number;
  quantity_step: string;
  max_quantity: string;
}

export interface PublicPricingCurrency {
  code: string;
  version: number;
  fraction_digits: number;
}

/**
 * Recently observed success rate for a SKU, on a 0..100 scale.
 *
 * `source` is `local` when measured from this deployment's own calls and
 * `upstream` when reported by the provider. Only the local figure carries
 * `samples`; the provider publishes no sample count, which is why a 100% figure
 * from upstream may rest on a single call.
 */
export interface PublicPricingAvailability {
  success_rate: string;
  source: 'local' | 'upstream';
  samples?: number;
  window_minutes: number;
  observed_at: string;
}

export interface PublicPricingSKU {
  id: number;
  code: string;
  operation: string;
  routes: PublicPricingRoute[];
  delivery_mode: string;
  max_results: number;
  idempotency_mode: string;
  service_tiers: string[];
  currency: PublicPricingCurrency;
  components: PublicRateComponent[];
  availability?: PublicPricingAvailability;
}

export interface PublicPricingModel {
  code: string;
  model_code: string;
  name: string;
  type: string;
  description: string;
  visibility: string;
  skus: PublicPricingSKU[];
}

export const fetchPublicPricing = (signal?: AbortSignal): Promise<PublicPricingModel[]> =>
  request<PublicPricingModel[]>('/public/pricing', { signal });
