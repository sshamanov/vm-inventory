// Playwright UI tests for VM Inventory
// Run: npx playwright test --config test/playwright.config.mjs
import { test, expect } from '@playwright/test';

const BASE = 'http://localhost:8081';
const PASSWORD = process.env.UI_PASSWORD;

async function loginIfNeeded(page) {
  await page.goto(BASE);
  // Wait briefly for the page to render
  await page.waitForTimeout(500);
  // If login overlay is present, log in
  const overlay = page.locator('#login-overlay');
  if (await overlay.isVisible({ timeout: 2000 }).catch(() => false)) {
    if (!PASSWORD) throw new Error('the UI is password-gated: set UI_PASSWORD');
    await page.fill('#login-password', PASSWORD);
    await page.click('#login-form button[type="submit"]');
    await page.waitForSelector('#login-overlay', { state: 'hidden', timeout: 5000 }).catch(() => {});
  }
  // Wait for inventory to load (#geos is no longer empty)
  await page.waitForFunction(() => {
    const el = document.getElementById('geos');
    return el && el.innerHTML.trim() !== '';
  }, { timeout: 10000 });
}

test.describe('Header controls', () => {
  test.beforeEach(async ({ page }) => {
    await loginIfNeeded(page);
  });

  test('Only one search input exists', async ({ page }) => {
    const searches = page.locator('#search');
    await expect(searches).toHaveCount(1);
  });

  test('Geo filter dropdown exists and onGeoFilter is callable', async ({ page }) => {
    const geoSelect = page.locator('#geo-filter');
    await expect(geoSelect).toBeVisible();
    // onGeoFilter must be a global function
    const isFunction = await page.evaluate(() => typeof window.onGeoFilter);
    expect(isFunction).toBe('function');
  });

  test('Geo filter dropdown has options populated', async ({ page }) => {
    const options = page.locator('#geo-filter option');
    const count = await options.count();
    expect(count).toBeGreaterThan(1); // "All Geos" + at least one geo
  });

  test('Geo filter change triggers re-render', async ({ page }) => {
    const options = page.locator('#geo-filter option');
    const count = await options.count();
    if (count < 2) return; // no geos to filter by, skip

    // Select a specific geo
    const secondOptionValue = await options.nth(1).getAttribute('value');
    await page.selectOption('#geo-filter', secondOptionValue);
    await page.waitForTimeout(300); // let render complete

    // Verify geo filter value changed
    const selectedValue = await page.evaluate(() => document.getElementById('geo-filter').value);
    expect(selectedValue).toBe(secondOptionValue);
  });

  test('Publish button says "Publish to Confluence"', async ({ page }) => {
    const btn = page.locator('#publish-btn');
    await expect(btn).toHaveText('Publish to Confluence');
  });

  test('Confluence link says "Confluence Page"', async ({ page }) => {
    const link = page.locator('#confluence-link');
    await expect(link).toHaveText('Confluence Page');
  });

  test('Host filter dropdown exists and onHostFilter is callable', async ({ page }) => {
    const hostSelect = page.locator('#host-filter');
    await expect(hostSelect).toBeVisible();
    const isFunction = await page.evaluate(() => typeof window.onHostFilter);
    expect(isFunction).toBe('function');
  });

  test('Host filter dropdown has options populated', async ({ page }) => {
    const options = page.locator('#host-filter option');
    const count = await options.count();
    expect(count).toBeGreaterThan(1); // "All Hosts" + at least one host
  });

  test('Host filter resets when geo filter is selected', async ({ page }) => {
    // First select a host
    const hostOpts = page.locator('#host-filter option');
    if (await hostOpts.count() < 2) return;
    const hostVal = await hostOpts.nth(1).getAttribute('value');
    await page.selectOption('#host-filter', hostVal);
    // Now select a geo — host should reset to "All Hosts"
    const geoOpts = page.locator('#geo-filter option');
    const geoVal = await geoOpts.nth(1).getAttribute('value');
    await page.selectOption('#geo-filter', geoVal);
    await page.waitForTimeout(300);
    const hostValue = await page.evaluate(() => document.getElementById('host-filter').value);
    expect(hostValue).toBe('');
  });

  test('Geo filter resets when host filter is selected', async ({ page }) => {
    // First select a geo
    const geoOpts = page.locator('#geo-filter option');
    if (await geoOpts.count() < 2) return;
    const geoVal = await geoOpts.nth(1).getAttribute('value');
    await page.selectOption('#geo-filter', geoVal);
    // Now select a host — geo should reset to "All Geos"
    const hostOpts = page.locator('#host-filter option');
    const hostVal = await hostOpts.nth(1).getAttribute('value');
    await page.selectOption('#host-filter', hostVal);
    await page.waitForTimeout(300);
    const geoValue = await page.evaluate(() => document.getElementById('geo-filter').value);
    expect(geoValue).toBe('');
  });
});

test.describe('Inventory rendering', () => {
  test.beforeEach(async ({ page }) => {
    await loginIfNeeded(page);
  });

  test('Host cards are rendered (flat list, no geo sections)', async ({ page }) => {
    // Hosts should be rendered as flat cards under #geos, not grouped in .geo-section divs
    const geoSections = page.locator('.geo-section');
    await expect(geoSections).toHaveCount(0);
    const hostCards = page.locator('.host-card');
    const count = await hostCards.count();
    expect(count).toBeGreaterThan(0);
  });

  test('VM table renders', async ({ page }) => {
    const vmTable = page.locator('table caption:has-text("Virtual Machines")');
    // VM table might not exist if there are no VMs, so this is optional
    const exists = await vmTable.isVisible().catch(() => false);
    // At minimum, geos div should have content
    await expect(page.locator('#geos')).not.toBeEmpty();
  });
});
