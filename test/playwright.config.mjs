// Playwright configuration for VM Inventory UI tests
export default {
  testDir: '.',
  testMatch: 'ui-tests.mjs',
  use: {
    baseURL: 'http://localhost:8081',
    headless: true,
  },
  workers: 1,
  timeout: 30000,
};
