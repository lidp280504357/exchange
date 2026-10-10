import { i18n } from "@exchange/core";
import { merge } from "./i18n";
import { appsEn, appsZh } from "./messages/apps";
import { attemptsEn, attemptsZh } from "./messages/attempts";
import { coinmEn, coinmZh } from "./messages/coinm";
import { consoleAccessEn, consoleAccessZh } from "./messages/consoleAccess";
import { contentEn, contentZh } from "./messages/content";
import { custodyEn, custodyZh } from "./messages/custody";
import { flagsEn, flagsZh } from "./messages/flags";
import { houseEn, houseZh } from "./messages/house";
import { identityEn, identityZh } from "./messages/identity";
import { changesEn, changesZh, instrumentsEn, instrumentsZh, profileEn, profileZh } from "./messages/instruments";
import { marginEn, marginZh } from "./messages/margin";
import { moneyEn, moneyZh } from "./messages/money";
import { pagesEn, pagesZh } from "./messages/pages";
import { platformEn, platformZh } from "./messages/platform";
import { productsEn, productsZh } from "./messages/products";
import { reportsEn, reportsZh } from "./messages/reports";
import { simEn, simZh } from "./messages/sim";
import { simTargetEn, simTargetZh } from "./messages/simTarget";
import { summaryEn, summaryZh } from "./messages/summary";
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
    custodyZh, houseZh, simTargetZh, platformZh, pagesZh, marginZh, coinmZh, appsZh, identityZh, productsZh, summaryZh, flagsZh, consoleAccessZh,
  ),
  en: merge(
    {}, usersEn, walletEn, moneyEn, tradingEn, instrumentsEn, systemEn, changesEn, contentEn, reportsEn, profileEn, simEn, attemptsEn, unownedEn,
    custodyEn, houseEn, simTargetEn, platformEn, pagesEn, marginEn, coinmEn, appsEn, identityEn, productsEn, summaryEn, flagsEn, consoleAccessEn,
  ),
};

/** registerPageMessages adds the pages' strings to both languages (deeply, over the entry's). */
export function registerPageMessages() {
  for (const [lng, bundle] of Object.entries(pageMessages)) i18n.addResourceBundle(lng, "translation", bundle, true, true);
}
