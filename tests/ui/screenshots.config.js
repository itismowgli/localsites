// Screenshots for the README and the website (docs/screenshots), taken from the real UI and a real,
// isolated LocalSites with demo projects: npx playwright test -c screenshots.config.js
// WebKit, like the macOS app; 1280×809 at 1.5625× = the 2000×1264 images the site expects.
const { defineConfig } = require('@playwright/test');
module.exports = defineConfig({
  testDir: '.',
  testMatch: 'screenshots.spec.js',
  timeout: 120_000,
  workers: 1,
  globalSetup: require.resolve('./sandbox-setup.js'),
  globalTeardown: require.resolve('./sandbox-teardown.js'),
  use: { baseURL: process.env.LS_UI_URL, storageState: require('path').join(require('os').tmpdir(), 'lsui-auth.json'),
    browserName: 'webkit', viewport: { width: 1280, height: 809 }, deviceScaleFactor: 1.5625, colorScheme: 'dark' },
});
