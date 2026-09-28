import type { zhCN } from "./zh-CN";

type Shape<T> = { [K in keyof T]: T[K] extends string ? string : Shape<T[K]> };

export const en: Shape<typeof zhCN> = {
  app: { name: "Exchange", tagline: "A learning exchange (test environment, simulated funds)" },
  nav: { assets: "Assets", markets: "Markets", transfer: "Transfer", notifications: "Notices", security: "Security", profile: "Settings", logout: "Sign out" },
  common: {
    submit: "Submit", next: "Next", back: "Back", cancel: "Cancel", confirm: "Confirm", loading: "Loading…", retry: "Retry",
    email: "Email", password: "Password", code: "Code", sendCode: "Send code", resend: "Resend", device: "Device", time: "Time",
    none: "Nothing yet", copy: "Copy", more: "Load more", ok: "Done",
  },
  auth: {
    login: "Sign in", register: "Sign up", noAccount: "No account? Sign up", haveAccount: "Have an account? Sign in", forgot: "Forgot password",
    identifier: "Email or phone (with country code)", newPassword: "Password (10–128 characters, not a common one)", country: "Country (two letters, e.g. SG)",
    agree: "I accept the terms and the risk disclosure (versions {{terms}} / {{risk}})", codeSent: "Code sent to {{target}}, valid 5 minutes", codeSentBound: "Code sent to your email, valid 5 minutes",
    challengeTitle: "Confirm sign-in", challengeHint: "It has been over 7 days since your last sign-in: confirm with a code.", resetTitle: "Reset password",
    resetDone: "Password reset and every device signed out. Sign in again; withdrawals are reviewed for 24 hours.", captcha: "Please complete the check",
  },
  assets: {
    title: "Assets", spot: "Spot", futures: "Futures", asset: "Asset", available: "Available", frozen: "Frozen", total: "Total",
    empty: "No balances yet. New test accounts get simulated funds.", live: "Live",
  },
  markets: { title: "Markets", pair: "Pair", status: "Status", tick: "Tick", lot: "Lot", minNotional: "Min notional", fees: "Maker / taker" },
  transfer: {
    title: "Transfer", from: "From", to: "To", amount: "Amount", max: "Max", done: "Transferred", history: "History",
    swap: "Swap", status: { COMPLETED: "Completed", FAILED: "Failed" },
  },
  ledger: { title: "Fund flow", type: "Type" },
  codes: {
    SPOT: "Spot", FUTURES: "Futures", AVAILABLE: "Available", FROZEN: "Frozen",
    MANUAL_ADJUSTMENT: "Adjustment (simulated funds)", ACCOUNT_TRANSFER: "Transfer", ORDER_FREEZE: "Order hold", ORDER_UNFREEZE: "Order release",
    TRADE_SETTLE: "Trade", TRADE_FEE: "Fee", DEPOSIT_CREDIT: "Deposit", WITHDRAW_FREEZE: "Withdrawal hold", WITHDRAW_SETTLE: "Withdrawal",
    WITHDRAW_UNFREEZE: "Withdrawal release", INTERNAL_TRANSFER: "Internal transfer",
    PASSWORD: "Password", OTP: "Code", LOGIN_CHALLENGE: "Sign-in check", REGISTER: "Sign-up",
    SUCCESS: "Success", FAILED_PASSWORD: "Wrong password", LOCKED: "Locked", CHALLENGE_REQUIRED: "Check required",
    WEB: "Web", APP: "App", ACTIVE: "Active", RISK_REVIEW: "Under review", FROZEN_ACCOUNT: "Frozen", CLOSED: "Closed",
    PREPARE: "Coming soon", TRADING: "Trading", HALT: "Halted", CANCEL_ONLY: "Cancel only", DELISTED: "Delisted",
  },
  notices: { title: "Notifications", markAll: "Mark all read", unread: "{{n}} unread" },
  security: {
    title: "Security", sessions: "Devices", current: "This device", revoke: "Sign out", revokeOthers: "Sign out all other devices",
    history: "Sign-in history", stepUp: "Security check", stepUpHint: "Confirm it is you first; we will email you a code.", method: "Method", result: "Result",
    newDevice: "New device",
  },
  profile: { title: "Settings", language: "Language", timezone: "Time zone", antiPhishing: "Anti-phishing code", antiPhishingHint: "Every mail we send will show it.", save: "Save", saved: "Saved", status: "Account status" },
  errors: {
    AUTH_PASSWORD_INVALID: "Wrong account or password", AUTH_PASSWORD_WEAK: "Too weak: 10+ characters, not common, no runs or repeats, no email in it",
    AUTH_ACCOUNT_LOCKED: "Too many failures, try again later", AUTH_CAPTCHA_REQUIRED: "Complete the human check first", AUTH_CAPTCHA_FAILED: "The human check failed, try again",
    AUTH_OTP_INVALID: "Wrong code", AUTH_OTP_EXPIRED: "The code expired, request a new one", AUTH_OTP_ATTEMPTS_EXCEEDED: "Too many wrong codes, request a new one",
    AUTH_OTP_RESEND_TOO_SOON: "Please wait before requesting another code", AUTH_TICKET_INVALID: "The verification expired, request a new code",
    AUTH_CHANNEL_UNAVAILABLE: "SMS is unavailable, use email", AUTH_TERMS_OUTDATED: "The terms changed: reload and accept them again",
    AUTH_IDENTITY_TAKEN: "That email or phone is taken", AUTH_STEP_UP_REQUIRED: "Complete the security check first", AUTH_SESSION_REVOKED: "Signed out, please sign in again",
    AUTH_LOGIN_CHALLENGE_INVALID: "The sign-in check expired, sign in again", USER_CLOSED: "The account is closed", USER_FROZEN: "The account is frozen and read-only",
    USER_RISK_REVIEW: "The account is under review", USER_NOT_ELIGIBLE: "Not available yet", USER_REGION_NOT_ALLOWED: "Not available in your region",
    LEDGER_INSUFFICIENT_BALANCE: "Insufficient available balance", LEDGER_AMOUNT_PRECISION: "Too many decimals for this asset", COMMON_RATE_LIMITED: "Too many requests, slow down",
    COMMON_IDEMPOTENCY_CONFLICT: "Duplicate submission, reload and retry", COMMON_INVALID_ARGUMENT: "Invalid input", COMMON_UNAVAILABLE: "Temporarily unavailable, try again",
    format: "Enter a valid number", zero: "The amount must be above 0", precision: "At most {{n}} decimals", unknown: "Something went wrong ({{code}})",
  },
};
