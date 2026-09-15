import { useEffect, useState } from 'react'

import {
  PALETTES,
  applyPref as applyPalette,
  current as currentPalette,
  type Palette,
} from '../palette'
import { applyPref, current, type PixelFontPref } from '../pixelfont'
import { current as currentTheme, onThemeChange } from '../theme'
import { Page } from './Page'
import { Section } from './Section'

/**
 * 外观 — the switches that decide what the panel looks like.
 *
 * It is a settings page rather than a glyph next to the mode toggle in the
 * sidebar footer, and the reason is who needs it: the pixel face is off out of
 * the box now, so the person reaching for this is the person who came looking
 * for it, and an unlabelled 18px icon is not something you go looking for.
 * 面板设置 is where this panel keeps the switches you flip once and forget,
 * which is exactly what this is.
 *
 * 配色 is here for the same test and one more. It is picked once; it needs five
 * labels a cycling glyph has no room for; and a colour is the one setting that
 * can show itself, which the swatches do — each one is painted by the scheme's
 * own tokens rather than by a copy of them.
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
  const [palette, setPalette] = useState<Palette>(currentPalette)
  // The swatches are painted by the scheme blocks, which are written per mode,
  // so each tile has to be told which mode to show — and told again when the
  // sidebar toggle, or the system under 跟随系统, changes it out from under it.
  const [mode, setMode] = useState(currentTheme)

  useEffect(() => onThemeChange(setMode), [])

  const set = (next: PixelFontPref) => {
    setPref(next)
    applyPref(next)
  }

  const pick = (next: Palette) => {
    setPalette(next)
    applyPalette(next)
  }

  return (
    <Page
      title="外观"
      lead="面板长什么样。改了立刻生效，只存在这台设备的浏览器里，不跟着账号走，也不影响别人看到的面板。"
    >
      {/* JSX turns every newline into a space, so the note's breaks fall after
          。 and nowhere else — one after a ，shows up as a gap in the sentence. */}
      <Section
        title="配色"
        note={
          <>
            四套配色，每套都有浅色和深色两份。
            换配色不动明暗，侧栏那个开关照旧管深浅，跟随系统也照旧。
          </>
        }
      >
        <div className="palettes">
          {PALETTES.map((item) => (
            <label
              key={item.id}
              className={`palettes__card${item.id === palette ? ' palettes__card--on' : ''}`}
            >
              <input
                type="radio"
                name="palette"
                checked={item.id === palette}
                onChange={() => pick(item.id)}
              />
              {/* Carries both attributes so the scheme's own token block paints
                  it; decorative, because the name beside it says the same thing
                  to anyone who cannot see the colours. */}
              <span
                className="palettes__swatch"
                data-theme={mode}
                data-palette={item.id}
                aria-hidden="true"
              />
              <span className="palettes__body">
                <span>{item.name}</span>
                <small>{item.note}</small>
              </span>
            </label>
          ))}
        </div>
        <p className="muted">
          服务器控制台和主机 shell 不跟着变。
          两块终端在任何配色下都保持深色、而且互相不同色，这是防止把危险命令敲进另一块终端的那道屏障。
          「运行中」「启动中」这类状态色同理 —— 它们要在扫一眼的时候还认得出来。
        </p>
      </Section>

      <Section title="像素字体">
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
      </Section>
    </Page>
  )
}
