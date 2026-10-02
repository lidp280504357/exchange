# 币种 logo 来源

`deploy/instruments/fetch-logos.sh` 在测试服上下载并经 `exchangectl instruments profile <CODE> --logo` 上传（2026-10-02，88 个资产；`report.txt` 是当次的清单：资产、格式、来源、字节数、原始地址）。再跑一次只补没有 logo 的资产，`FORCE=1` 才整体替换，运营在后台上传的 logo 不会被覆盖。

| 来源 | 许可 | 用于 |
|---|---|---|
| [spothq/cryptocurrency-icons](https://github.com/spothq/cryptocurrency-icons)（`svg/color`） | CC0 1.0 | 45 个老牌币的扁平彩色 SVG（`1000PEPE` 等千倍计价资产映射到原币；`POL` 用 `matic`，`RENDER` 用 `rndr`） |
| [CoinGecko](https://www.coingecko.com) 币种图片（市值前 1,000 按符号匹配） | 各项目自有品牌图，仅作标识用途 | 43 个较新的币（JPEG/非正方形的经 `python3-pil` 转为透明边距的正方形 PNG，最长边 ≤ 256 px） |
| `deploy/instruments/astra.svg` | 本项目自绘 | 平台币 ASTRA（`scripts/ops/astra.sh profile` 上传） |

服务端规则（ASTRA 设计 §5.3）：PNG/SVG/WebP、正方形、≤ 200 KB；SVG 入库时按白名单重建。
