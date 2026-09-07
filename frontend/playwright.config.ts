import { defineConfig, devices } from '@playwright/test';

const production = process.env.FLIXR_ACCEPTANCE_URL;
const previewPort = Number(process.env.FLIXR_TEST_PORT || 4173);
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  outputDir: 'test-results',
  preserveOutput: 'always',
  use: {
    baseURL: production || `http://127.0.0.1:${previewPort}`,
    trace: 'retain-on-failure',
  },
  webServer: production ? undefined : { command: `npm run build && npm run preview -- --port ${previewPort} --strictPort`, port: previewPort, reuseExistingServer: false },
  projects: [
    { name: 'chromium', testMatch: /mock-api\.spec\.ts/, use: { ...devices['Desktop Chrome'], launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : undefined } },
    { name: 'firefox', testMatch: /mock-api\.spec\.ts/, use: { ...devices['Desktop Firefox'] } },
    { name: 'webkit', testMatch: /mock-api\.spec\.ts/, use: { ...devices['Desktop Safari'] } },
    { name: 'chromium-production', testMatch: /production\.spec\.ts/, use: { ...devices['Desktop Chrome'], launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : undefined } },
    { name: 'chromium-metadata', testMatch: /metadata-provider\.spec\.ts/, use: { ...devices['Desktop Chrome'], launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : undefined } },
  ],
});
