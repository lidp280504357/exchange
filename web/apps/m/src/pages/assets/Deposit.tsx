import { errorText, routes } from "@exchange/core";
import { useDepositAddress, useWalletNetworks, useWalletPushes, walletKeys } from "@exchange/core/wallet/hooks";
import { assetsFor, explorerUrl, isTestnet, networksFor, sortAssets, walletFlow, type WalletNetwork } from "@exchange/core/wallet/networks";
import { ErrorState, KeyValue, QrCode, Skeleton, SkeletonLines } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Clock, ExternalLink, ShieldAlert } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { AddressValue, CopyAction, CopyIcon, RETRY } from "./parts/bits";
import { CoinList, InternalOnly } from "./parts/Coins";
import { plainAmount, useAssetMeta, useTradeLinks } from "./parts/meta";
import { EligibilityNotice, Notice } from "./parts/Notice";
import { PullToRefresh } from "../../components/PullToRefresh";
import { DepositList } from "./parts/Records";
import { ChosenCoin, ChosenNetwork, NetworkCards, StepBlock, useEtaText } from "./parts/Steps";

/**
 * Deposit (design §7.2, §6.2): three steps top to bottom (coin → network
 * → address), built from GET /v1/wallet/networks so new networks appear by
 * themselves; a large QR code, the address with its copy buttons, the red
 * wrong-network warning and the minimum; below, the recent deposits, moved
 * live by the pushes. The choice lives in the URL (?asset=&network=).
 */
export default function Deposit() {
  const { t } = useTranslation();
  const title = t("nav.deposit");
  usePageHeader({ title, back: routes.assets }, [title]);
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const meta = useAssetMeta();
  const links = useTradeLinks();
  const networks = useWalletNetworks();
  useWalletPushes();

  const all = useMemo(() => networks.data ?? [], [networks.data]);
  const coins = useMemo(() => assetsFor(all, "deposit"), [all]);
  const everyCoin = useMemo(() => sortAssets(meta.list.map((a) => a.asset_code)), [meta.list]);
  const asset = (params.get("asset") ?? "").toUpperCase();
  const { list, network: selected, internalOnly, paused, step } = walletFlow(networks.data, asset, params.get("network"), "deposit");
  const openCount = list.filter((n) => n.deposit_enabled).length;
  const decimals = meta.decimals(asset);

  const choose = (next: { asset?: string; network?: string | null }) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (next.asset !== undefined) {
          if (next.asset) p.set("asset", next.asset);
          else p.delete("asset");
          p.delete("network");
        }
        if (next.network !== undefined) {
          if (next.network) p.set("network", next.network);
          else p.delete("network");
        }
        return p;
      },
      { replace: true },
    );

  const refresh = () => Promise.all([networks.refetch(), qc.refetchQueries({ queryKey: walletKeys.deposits })]);

  return (
    <PullToRefresh onRefresh={refresh}>
      <div className="flex flex-col gap-3 px-4 pb-6 pt-2">
        <EligibilityNotice feature="DEPOSIT" title={title} />
        <section aria-label={title} className="rounded-3 bg-bg-1 p-4">
          {networks.isError ? (
            <ErrorState compact message={errorText(networks.error)} onRetry={() => void networks.refetch()} className={RETRY} />
          ) : (
            <>
              <StepBlock
                n={1}
                title={t("mAssets.deposit.stepCoin")}
                state={step === 0 ? "active" : "done"}
                onChange={() => choose({ asset: "" })}
                summary={<ChosenCoin asset={asset} name={meta.name(asset)} />}
              >
                {internalOnly && (
                  <div className="mb-3">
                    <InternalOnly asset={asset} name={meta.name(asset)} trade={links.spot(asset)} />
                  </div>
                )}
                <CoinList
                  open={coins}
                  all={everyCoin}
                  value={asset}
                  onPick={(a) => choose({ asset: a })}
                  name={meta.name}
                  trailing={(a) => t("mAssets.common.networksCount", { count: networksFor(all, a, "deposit").filter((n) => n.deposit_enabled).length })}
                  tradeLink={links.spot}
                  loading={networks.isPending}
                />
              </StepBlock>
              <StepBlock
                n={2}
                title={t("mAssets.deposit.stepNetwork")}
                state={step === 0 ? "locked" : step === 1 ? "active" : "done"}
                onChange={openCount > 1 ? () => choose({ network: null }) : undefined}
                summary={selected && <ChosenNetwork network={selected} detail={t("mAssets.common.confirmations", { n: selected.confirmations })} />}
              >
                <p className="mb-2 text-sm text-fg-3">{t("mAssets.deposit.networkHint")}</p>
                {paused && (
                  <Notice tone="warn" className="mb-2">
                    {t("mAssets.deposit.pausedAll", { asset })}
                  </Notice>
                )}
                <NetworkCards
                  list={list}
                  value={selected?.network ?? null}
                  onChange={(n) => choose({ network: n })}
                  purpose="deposit"
                  asset={asset}
                  decimals={decimals}
                  loading={networks.isPending}
                />
              </StepBlock>
              <StepBlock n={3} title={t("mAssets.deposit.stepAddress")} state={step === 2 ? "active" : "locked"} last>
                {selected && <AddressPanel key={selected.network + asset} asset={asset} network={selected} decimals={decimals} />}
              </StepBlock>
            </>
          )}
        </section>
        <h2 className="px-1 pt-2 text-md font-semibold text-fg-1">{t("mAssets.deposit.recent")}</h2>
        <DepositList networks={all} decimals={meta.decimals} />
      </div>
    </PullToRefresh>
  );
}

function AddressPanel({ asset, network, decimals }: { asset: string; network: WalletNetwork; decimals: number }) {
  const { t } = useTranslation();
  const eta = useEtaText();
  const address = useDepositAddress(asset, network.network);
  const a = address.data;
  const explorer = explorerUrl(network.explorer_address_url, a?.address);
  const contract = a?.contract ?? network.contract;
  const min = plainAmount(a?.min_deposit ?? network.min_deposit, decimals);
  const confirmations = a?.confirmations ?? network.confirmations;

  if (address.isError) {
    return (
      <div className="rounded-2 bg-bg-2">
        <ErrorState compact message={errorText(address.error)} onRetry={() => void address.refetch()} className={RETRY} />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-center">
        {a ? <QrCode value={a.address} size={184} label={t("mAssets.deposit.qr", { asset })} /> : <Skeleton className="size-[208px] rounded-2" />}
      </div>
      <div>
        <div className="text-xs text-fg-3">{t("mAssets.deposit.addressFor", { asset, network: network.display_name })}</div>
        {a ? (
          <div className="mt-1.5 flex items-center gap-1 rounded-2 bg-bg-2 pl-3">
            <code data-testid="deposit-address" className="min-w-0 flex-1 break-all py-2.5 font-mono text-sm text-fg-1">
              {a.address}
            </code>
            <CopyIcon value={a.address} label={t("mAssets.deposit.copyAddress")} />
          </div>
        ) : (
          <SkeletonLines lines={2} className="mt-2" />
        )}
      </div>
      {a && <CopyAction value={a.address} label={t("mAssets.deposit.copyAddress")} />}
      {explorer && (
        <a
          href={explorer}
          target="_blank"
          rel="noreferrer noopener"
          className="-my-1 flex min-h-11 items-center justify-center gap-1.5 text-sm text-fg-2 active:text-brand"
        >
          <ExternalLink size={14} />
          {t("mAssets.deposit.viewAddress")}
        </a>
      )}
      <Notice tone="danger" role="alert">
        <span className="font-medium">{t("mAssets.deposit.warning", { asset, network: network.display_name })}</span>
      </Notice>
      <KeyValue
        className="rounded-2 bg-bg-2 p-3"
        items={[
          { key: "min", label: t("mAssets.deposit.minDeposit"), value: `${min} ${asset}` },
          { key: "confirmations", label: t("mAssets.deposit.confirmations"), value: t("mAssets.common.confirmations", { n: confirmations }) },
          { key: "eta", label: t("mAssets.common.eta"), value: eta(network.eta_minutes) },
          ...(contract ? [{ key: "contract", label: t("mAssets.deposit.contract"), value: <AddressValue address={contract} /> }] : []),
        ]}
      />
      <ul className="flex flex-col gap-1.5 text-xs text-fg-3">
        <li className="flex items-start gap-1.5">
          <ShieldAlert size={12} className="mt-0.5 shrink-0" />
          {t("mAssets.deposit.belowMin", { min, asset })}
        </li>
        <li className="flex items-start gap-1.5">
          <Clock size={12} className="mt-0.5 shrink-0" />
          {t("mAssets.deposit.afterConfirmations", { n: confirmations })}
        </li>
      </ul>
      {isTestnet(network) && <Notice tone="info">{t("mAssets.common.testnetHint")}</Notice>}
    </div>
  );
}
