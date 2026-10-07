import type { AppDownload } from "@exchange/core/platform/apps";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { Button } from "../components/Button";
import { QrCode } from "../data/QrCode";
import { AppCard, type AppCardLabels } from "./AppCard";
import { AppQrPanel } from "./AppQrPanel";

// The download page's cards (design 2026-10-07, App download page §4): an
// APK uploaded in the console, an App Store link and an enterprise iOS app,
// as the PC site (wide, with a QR code) and the phone (narrow) show them;
// then the PC top bar's QR panel.

const notes = { "zh-CN": "修复了行情页的偶发卡顿\n新增价格提醒", en: "Fixed an occasional stall\nPrice alerts" };
const apk: AppDownload = {
  mode: "FILE", url: "https://astras.vip/downloads/android/0192a000-0000-7000-8000-000000000001.apk", install_url: null, ios_install: null,
  package: "vip.astras.app", version: "1.2.0", build: "42", min_os: "24", size: 50_541_363, sha256: "9f".repeat(32), mobileconfig_url: null, notes,
  updated_at: "2026-10-07T03:00:00Z",
};
const store: AppDownload = {
  mode: "LINK", url: "https://apps.apple.com/app/id1234567890", install_url: null, ios_install: "APP_STORE", package: null, version: null, build: null,
  min_os: null, size: null, sha256: null, mobileconfig_url: null, notes: { "zh-CN": "", en: "" }, updated_at: "2026-10-07T03:00:00Z",
};
const ota: AppDownload = {
  ...apk,
  url: "https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000002.ipa",
  install_url: "itms-services://?action=download-manifest&url=https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000002.plist",
  ios_install: "OTA",
  min_os: "15.0",
  size: 61_203_456,
  sha256: "0c".repeat(32),
  mobileconfig_url: "https://astras.vip/downloads/ios/0192a000-0000-7000-8000-000000000003.mobileconfig",
};

const labels = (platform: string, kind: string, badge?: string): AppCardLabels => ({
  platform, kind, badge, version: "版本", updated: "更新时间", size: "大小", minOs: "系统要求", notes: "更新说明", help: "安装说明",
});
const androidSteps = ["下载完成后点按安装包进行安装。", "如果提示禁止安装未知来源的应用，请在系统设置里允许当前浏览器安装应用。", "可以用 SHA-256 核对下载的文件是否完整。"];
const iosSteps = ["在 Safari 中打开本页，点按「安装」并确认。", "回到主屏幕，等待图标下载完成。", "首次打开时在 设置 → 通用 → VPN 与设备管理 中信任该开发者。"];
const qr = (value: string, caption: string) => (
  <>
    <QrCode value={value} size={132} label={caption} />
    <span className="text-center text-xs leading-relaxed text-fg-3">{caption}</span>
  </>
);

const meta = {
  title: "Download/AppCard",
  component: AppCard,
  args: {
    platform: "android",
    app: apk,
    labels: labels("Android", "安装包（APK）"),
    minOs: "Android 7.0 及以上",
    notes: notes["zh-CN"],
    qr: qr("https://astras.vip/download?platform=android", "手机扫码下载"),
    actions: (
      <Button asChild size="lg">
        <a href={apk.url}>下载 APK</a>
      </Button>
    ),
    steps: androidSteps,
    className: "w-[560px]",
  },
} satisfies Meta<typeof AppCard>;
export default meta;

type Story = StoryObj<typeof meta>;

/** An APK uploaded in the console (PC): the QR code leads a phone to the download page. */
export const UploadedApk: Story = {};

/** An App Store link (PC): no file facts; the QR code is the link itself. */
export const AppStoreLink: Story = {
  args: {
    platform: "ios",
    app: store,
    labels: labels("iOS", "App Store"),
    minOs: null,
    notes: "",
    qr: qr(store.url, "手机扫码下载"),
    actions: (
      <Button asChild size="lg">
        <a href={store.url}>前往 App Store</a>
      </Button>
    ),
    steps: null,
  },
};

/** An enterprise iOS app on the PC: only a phone installs it, so the QR code says so and there is no button. */
export const EnterpriseIos: Story = {
  args: {
    platform: "ios",
    app: ota,
    labels: labels("iOS", "企业签名安装"),
    minOs: "iOS 15.0 及以上",
    qr: qr("https://astras.vip/download?platform=ios", "用 iPhone 或 iPad 扫码，在 Safari 中打开后安装"),
    actions: (
      <a href={ota.mobileconfig_url ?? ""} className="text-sm text-brand hover:underline">
        下载配置描述文件
      </a>
    ),
    steps: iosSteps,
  },
};

/** The phone's own platform (narrow): marked, its button primary, its steps open. */
export const PhoneOwn: Story = {
  args: {
    platform: "ios",
    app: ota,
    layout: "narrow",
    labels: labels("iOS", "企业签名安装", "本机"),
    minOs: "iOS 15.0 及以上",
    qr: undefined,
    actions: (
      <Button asChild size="lg" block>
        <a href={ota.install_url ?? ""}>安装</a>
      </Button>
    ),
    steps: iosSteps,
    stepsOpen: true,
    className: "w-[390px]",
  },
};

/** The other platform on the phone: a secondary button, its steps closed. */
export const PhoneOther: Story = {
  args: {
    layout: "narrow",
    qr: undefined,
    actions: (
      <Button asChild size="lg" block variant="secondary">
        <a href={apk.url}>下载 APK</a>
      </Button>
    ),
    className: "w-[390px]",
  },
};

/** The PC top bar's download panel: a QR code for each app and the way to the page. */
export const TopBarPanel: StoryObj<typeof AppQrPanel> = {
  render: () => (
    <div className="inline-block rounded-2 border border-line-1 bg-bg-1 p-4 shadow-pop">
      <AppQrPanel
        title="扫码下载 App"
        codes={[
          { key: "android", value: "https://astras.vip/download?platform=android", caption: "Android" },
          { key: "ios", value: store.url, caption: "iOS" },
        ]}
        footer={
          <a href="/download" className="flex items-center justify-center rounded-2 bg-bg-2 py-2 text-sm text-fg-1">
            更多下载方式
          </a>
        }
      />
    </div>
  ),
};
