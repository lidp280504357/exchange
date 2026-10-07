// Generated from apps/m/src/i18n/profile.ts by
// packages/core/scripts/gen-zh-tw.mjs (OpenCC s2twp and the term table
// packages/core/src/i18n/zh-TW.overrides.ts); do not edit: change the
// source or the term table and run pnpm i18n (task web:i18n).

export default {
  mProfile: {
    title: "個人資料",
    avatar: {
      change: "更換頭像",
      remove: "恢復預設",
      hint: "PNG、JPEG 或 WebP 圖片，取中間的正方形",
      builtIn: "現在是系統預設頭像",
      removeTitle: "恢復預設頭像？",
      removeDesc: "上傳的頭像會被刪除，改用系統預設頭像，之後可以隨時重新上傳。",
      removed: "已恢復預設頭像",
      uploaded: "頭像已更新",
      preparing: "正在處理圖片…",
      uploading: "正在上傳",
      saving: "正在儲存…",
      cancel: "取消上傳",
      problems: {
        type: "只支援 PNG、JPEG、WebP 格式的圖片",
        large: "圖片太大，請選擇 20 MB 以內的圖片",
        small: "圖片太小：寬和高都至少 64 畫素",
        decode: "無法讀取這張圖片，請換一張",
      },
    },
    username: {
      title: "使用者名稱",
      cooldown: "{{time}} 後可再次修改",
      sheetTitle: "修改使用者名稱",
      label: "新使用者名稱",
      placeholder: "例如 satoshi_n",
      hint: "3–20 個字母、數字或下劃線，不能以下劃線開頭；修改後 7 天內不能再改",
      save: "儲存",
      saved: "使用者名稱已修改",
      problems: {
        chars: "只能包含字母、數字和下劃線",
        start: "不能以下劃線開頭",
        length: "長度為 3–20 個字元",
        reserved: "這是保留名稱，請換一個",
        same: "與現在的使用者名稱相同",
      },
    },
    uidCopied: "UID 已複製",
    joined: "註冊時間",
    locked: "帳戶已凍結，暫時不能修改資料",
    note: "頭像和使用者名稱會顯示在你的帳戶裡，登入仍使用電子郵件或手機號碼。",
  },
};
