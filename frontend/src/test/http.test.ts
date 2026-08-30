import { expect, it } from 'vitest';
import { strictFetch } from './http';

it('rejects unknown methods and paths with a named request', async () => {
  const fetcher = strictFetch([{ method: 'GET', path: '/api/v1/catalog/view?media=all', handle: () => ({ json: { preference: { view: 'rows', sort: 'title' }, sections: [] } }) }]);
  await expect(fetcher('/api/v1/catalog/view?media=all')).resolves.toBeInstanceOf(Response);
  await expect(fetcher('/api/v1/catalog/view?media=film')).rejects.toThrow('Unexpected test request: GET /api/v1/catalog/view?media=film');
  await expect(fetcher('/api/v1/catalog/view?media=all', { method: 'POST' })).rejects.toThrow('Unexpected test request: POST /api/v1/catalog/view?media=all');
});
