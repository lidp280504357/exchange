import { i18n } from "@exchange/core";
import { merge } from "./i18n";
import { attemptsEn, attemptsZh } from "./messages/attempts";
import { contentEn, contentZh } from "./messages/content";
import { custodyEn, custodyZh } from "./messages/custody";
import { houseEn, houseZh } from "./messages/house";
import { changesEn, changesZh, instrumentsEn, instrumentsZh, profileEn, profileZh } from "./messages/instruments";
import { marginEn, marginZh } from "./messages/margin";
import { moneyEn, moneyZh } from "./messages/money";
import { pagesEn, pagesZh } from "./messages/pages";
import { platformEn, platformZh } from "./messages/platform";
import { reportsEn, reportsZh } from "./messages/reports";
import { simEn, simZh } from "./messages/sim";
import { simTargetEn, simTargetZh } from "./messages/simTarget";
import { systemEn, systemZh } from "./messages/system";
import { tradingEn, tradingZh } from "./messages/trading";
import { unownedEn, unownedZh } from "./messages/unowned";
import { usersEn, usersZh } from "./messages/users";
import { walletEn, walletZh } from "./messages/wallet";

// The pages' strings, out of the sign-in's chunk (A40): the signed-in
// console registers them when its chunk loads, before anything renders.

const pageMessages = {
  "zh-CN": merge(
    {}, usersZh, walletZh, moneyZh, tradingZh, instrumentsZh, systemZh, changesZh, contentZh, reportsZh, profileZh, simZh, attemptsZh, unownedZh,
    custodyZh, houseZh, simTargetZh, platformZh, pagesZh, marginZh,
  ),
  en: merge(
    {}, usersEn, walletEn, moneyEn, tradingEn, instrumentsEn, systemEn, changesEn, contentEn, reportsEn, profileEn, simEn, attemptsEn, unownedEn,
    custodyEn, houseEn, simTargetEn, platformEn, pagesEn, marginEn,
  ),
};

/** registerPageMessages adds the pages' strings to both languages (deeply, over the entry's). */
export function registerPageMessages() {
  for (const [lng, bundle] of Object.entries(pageMessages)) i18n.addResourceBundle(lng, "translation", bundle, true, true);
}
