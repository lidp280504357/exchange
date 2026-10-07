// Generated from apps/pc/src/i18n/download.ts by
// packages/core/scripts/gen-zh-tw.mjs (OpenCC s2twp and the term table
// packages/core/src/i18n/zh-TW.overrides.ts); do not edit: change the
// source or the term table and run pnpm i18n (task web:i18n).

export default {
  pcDownload: {
    title: "下載 {{brand}} App",
    subtitle: "在手機上看行情、交易與管理資產，帳戶與網頁版通用",
    none: "暫未提供 App",
    noneHint: "用手機瀏覽器開啟本站，會自動進入手機版",
    platforms: { android: "Android", ios: "iOS" },
    kinds: { androidFile: "安裝包（APK）", androidLink: "下載頁", iosOta: "企業簽名安裝", iosStore: "App Store" },
    version: "版本",
    updated: "更新時間",
    size: "大小",
    minOs: "系統要求",
    minAndroid: "Android {{version}} 及以上",
    minAndroidApi: "Android API {{level}} 及以上",
    minIos: "iOS {{version}} 及以上",
    notes: "更新說明",
    downloadApk: "下載 APK",
    open: "前往下載",
    appStore: "前往 App Store",
    install: "安裝",
    scan: "手機掃碼下載",
    qrLabel: "{{platform}} 下載二維碼",
    copySha: "複製 SHA-256",
    scanIos: "用 iPhone 或 iPad 掃碼，在 Safari 中開啟後安裝",
    mobileconfig: "下載配置描述檔案",
    help: "安裝說明",
    helpAndroid: {
      s1: "下載完成後點按安裝包進行安裝。",
      s2: "如果提示禁止安裝未知來源的應用，請在系統設定裡允許當前瀏覽器安裝應用，再點按一次安裝包。",
      s3: "可以用上面的 SHA-256 核對下載的檔案是否完整。",
    },
    helpIos: {
      s1: "在 iPhone 或 iPad 上用 Safari 開啟本頁，點按「安裝」並在彈窗中確認。",
      s2: "回到主螢幕，等待圖示下載完成。",
      s3: "首次開啟如提示「未受信任的企業級開發者」，前往 設定 → 通用 → VPN 與裝置管理，信任該開發者後再開啟。",
    },
    menu: { title: "掃碼下載 App", more: "更多下載方式" },
  },
};
