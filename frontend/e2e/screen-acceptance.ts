import { expect, type Browser, type Page, type TestInfo } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/** Independent cookies, real sockets, and playable media from the embedded binary. */
export async function acceptScreens(sender: Page, browser: Browser, info: TestInfo) {
  const receiverContext = await browser.newContext({ baseURL: new URL(sender.url()).origin });
  const receiver = await receiverContext.newPage();
  const errors: string[] = [];
  receiver.on('pageerror', (error) => errors.push(error.message));
  try {
    await receiver.goto('/profiles');
    await receiver.getByRole('button', { name: /production viewer/i }).click();
    await expect(receiver).toHaveURL(/\/home$/);
    await openTitle(receiver);
    await receiver.getByRole('button', { name: 'Use this device as a screen' }).click();
    await expect(receiver.getByRole('region', { name: 'Local screens' })).toContainText('is ready');
    await openTitle(sender);
    for (const viewport of [{ name: 'tv', width: 1920, height: 1080 }, { name: 'desktop', width: 1440, height: 900 }, { name: 'tablet', width: 1024, height: 768 }, { name: 'phone', width: 390, height: 844 }]) {
      await sender.setViewportSize(viewport);
      await sender.getByRole('button', { name: 'Choose a screen', exact: true }).click();
      const chooser = sender.getByRole('dialog', { name: 'Choose a local screen', exact: true });
      await expect(chooser.getByRole('button', { name: /screen · available/ })).toBeVisible();
      await chooser.evaluate((element) => Promise.all(element.getAnimations({ subtree: true }).map((animation) => animation.finished.catch(() => undefined))));
      expect((await new AxeBuilder({ page: sender }).analyze()).violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')).toEqual([]);
      expect(await sender.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await sender.screenshot({ path: info.outputPath(`screen-chooser-${viewport.name}.png`), fullPage: true });
      await sender.keyboard.press('Escape');
      await expect(sender.getByRole('button', { name: 'Choose a screen', exact: true })).toBeFocused();
    }
    await sender.getByRole('button', { name: 'Choose a screen', exact: true }).click();
    await sender.getByRole('dialog', { name: 'Choose a local screen', exact: true }).getByRole('button', { name: /screen · available/ }).click();
    await expect(receiver).toHaveURL(/\/play\//);
    const video = receiver.locator('video');
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThan(0);
    // Browser policy may require a receiver gesture; this is not a Cast/AirPlay claim.
    if (await video.evaluate((v: HTMLVideoElement) => v.paused)) await receiver.getByRole('button', { name: 'Play', exact: true }).click();
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThan(0);
    await sender.getByRole('button', { name: 'Pause remote screen' }).click();
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.paused)).toBe(true);
    await sender.getByRole('button', { name: 'Restart on remote screen' }).click();
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeLessThan(0.1);
    await sender.getByRole('button', { name: 'Stop remote playback' }).click();
    await expect(receiver).toHaveURL(/\/home$/);

    // The receiver survives route changes and can accept a new controller session.
    await sender.getByRole('button', { name: 'Choose a screen', exact: true }).click();
    await sender.getByRole('dialog', { name: 'Choose a local screen', exact: true }).getByRole('button', { name: /screen · available/ }).click();
    await expect(receiver).toHaveURL(/\/play\//);
    await receiverContext.close();
    await expect(sender.getByRole('region', { name: 'Local screens' })).toContainText('Remote screen disconnected');
    await sender.getByRole('button', { name: 'Choose a screen', exact: true }).click();
    await expect(sender.getByRole('dialog', { name: 'Choose a local screen', exact: true })).toContainText('No screens are available');
    await sender.keyboard.press('Escape');
    expect(errors).toEqual([]);
  } finally {
    await receiverContext.close();
  }
}

async function openTitle(page: Page) {
  await page.goto('/home');
  await page.getByRole('region', { name: 'New', exact: true }).getByRole('button', { name: /film blue horizon 2026/i }).click();
  await expect(page.getByRole('button', { name: 'Choose a screen', exact: true })).toBeVisible();
}
