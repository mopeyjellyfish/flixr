/** PIN focus regression with mocked API responses; no server authentication is exercised. */
import { expect, test } from '@playwright/test';

for (const activation of ['click', 'keyboard', 'touch'] as const) {
  test.describe(`PIN focus via ${activation}`, () => {
    test.use(activation === 'touch' ? { hasTouch: true, viewport: { width: 390, height: 844 } } : {});

    test('keeps focus through opening, typing, and reopening', async ({ page, browserName }, testInfo) => {
      const errors: string[] = [];
      page.on('pageerror', (error) => errors.push(error.message));
      await page.route('**/api/v1/**', (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/setup/status')) return route.fulfill({ json: { claimed: true, readiness: { ffprobe: true, ffmpeg: true } } });
        if (path.endsWith('/profiles')) return route.fulfill({ json: { profiles: [
          { id: 'a', name: 'Ari', protected: true },
          { id: 'b', name: 'Bea', protected: true },
          { id: 'c', name: 'Cam', protected: false },
        ] } });
        if (path.endsWith('/select')) return route.fulfill({ status: 401, json: { error: { code: 'invalid_pin' } } });
        return route.fulfill({ json: { screens: [] } });
      });
      await page.goto('/');
      const ari = page.getByRole('button', { name: /Ari PIN protected/i });
      if (activation === 'keyboard') { await ari.focus(); await page.keyboard.press('Enter'); }
      else if (activation === 'touch') await ari.tap();
      else await ari.click();
      const first = page.getByLabel('Profile PIN digit 1 of 4');
      await expect(first).toBeFocused();
      await expect(first).toHaveAttribute('type', 'password');
      await page.keyboard.type('12');
      await expect(page.getByLabel('Profile PIN digit 3 of 4')).toBeFocused();
      await page.keyboard.press('ArrowLeft');
      await expect(page.getByLabel('Profile PIN digit 2 of 4')).toBeFocused();
      await page.keyboard.press('ArrowRight');
      // Wait for the actual entrance animations, then verify they did not reset focus.
      await page.getByRole('dialog').evaluate(async (dialog) => {
        await Promise.all(dialog.getAnimations({ subtree: true }).map((animation) => animation.finished));
      });
      await expect(page.getByLabel('Profile PIN digit 3 of 4')).toBeFocused();
      await page.screenshot({ path: testInfo.outputPath('pin-focus.png') });
      const selection = page.waitForRequest((request) => request.url().endsWith('/profiles/a/select'));
      await page.keyboard.type('34');
      expect((await selection).postDataJSON()).toEqual({ pin: '1234' });
      await expect(page.getByRole('alert')).toContainText(/not valid/i);
      await expect(first).toBeFocused();
      // macOS WebKit uses Option+Tab to include buttons in keyboard navigation.
      await page.keyboard.press(browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Shift+Tab' : 'Shift+Tab');
      await expect(page.getByRole('button', { name: 'Close', exact: true })).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(page.getByRole('dialog')).toHaveCount(0);
      await page.getByRole('button', { name: /Bea PIN protected/i }).click();
      await expect(page.getByRole('dialog')).toHaveAccessibleName('Enter PIN for Bea');
      await expect(first).toBeFocused();
      await page.getByRole('button', { name: 'Cancel', exact: true }).click();
      const unprotected = page.waitForRequest((request) => request.url().endsWith('/profiles/c/select'));
      await page.getByRole('button', { name: /Cam Ready/i }).click();
      expect((await unprotected).postDataJSON()).toEqual({ pin: '' });
      await expect(page.getByRole('dialog')).toHaveCount(0);
      expect(errors).toEqual([]);
    });
  });
}
