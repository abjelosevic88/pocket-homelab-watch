// README screenshots and a browser smoke test, against the demo library (scripts/demo-library/make.sh: a
// throwaway Jellyfin with Blender open movies, silent-era public-domain films and three series) and a
// Pocket Watch started with DEMO_DATA=true:
//
//   scripts/demo-library/make.sh
//   DATA_DIR=~/.cache/pocket-watch-dev/app DEMO_DATA=true ADMIN_EMAIL=ana@example.com ADMIN_PASSWORD=demo-password \
//     ADMIN_NAME="Ana Petrović" JELLYFIN_URL=http://localhost:18096 JELLYFIN_API_KEY=<key> JELLYFIN_USER=demo \
//     PORT=18081 ./pocket-watch
//   BASE=http://127.0.0.1:18081 node scripts/screenshots.mjs                 # Playwright's own Chromium
//   CDP=http://127.0.0.1:9233 BASE=… node scripts/screenshots.mjs            # or any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                   # checks only
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18081'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/409|Failed to load resource|MEDIA_ERR|NotSupportedError/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'ana@example.com', password: 'demo-password' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
const settle = page => page.waitForTimeout(1800)
async function go(page, path) { await page.goto(base + path, { waitUntil: 'networkidle' }); await page.evaluate(() => document.fonts.ready); await settle(page) }
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const api = (page, path, init) => page.evaluate(([p, i]) => fetch('/api/v1' + p, i).then(r => r.json()), [path, init])
async function idOf(page, name, type) {
  const r = await api(page, `/browse?type=${type}&q=${encodeURIComponent(name)}`)
  assert.ok(r.items.length > 0, `${name} is in the demo library`)
  return r.items[0].id
}

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/')
  assert.ok(await page.locator('.feature').isVisible(), 'the feature banner on Home')
  assert.ok(await page.locator('.shelf').count() >= 3, 'shelves on Home')
  await page.waitForFunction(() => [...document.querySelectorAll('.shelf .art')].filter(a => a.classList.contains('loaded')).length >= 6, null, { timeout: 15000 })
  await shot(page, 'desktop-home')

  await go(page, '/films?sort=title')
  assert.ok(await page.locator('.grid-posters .tile').count() >= 15, 'the films grid')
  await shot(page, 'desktop-films')

  const show = await idOf(page, 'Bonanza', 'series')
  await go(page, `/title/${show}`)
  assert.ok(await page.locator('.episode').count() >= 5, 'episodes on the show page')
  await page.evaluate(() => document.querySelector('.content')?.scrollTo({ top: 0 }))
  await shot(page, 'desktop-show')

  const film = await idOf(page, 'Sintel', 'movie')
  await go(page, `/title/${film}`)
  assert.ok(await page.locator('.title-actions .btn.primary').isVisible(), 'a play button on the film page')
  await shot(page, 'desktop-film')

  // playing: the server answers with a stream, progress goes back to Jellyfin and into history
  await page.click('.title-actions .btn.primary')
  await page.waitForURL(/\/play\//)
  await page.waitForSelector('.player-video')
  const started = await page.waitForFunction(() => { const v = document.querySelector('video'); return v && (v.currentTime > 1 || v.error) ? (v.error ? 'error ' + v.error.code : 'playing') : null }, null, { timeout: 30000 }).then(h => h.jsonValue()).catch(() => 'timeout')
  console.log('player:', started)
  if (started === 'playing') {
    await page.waitForTimeout(3000)
    const now = await api(page, '/dashboard')
    assert.ok(now.now.length > 0, 'the play shows up as playing now')
  }
  const subs = await page.evaluate(() => document.querySelectorAll('video track').length)
  assert.ok(subs >= 1, 'the Sintel subtitle track is offered')
  await page.mouse.move(700, 500)
  await page.mouse.move(720, 520)
  await page.waitForTimeout(400)
  await shot(page, 'desktop-player')
  await page.click('.player-top .pbtn')
  await page.waitForTimeout(800)

  await go(page, '/stats')
  await page.click('text=3 months')
  await settle(page)
  assert.ok(await page.locator('.stat-tile').count() >= 3, 'stat tiles')
  await shot(page, 'desktop-stats')

  await go(page, '/history')
  assert.ok(await page.locator('.h-row').count() >= 5, 'history rows')
  await shot(page, 'desktop-history')

  await go(page, '/')
  await page.keyboard.press('w')
  await page.keyboard.type('something animated and short')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.pick', { timeout: 30000 })
  await page.waitForTimeout(1200)
  await shot(page, 'desktop-tonight')
  await page.keyboard.press('Escape')

  await go(page, '/settings/jellyfin')
  assert.ok(await page.locator('.conn-status.ok').isVisible(), 'Jellyfin connected in Settings')
  await shot(page, 'desktop-settings')
  await ctx.close()
}

// ---- dark ----
{
  const { ctx, page } = await session({ dark: true })
  const film = await idOf(page, 'Nosferatu', 'movie')
  await go(page, `/title/${film}`)
  await shot(page, 'desktop-film-dark')
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/')
  await shot(page, 'phone-home')
  const show = await idOf(page, 'Pioneer One', 'series')
  await go(page, `/title/${show}`)
  await shot(page, 'phone-show')
  await ctx.close()
}

await browser.close().catch(() => {})
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('ok')
