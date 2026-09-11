import { useState } from 'react'

import { applyPref, current, type PixelFontPref } from '../pixelfont'
import { Page } from './Page'

/**
 * 外观 — one switch, and the place the next one goes.
 *
 * It is a settings page rather than a glyph next to the mode toggle in the
 * sidebar footer, and the reason is who needs it: the person reaching for this
 * is the person who finds the pixel face hard to read, and asking them to
 * recognise an unlabelled 18px icon would be a joke at their expense. 面板设置
 * is where this panel keeps the switches you flip once and forget, which is
 * exactly what this is.
 *
 * The mode toggle stays in the footer. It is not a flip-once setting — people
 * move between light and dark through a day — and moving it here would put a
 * daily control two navigations away for the sake of a tidy grouping.
 */
export function AppearanceSettings() {
  // Seeded from the attribute rather than the stored preference: the two differ
  // when storage is unreadable, and what the switch has to agree with is the
  // page in front of the reader.
  const [pref, setPref] = useState<PixelFontPref>(current)

  const set = (next: PixelFontPref) => {
    setPref(next)
    applyPref(next)
  }

  return (
    <Page
      title="外观"
      lead="面板长什么样。改了立刻生效，只存在这台设备的浏览器里，不跟着账号走，也不影响别人看到的面板。"
    >
      <section className="panel">
        <h2 className="panel__title">像素字体</h2>
        <label className="checkbox checkbox--stacked">
          <input
            type="checkbox"
            checked={pref === 'on'}
            onChange={(event) => set(event.target.checked ? 'on' : 'off')}
          />
          <span>页面文本使用像素字体</span>
          {/* JSX turns every newline in here into a space, so the breaks only
              ever fall after a 。 or after a Latin word. One after a 、 or a ，
              shows up as a gap; one inside a word shows up as a broken word. */}
          <small>
            英文和数字用 Monocraft，照着 Minecraft 的字形做的开源字体；汉字用缝合像素字体补上，因为前者一个汉字都没有。
            两个都是 SIL Open Font License，随二进制一起发，关掉就回到系统默认的无衬线字体。
            服务器控制台、主机 shell，以及面板里的路径、SHA、配置 diff
            不跟着变 —— 那些是一个字符一个字符去认的东西，换成点阵只会更难读。
          </small>
        </label>
      </section>
    </Page>
  )
}
