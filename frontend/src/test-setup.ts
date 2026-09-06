import '@testing-library/jest-dom/vitest';

// jsdom has no layout engine; native observers are exercised in browser checks.
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
