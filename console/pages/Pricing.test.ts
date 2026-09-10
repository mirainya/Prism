import { describe, expect, it } from 'vitest';

import { formatPricingRate } from './Pricing';
import type { PublicPricingCurrency, PublicRateComponent } from '../services/pricingApi';

const currency: PublicPricingCurrency = { code: 'USD', version: 1, fraction_digits: 8 };

const component = (unitScale: number): PublicRateComponent => ({
  id: 1,
  component_code: 'input',
  unit_code: 'token',
  quantity_source: 'usage.input_tokens',
  charge_event: 'call.succeeded',
  unit_price: '0.00000300',
  unit_scale: unitScale,
  quantity_step: '0',
  max_quantity: '1000000',
});

describe('formatPricingRate', () => {
  it('preserves the decimal string returned by the catalog', () => {
    expect(formatPricingRate(component(0), currency)).toBe('USD 0.00000300 / token');
  });

  it('shows the declared power-of-ten unit scale', () => {
    expect(formatPricingRate(component(3), currency)).toBe('USD 0.00000300 / 10^3 token');
  });
});
