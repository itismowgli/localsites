// UI end-to-end tests: the real desktop UI against a real, isolated LocalSites service.
// WebKit = the macOS app's engine; Chromium = Windows (WebView2) and close to Linux.
const { defineConfig } = require('@playwright/test');
module.exports = defineConfig({
  testDir: '.',
  timeout: 60_000,
  workers: 1, // one shared sandbox service
  retries: 0,
  globalSetup: require.resolve('./sandbox-setup.js'),
  globalTeardown: require.resolve('./sandbox-teardown.js'),
  use: { baseURL: process.env.LS_UI_URL, storageState: require('path').join(require('os').tmpdir(), 'lsui-auth.json'), viewport: { width: 1360, height: 860 }, trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  projects: [
    { name: 'webkit', use: { browserName: 'webkit' } },
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
});
