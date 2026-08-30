import { defineConfig, devices } from '@playwright/test';

const production = process.env.FLIXR_ACCEPTANCE_URL;
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  outputDir: 'test-results',
  preserveOutput: 'always',
  use: {
    baseURL: production || 'http://127.0.0.1:4173',
    trace: 'retain-on-failure',
  },
  webServer: production ? undefined : { command: 'npm run build && npm run preview -- --port 4173', port: 4173, reuseExistingServer: true },
  projects: [
    { name: 'chromium', testMatch: /mock-api\.spec\.ts/, use: { ...devices['Desktop Chrome'], launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : undefined } },
    { name: 'firefox', testMatch: /mock-api\.spec\.ts/, use: { ...devices['Desktop Firefox'] } },
    { name: 'webkit', testMatch: /mock-api\.spec\.ts/, use: { ...devices['Desktop Safari'] } },
    { name: 'chromium-production', testMatch: /production\.spec\.ts/, use: { ...devices['Desktop Chrome'], launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH } : undefined } },
  ],
});
