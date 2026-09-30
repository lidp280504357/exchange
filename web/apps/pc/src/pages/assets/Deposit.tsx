import { errorText } from "@exchange/core";
import { useDepositAddress, useWalletNetworks, useWalletPushes } from "@exchange/core/wallet/hooks";
import { assetsFor, explorerUrl, isTestnet, networksFor, walletFlow, type WalletNetwork } from "@exchange/core/wallet/networks";
import { Button, CoinIcon, CopyButton, ErrorState, KeyValue, QrCode, Skeleton, SkeletonLines, Stepper, type StepItem } from "@exchange/ui";
import { BadgeCheck, Clock, ExternalLink, Layers, ShieldAlert, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { AssetsLayout, Card, CopyAction, Tips } from "./parts/AssetsLayout";
import { plainAmount, useAssetMeta, useTradeLinks } from "./parts/meta";
import { EligibilityNotice, Notice } from "./parts/Notice";
import { CoinPicker, InternalOnly, NetworkCards, useEtaText } from "./parts/Picker";
import { DepositList } from "./parts/Records";

/**
 * Deposit (design §6.2): three steps on one page (coin → network →
 * address), built from GET /v1/wallet/networks so new networks appear by
 * themselves; the address with its QR code, the network's rules and a
 * warning; beside it the recent deposits, moved live by the pushes. The
 * choice lives in the URL (?asset=&network=), so links can open a step.
 */
export default function Deposit() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const meta = useAssetMeta();
  const links = useTradeLinks();
  const networks = useWalletNetworks();
  useWalletPushes();

  const all = networks.data ?? [];
  const coins = assetsFor(all, "deposit");
  const asset = (params.get("asset") ?? "").toUpperCase();
  const { list, network: selected, internalOnly, paused, step } = walletFlow(networks.data, asset, params.get("network"), "deposit");
  const network = selected?.network ?? null;

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

  const steps: StepItem[] = [
    { key: "coin", title: t("pcAssets.deposit.stepCoin"), description: asset && !internalOnly ? asset : undefined },
    { key: "network", title: t("pcAssets.deposit.stepNetwork"), description: selected?.display_name },
    { key: "address", title: t("pcAssets.deposit.stepAddress") },
  ];

  return (
    <AssetsLayout title={t("pcAssets.deposit.title")} subtitle={t("pcAssets.deposit.subtitle")}>
      <EligibilityNotice feature="DEPOSIT" title={t("pcAssets.deposit.title")} />
      <div className="grid grid-cols-[minmax(0,1fr)_340px] items-start gap-4">
        <Card bodyClassName="p-6">
          <Stepper
            steps={steps}
            current={step}
            onStepClick={(i) => choose(i === 0 ? { asset: "" } : { network: null })}
            aria-label={t("pcAssets.deposit.title")}
          />
          <div className="mt-6 border-t border-line-1 pt-6">
            {networks.isError ? (
              <ErrorState compact message={errorText(networks.error)} onRetry={() => void networks.refetch()} />
            ) : step === 0 ? (
              <section aria-labelledby="deposit-coin" className="animate-fade-up">
                <h2 id="deposit-coin" className="mb-3 text-base font-medium text-fg-1">
                  {t("pcAssets.deposit.pickCoin")}
                </h2>
                {!networks.isPending && coins.length === 0 && (
                  <p className="mb-3 text-sm text-fg-3">{t("pcAssets.deposit.noneOpen")}</p>
                )}
                <CoinPicker
                  coins={coins}
                  value={asset}
                  onChange={(a) => choose({ asset: a })}
                  meta={meta}
                  loading={networks.isPending}
                  trailing={(a) => t("pcAssets.common.networksCount", { count: networksFor(all, a, "deposit").filter((n) => n.deposit_enabled).length })}
                />
                {internalOnly && (
                  <div className="mt-4">
                    <InternalOnly asset={asset} name={meta.name(asset)} trade={links.spot(asset)} />
                  </div>
                )}
              </section>
            ) : (
              <>
                <Chosen asset={asset} name={meta.name(asset)} network={step === 2 && selected ? selected : undefined} onChangeCoin={() => choose({ asset: "" })} onChangeNetwork={list.filter((n) => n.deposit_enabled).length > 1 ? () => choose({ network: null }) : undefined} />
                {step === 1 ? (
                  <section aria-labelledby="deposit-network" className="mt-5 animate-fade-up">
                    <h2 id="deposit-network" className="text-base font-medium text-fg-1">
                      {t("pcAssets.deposit.pickNetwork")}
                    </h2>
                    <p className="mb-3 mt-1 text-sm text-fg-3">{t("pcAssets.deposit.networkHint")}</p>
                    {paused && (
                      <Notice tone="warn" className="mb-3">
                        {t("pcAssets.deposit.pausedAll", { asset })}
                      </Notice>
                    )}
                    <NetworkCards
                      list={list}
                      value={network}
                      onChange={(n) => choose({ network: n })}
                      purpose="deposit"
                      asset={asset}
                      decimals={meta.decimals(asset)}
                      loading={networks.isPending}
                    />
                  </section>
                ) : (
                  selected && <AddressPanel key={selected.network + asset} asset={asset} network={selected} decimals={meta.decimals(asset)} />
                )}
              </>
            )}
          </div>
        </Card>
        <div className="flex flex-col gap-4">
          <Card title={t("pcAssets.deposit.recent")} bodyClassName="p-4">
            <DepositList networks={all} decimals={meta.decimals} />
          </Card>
          <Tips
            title={t("pcAssets.deposit.tipsTitle")}
            tips={[
              [<Layers key="1" size={14} />, t("pcAssets.deposit.tip1")],
              [<TriangleAlert key="2" size={14} />, t("pcAssets.deposit.tip2")],
              [<ShieldAlert key="3" size={14} />, t("pcAssets.deposit.tip3")],
              [<BadgeCheck key="4" size={14} />, t("pcAssets.deposit.tip4")],
            ]}
          />
        </div>
      </div>
    </AssetsLayout>
  );
}

/** Chosen sums up the steps taken, each with a way back. */
function Chosen({
  asset, name, network, onChangeCoin, onChangeNetwork,
}: { asset: string; name: string; network?: WalletNetwork; onChangeCoin: () => void; onChangeNetwork?: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="flex items-center gap-2 rounded-full border border-line-1 bg-bg-2 py-1 pl-1 pr-1.5 text-sm">
        <CoinIcon symbol={asset} size={22} />
        <span className="font-medium text-fg-1">{asset}</span>
        <span className="text-fg-3">{name}</span>
        <Button size="sm" variant="ghost" className="h-6 px-2" onClick={onChangeCoin}>
          {t("pcAssets.common.change")}
        </Button>
      </span>
      {network && (
        <span className="flex items-center gap-2 rounded-full border border-line-1 bg-bg-2 py-1 pl-3 pr-1.5 text-sm">
          <span className="font-medium text-fg-1">{network.display_name}</span>
          <span className="text-xs text-fg-3">{network.network}</span>
          {onChangeNetwork && (
            <Button size="sm" variant="ghost" className="h-6 px-2" onClick={onChangeNetwork}>
              {t("pcAssets.common.change")}
            </Button>
          )}
        </span>
      )}
    </div>
  );
}

function AddressPanel({ asset, network, decimals }: { asset: string; network: WalletNetwork; decimals: number }) {
  const { t } = useTranslation();
  const eta = useEtaText();
  const address = useDepositAddress(asset, network.network);
  const a = address.data;
  const explorer = explorerUrl(network.explorer_address_url, a?.address);
  const contract = a?.contract ?? network.contract;

  if (address.isError) {
    return (
      <div className="mt-6 rounded-2 border border-line-1 bg-bg-2">
        <ErrorState compact message={errorText(address.error)} onRetry={() => void address.refetch()} />
      </div>
    );
  }
  return (
    <section aria-labelledby="deposit-address-title" className="mt-6 animate-fade-up">
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-6">
        <div className="flex flex-col items-center gap-2">
          {a ? (
            <QrCode value={a.address} size={168} label={t("pcAssets.deposit.qr", { asset })} />
          ) : (
            <Skeleton className="size-[192px] rounded-2" />
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <div>
            <h2 id="deposit-address-title" className="text-sm text-fg-3">
              {t("pcAssets.deposit.addressFor", { asset, network: network.display_name })}
            </h2>
            {a ? (
              <div className="mt-2 flex items-start gap-2 rounded-2 border border-line-1 bg-bg-2 px-3 py-2.5">
                <code data-testid="deposit-address" className="min-w-0 flex-1 break-all font-mono text-md text-fg-1">
                  {a.address}
                </code>
                <CopyButton value={a.address} className="mt-0.5" />
              </div>
            ) : (
              <SkeletonLines lines={2} className="mt-2" />
            )}
            <div className="mt-3 flex flex-wrap items-center gap-2">
              {a && <CopyAction value={a.address} label={t("pcAssets.deposit.copyAddress")} />}
              {explorer && (
                <a href={explorer} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 text-sm text-fg-2 hover:text-brand">
                  <ExternalLink size={14} />
                  {t("pcAssets.deposit.viewAddress")}
                </a>
              )}
            </div>
          </div>
          <Notice tone="danger" role="alert">
            <span className="font-medium">{t("pcAssets.deposit.warning", { asset, network: network.display_name })}</span>
          </Notice>
          <KeyValue
            density="compact"
            items={[
              { key: "min", label: t("pcAssets.deposit.minDeposit"), value: `${plainAmount(a?.min_deposit ?? network.min_deposit, decimals)} ${asset}` },
              {
                key: "confirmations",
                label: t("pcAssets.deposit.confirmations"),
                value: t("pcAssets.common.confirmations", { n: a?.confirmations ?? network.confirmations }),
              },
              { key: "eta", label: t("pcAssets.deposit.eta"), value: eta(network.eta_minutes) },
              ...(contract ? [{ key: "contract", label: t("pcAssets.deposit.contract"), value: contract, copy: true }] : []),
            ]}
          />
          <ul className="flex flex-col gap-1.5 text-xs text-fg-3">
            <Rule icon={<ShieldAlert size={12} />}>
              {t("pcAssets.deposit.belowMin", { min: plainAmount(a?.min_deposit ?? network.min_deposit, decimals), asset })}
            </Rule>
            <Rule icon={<Clock size={12} />}>{t("pcAssets.deposit.afterConfirmations", { n: a?.confirmations ?? network.confirmations })}</Rule>
          </ul>
          {isTestnet(network) && <Notice tone="info">{t("pcAssets.common.testnetHint")}</Notice>}
        </div>
      </div>
    </section>
  );
}

function Rule({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <li className="flex items-start gap-1.5">
      <span className="mt-0.5 shrink-0">{icon}</span>
      {children}
    </li>
  );
}
