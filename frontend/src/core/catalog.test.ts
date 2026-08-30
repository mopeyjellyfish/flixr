import { describe, expect, it } from 'vitest';
import { browseState } from './catalog';
describe('browse state', () => { it('reports empty and failure states', () => { expect(browseState(0)).toBe('empty'); expect(browseState(2)).toBe('ready'); expect(browseState(2, true)).toBe('failure'); }); });
