# 像素字体

面板「像素字体」开关打开时用的两个字体。都是 SIL Open Font License 1.1，随文件附了各自的许可证全文——OFL 要求字体分发时带上许可证，别把 `*-LICENSE-OFL.txt` 删了。

| 文件 | 来源 | 版本 | 覆盖 |
| --- | --- | --- | --- |
| `Monocraft.woff2` | [IdreesInc/Monocraft](https://github.com/IdreesInc/Monocraft) | 4.2 | 拉丁、数字、ASCII 标点 |
| `FusionPixel12pxProportionalSC.woff2` | [TakWolf/fusion-pixel-font](https://github.com/TakWolf/fusion-pixel-font)（取自 npm `@fontsource/fusion-pixel-12px-proportional-sc@5.3.0`） | 12px proportional zh_hans | 汉字、全角标点，以及 Monocraft 没有的一切 |

两个都**未经改动**，Monocraft 只做了 TTF → WOFF2 的格式转换（它的版权行没有声明 Reserved Font Name，所以转换后仍叫 Monocraft）。要升级就重新下载原件、重新转换，不要在这里编辑字形。

为什么是这两个：Minecraft 自己的字体是 Mojang 的资产，不能进开源仓库。Monocraft 是照着它的字形做的开源替身，负责游戏味最重的那部分——英文和数字；它一个汉字都没有，而面板界面几乎全是中文，所以后面接一个覆盖全 CJK 的点阵字体。挑 Fusion Pixel 是因为它按 12px 网格设计，em 上的 cap 高度（900/1200）和面板原本的字体栈几乎一样，换字体不用动 `styles.css` 里那三百多处 `font-size`。

字体是被 `//go:embed` 打进单文件二进制的，所以体积直接算在发行物上：Monocraft 31 KB，Fusion Pixel 602 KB。往这里加第三个字体之前先想清楚这一点。
