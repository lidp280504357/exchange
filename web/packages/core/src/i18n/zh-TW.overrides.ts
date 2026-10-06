// The term table of the Traditional Chinese resources (design 2026-10-06
// 繁体中文 §2.2), laid over OpenCC's Simplified → Taiwan conversion
// (s2twp) by scripts/gen-zh-tw.mjs. The wording follows Binance's
// Traditional Chinese site: 「數據」for market and contract data,
// 「資料」for personal data (资料 becomes it by itself). Both sites share
// the table, so they word things alike. After a change run pnpm i18n
// (task web:i18n) and commit what it writes.

export const zhTWOverrides = {
  /**
   * A Simplified phrase → its Traditional wording, in place of OpenCC's.
   * They join OpenCC's own phrases, the longest match winning, so a longer
   * phrase it knows still goes its way (数据库 → 資料庫).
   */
  phrases: [
    ["数据", "數據"],
    ["数字资产", "數位資產"],
    ["数字货币", "數位貨幣"],
    ["邮箱", "電子郵件"],
    ["手机号码", "手機號碼"],
    ["手机号", "手機號碼"],
    ["用户", "用戶"],
    ["账本", "帳本"],
    ["退出登录", "登出"],
    // An account closed for good, not a sign-out.
    ["注销", "註銷"],
    ["绑定", "綁定"],
    ["发布", "發布"],
    ["审核", "審核"],
    ["查看", "查看"],
    ["了解", "了解"],
    ["一目了然", "一目了然"],
    ["获取", "取得"],
    ["重置", "重設"],
    ["公布", "公布"],
    ["高级", "進階"],
    ["权限", "權限"],
    ["类型", "類型"],
    ["项目", "項目"],
    ["对象", "對象"],
    // A coin's symbol.
    ["代码", "代碼"],
    ["智能", "智能"],
    // The withdrawal form's address field, not a browser's.
    ["地址栏", "地址欄"],
    // Filecoin's Chinese name.
    ["文件币", "文件幣"],
  ],
  /** A Traditional character → the form written instead, everywhere. */
  characters: [
    // 平台、後台: the common form.
    ["臺", "台"],
    // 帳戶、帳號、對帳、轉帳: Taiwan's form.
    ["賬", "帳"],
  ],
} as const;
