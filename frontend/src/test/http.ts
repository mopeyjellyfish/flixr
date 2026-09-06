type TestResponse = { status?: number; json: unknown };
type TestRoute = {
  method?: string;
  path: string | RegExp;
  handle: (request: { url: URL; method: string; body?: unknown }) => TestResponse | Promise<TestResponse>;
};

export function strictFetch(routes: TestRoute[]): typeof fetch {
  return async (input, init) => {
    const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, window.location.origin);
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
    const route = routes.find((candidate) => (!candidate.method || candidate.method.toUpperCase() === method) && (typeof candidate.path === 'string' ? candidate.path === url.pathname + url.search : candidate.path.test(url.pathname + url.search)));
    if (!route) throw new Error(`Unexpected test request: ${method} ${url.pathname}${url.search}`);
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined;
    const response = await route.handle({ url, method, body });
    return new Response(JSON.stringify(response.json), { status: response.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
}
