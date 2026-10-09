import type { Meta, StoryObj } from "@storybook/react-vite";
import { PlatformLogo } from "../download/PlatformLogo";
import { QrCode } from "./QrCode";

const meta = {
  title: "Data/QrCode",
  component: QrCode,
  args: { value: "0x61b1cB5D2A5C1f4f9aE0b2d4d0E5F3c2A1b73315", label: "Deposit address" },
} satisfies Meta<typeof QrCode>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Address: Story = {};

/** An authenticator binding link, larger. */
export const Otpauth: Story = {
  args: { value: "otpauth://totp/Astras:ann%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Astras", size: 200, label: "Authenticator" },
};

/** An app's download link with its platform's mark in a badge at the centre, drawn at error correction H (F25). */
export const WithLogo: Story = {
  args: { value: "https://astras.vip/download?platform=android", size: 132, label: "Android 下载二维码", logo: <PlatformLogo platform="android" /> },
};
