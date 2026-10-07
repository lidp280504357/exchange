import { registerMessages } from "@exchange/core";
import { preloadable } from "@exchange/ui";

// The top bar's download panel in a chunk of its own, with its strings:
// the QR code library stays off every page's first screen; the pointer or
// the focus reaching the download button starts loading it.
export const DownloadQrs = preloadable(
  () =>
    Promise.all([import("./DownloadQrs"), import("../../i18n/download").then((m) => registerMessages(m.default))]).then(([m]) => m),
  (m) => m.DownloadQrs,
);
