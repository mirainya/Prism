import { describe, expect, it } from 'vitest';
import { buildPaginationItems } from './Pagination';

describe('buildPaginationItems', () => {
  it('shows every page for short result sets', () => {
    expect(buildPaginationItems(3, 5)).toEqual([1, 2, 3, 4, 5]);
  });

  it('keeps the first and last page near the beginning', () => {
    expect(buildPaginationItems(2, 20)).toEqual([1, 2, 3, 4, 5, 'end-ellipsis', 20]);
  });

  it('centers the current page in long result sets', () => {
    expect(buildPaginationItems(10, 20)).toEqual([1, 'start-ellipsis', 9, 10, 11, 'end-ellipsis', 20]);
  });

  it('keeps the first and last page near the end', () => {
    expect(buildPaginationItems(19, 20)).toEqual([1, 'start-ellipsis', 16, 17, 18, 19, 20]);
  });
});
