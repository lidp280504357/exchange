// The strings of one's own password and authenticator and of the one-time
// setup links (C5.5 ⑪), merged into the console's messages in i18n.ts.
export const accountZh = {
  admin: {
    nav: { account: "账号与安全" },
    perm: { audit_export: "导出审计日志（含邮箱与 IP）" },
    account: {
      help: "你自己的口令与身份验证器。修改时用当前口令确认身份，修改后你在其它设备上的会话会结束。",
      password: "口令", totp: "身份验证器", current: "当前口令", newPassword: "新口令（至少 {{n}} 位）",
      changePassword: "修改口令", passwordChanged: "口令已修改，其它会话已结束",
      passwordHint: "口令只有你自己知道；忘记时请另一位管理员（ADMIN）重置，你会收到一次性设置链接。",
      oldCode: "当前身份验证器上的 6 位验证码", startTotp: "更换身份验证器",
      totpHint: "更换需要当前口令和当前身份验证器的验证码；旧的身份验证器丢失时，请另一位管理员（ADMIN）重置。",
      bind: "绑定", bindHint: "请在 10 分钟内完成绑定；在此之前旧的身份验证器仍然有效。",
      totpChanged: "身份验证器已更换，其它会话已结束",
      mustTitle: "请先修改口令",
      mustText: "{{email}} 的口令是生成后交给你的。改成只有你知道的口令（至少 12 位）后才能使用后台；其它会话会结束。",
    },
    setup: {
      title_CREATE: "设置你的管理员账号", title_PASSWORD: "设置新口令", title_TOTP: "绑定新的身份验证器",
      until: "这个链接 {{time}} 前有效，只能使用一次。",
      password: "口令（至少 {{n}} 位）", again: "再输入一次", short: "至少 {{n}} 位", mismatch: "两次输入不一致",
      scan: "用身份验证器 App（如 Google Authenticator、1Password）扫描二维码，或手动输入密钥：",
      secret: "身份验证器密钥", code: "身份验证器上显示的 6 位验证码", submit: "完成设置",
      hint: "口令与身份验证器只有你自己知道；把链接交给你的人看不到它们。",
      done: "设置完成", donePassword: "现在可以用新口令和身份验证器登录。", doneTotp: "现在可以用新的身份验证器登录。",
      toLogin: "去登录", invalidTitle: "链接不可用",
      invalid: "这个设置链接不存在、已经用过或已过期。请联系管理员重新重置，获取新的链接。",
      missing: "链接不完整，请把收到的整个链接粘贴到地址栏。",
    },
  },
  errors: {
    ADMIN_SETUP_INVALID: "设置链接不存在、已经用过或已过期",
    ADMIN_PASSWORD_CHANGE_REQUIRED: "请先修改口令",
    ADMIN_PASSWORD_WRONG: "当前口令不正确",
    ADMIN_TOTP_CODE_WRONG: "验证码不正确或已用过，请核对手机时间后等下一个验证码",
  },
};

export const accountEn = {
  admin: {
    nav: { account: "Account & security" },
    perm: { audit_export: "Export the audit trail (with email and IP addresses)" },
    account: {
      help: "Your own password and authenticator. Your current password confirms a change; your sessions on other devices end with it.",
      password: "Password", totp: "Authenticator", current: "Current password", newPassword: "New password (at least {{n}} characters)",
      changePassword: "Change password", passwordChanged: "Password changed; your other sessions ended",
      passwordHint: "Only you know your password; if you forget it, another ADMIN resets it and you receive a one-time setup link.",
      oldCode: "The 6-digit code of your current authenticator", startTotp: "Change authenticator",
      totpHint: "A change needs your current password and a code from your current authenticator; if you lost it, another ADMIN resets it.",
      bind: "Bind", bindHint: "Bind it within 10 minutes; until then your old authenticator still works.",
      totpChanged: "Authenticator changed; your other sessions ended",
      mustTitle: "Change your password first",
      mustText: "The password of {{email}} was generated and handed to you. Change it to one only you know (at least 12 characters) to use the console; your other sessions end.",
    },
    setup: {
      title_CREATE: "Set up your administrator account", title_PASSWORD: "Set a new password", title_TOTP: "Bind a new authenticator",
      until: "This link works once, until {{time}}.",
      password: "Password (at least {{n}} characters)", again: "Again", short: "At least {{n}} characters", mismatch: "The two do not match",
      scan: "Scan the QR code with an authenticator app (such as Google Authenticator or 1Password), or type the secret:",
      secret: "Authenticator secret", code: "The 6-digit code the authenticator shows", submit: "Finish setup",
      hint: "Only you know your password and authenticator; whoever handed you the link does not see them.",
      done: "All set", donePassword: "Sign in with your new password and authenticator.", doneTotp: "Sign in with your new authenticator.",
      toLogin: "Sign in", invalidTitle: "This link cannot be used",
      invalid: "The setup link is unknown, used or expired. Ask an administrator to reset your account again for a new one.",
      missing: "The link is incomplete; paste the whole link you received into the address bar.",
    },
  },
  errors: {
    ADMIN_SETUP_INVALID: "The setup link is unknown, used or expired",
    ADMIN_PASSWORD_CHANGE_REQUIRED: "Change your password first",
    ADMIN_PASSWORD_WRONG: "The current password is wrong",
    ADMIN_TOTP_CODE_WRONG: "Wrong or used code; check your phone's clock and wait for the next code",
  },
};
