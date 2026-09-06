// Run in playwright-cli against the live demo; see README's responsive checks.
async (page) => {
  page.setDefaultTimeout(10000);
  const origin = await page.evaluate(() => location.origin);
  const status = await (await page.request.get(origin + '/api/v1/setup/status')).json();
  if (!status.demo) throw Error('Responsive capture requires the isolated demo server');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto(origin + '/profiles');
  await page.getByRole('button', { name: 'Alex Ready' }).click();
  const results = [];
  const bounds = async (name) => {
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
    if (overflow) throw Error(name + ': page overflows horizontally');
    for (const button of await page.locator('header button').all()) {
      const box = await button.boundingBox();
      if (box && (box.x < -1 || box.x + box.width > page.viewportSize().width + 1)) throw Error(name + ': navigation is clipped');
    }
  };
  for (const [name, width, height] of [['small-mobile',320,740],['mobile',390,844],['tablet',768,1024],['tablet-landscape',1024,768],['desktop',1440,900],['full-hd',1920,1080],['4k',3840,2160],['8k',7680,4320],['desktop-return',1920,1080]]) {
    await page.setViewportSize({ width, height });
    await page.getByLabel('View', { exact: true }).selectOption('rows');
    await page.locator('[data-layout="rail"]').first().waitFor();
    await page.waitForFunction(() => {
      const unit = parseFloat(getComputedStyle(document.documentElement).fontSize);
      const card = document.querySelector('[data-card]')?.getBoundingClientRect();
      return card && card.width >= 9.0625 * unit - 1 && card.width <= 13.75 * unit + 1;
    });
    await bounds(name);
    const metrics = await page.evaluate(() => ({ width: innerWidth, font: parseFloat(getComputedStyle(document.documentElement).fontSize), poster: document.querySelector('[data-card]').getBoundingClientRect().width }));
    if (Math.abs(metrics.font - Math.max(16, Math.min(64, width / 120))) > 0.1) throw Error(name + ': incorrect type scale');
    results.push({ name, ...metrics });
    await page.getByRole('button', { name: /^View details for / }).click();
    const detail = page.getByRole('dialog');
    await detail.waitFor();
    const box = await detail.boundingBox();
    if (!box || box.x < -1 || box.y < -1 || box.x + box.width > width + 1 || box.y + box.height > height + 1) throw Error(name + ': detail leaves viewport');
    await page.getByRole('button', { name: 'Close details' }).click();
    await page.getByLabel('View', { exact: true }).selectOption('grid');
    await page.locator('[data-layout="grid"]').waitFor();
    await bounds(name + ' grid');
    await page.locator('[data-layout="grid"]').scrollIntoViewIfNeeded();
    await page.locator('[data-layout="grid"]').evaluate(element => { element.scrollTop = element.scrollHeight; });
    await page.waitForFunction(() => [...document.querySelectorAll('[data-layout="grid"] [data-card]')].some(card => { const r=card.getBoundingClientRect(); return r.bottom > 0 && r.top < innerHeight; }));
    await page.getByLabel('View', { exact: true }).selectOption('rows');
  }
  for (const width of [320,768,1440,3840,7680]) {
    await page.setViewportSize({width,height:width < 1000 ? 1024 : width * 9/16});
    for (const route of ['/profiles','/login']) {
      await page.goto(origin + route);
      await page.locator('[data-app-content]:not([inert])').waitFor();
      await bounds(route + ' ' + width);
    }
  }
  await page.setViewportSize({width:1440,height:900});
  await page.goto(origin + '/profiles');
  await page.getByRole('button', {name:'Alex Ready'}).click();
  return results;
}
