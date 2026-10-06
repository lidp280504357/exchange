import { dec } from "@exchange/core";
import { Badge, Button, Drawer, FormField, IconButton, Input, Select, Switch } from "@exchange/ui";
import { Plus, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  isAmount, riskTierProblems, withoutVersion, type AssetConfig, type ContractConfig, type FeeSchedule, type InstrumentConfig,
  type NetworkConfig, type PairConfig, type RiskTier,
} from "./config";
import { AssetProfileSection } from "./profile";
import { useReview } from "./Review";

// The editors of the reference data (design 2026-10-02 §4.4, C3): a pair,
// an asset with its networks, a contract with its risk ladder, a fee tier.
// Each builds a patch of whole items and hands it to useReview, which
// previews it and applies it on confirmation.

/** Field is a labelled text input with its error. */
function Field({
  label, value, onChange, error, hint, disabled, placeholder, mono, unit,
}: {
  label: ReactNode;
  value: string;
  onChange?: (v: string) => void;
  error?: string;
  hint?: ReactNode;
  disabled?: boolean;
  placeholder?: string;
  mono?: boolean;
  unit?: string;
}) {
  return (
    <FormField label={label} error={error} hint={hint}>
      <Input value={value} onValueChange={onChange} disabled={disabled} placeholder={placeholder} unit={unit} className={mono ? "font-mono" : undefined} />
    </FormField>
  );
}

/** useAmount is an amount field's error: required, a non-negative decimal, positive unless zero is allowed. */
function useAmount() {
  const { t } = useTranslation();
  return (v: string | undefined, zero = false) =>
    !v || !isAmount(v) || (!zero && dec.isZero(v.trim())) ? t(zero ? "admin.listing.needAmountOrZero" : "admin.listing.needAmount") : undefined;
}

function Footer({ busy, disabled, onSave, onCancel }: { busy: boolean; disabled: boolean; onSave: () => void; onCancel: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="sticky bottom-0 -mx-6 mt-2 flex justify-end gap-2 border-t border-line-1 bg-bg-1 px-6 py-3">
      <Button variant="secondary" onClick={onCancel}>
        {t("common.cancel")}
      </Button>
      <Button loading={busy} disabled={disabled} onClick={onSave}>
        {t("admin.listing.preview")}
      </Button>
    </div>
  );
}

const grid = "grid gap-3 sm:grid-cols-2";

/** blankPair is a new pair's defaults: quoted in USDT, the first fee tier. */
function blankPair(cfg: InstrumentConfig): PairConfig {
  return {
    symbol: "", base_asset: "", quote_asset: "USDT", tick_size: "0.0001", lot_size: "0.01", min_quantity: "0.01", max_quantity: "1000000",
    min_notional: "5", price_band: "0.1", fee_tier: cfg.fee_schedules[0]?.tier ?? "default", status: "PREPARE", reference_symbol: "",
    reference_multiplier: "1",
  };
}

/** PairDrawer creates a pair (pair null) or edits one; a new pair starts in PREPARE. */
export function PairDrawer({ cfg, pair, onClose }: { cfg: InstrumentConfig; pair: PairConfig | null; onClose: () => void }) {
  const { t } = useTranslation();
  const amount = useAmount();
  const creating = pair === null;
  const [v, setV] = useState<PairConfig>(pair ?? blankPair(cfg));
  const { review, busy, dialog } = useReview();
  const set = (k: keyof PairConfig) => (x: string) => setV((p) => ({ ...p, [k]: x }));
  const symbol = creating ? (v.base_asset && v.quote_asset ? `${v.base_asset}-${v.quote_asset}` : "") : v.symbol;
  const taken = creating && cfg.pairs.some((p) => p.symbol === symbol);
  const assets = cfg.assets.map((a) => ({ value: a.asset_code, label: `${a.asset_code} · ${a.name}` }));
  const precision = steps(cfg, v);
  const errors = {
    base: !v.base_asset || v.base_asset === v.quote_asset ? t("admin.listing.needBase") : taken ? t("admin.listing.taken", { symbol }) : undefined,
    tick: amount(v.tick_size) ?? (precision.tick !== undefined ? t("admin.listing.tooFine", { n: precision.tick }) : undefined),
    lot:
      amount(v.lot_size) ??
      (precision.lot !== undefined ? t("admin.listing.tooFine", { n: precision.lot }) : precision.product !== undefined ? t("admin.listing.tickLot", { n: precision.product }) : undefined),
    minQty: amount(v.min_quantity), maxQty: amount(v.max_quantity),
    minNotional: amount(v.min_notional, true), band: amount(v.price_band), multiplier: amount(v.reference_multiplier),
  };
  const ok = Object.values(errors).every((e) => !e);
  const save = () => {
    const item = withoutVersion({ ...v, symbol, reference_symbol: v.reference_symbol?.trim().toUpperCase() ?? "" });
    void review({ pairs: [item] }, creating ? t("admin.listing.createPair", { symbol }) : t("admin.listing.editPair", { symbol }), onClose);
  };
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={creating ? t("admin.listing.newPair") : symbol}
      description={creating ? t("admin.listing.newPairHint") : t("admin.listing.editHint")}
      width={600}
    >
      <div className="flex flex-col gap-4">
        <div className={grid}>
          <FormField label={t("admin.listing.fields.base_asset")} error={errors.base || undefined}>
            <Select value={v.base_asset || undefined} onValueChange={set("base_asset")} options={assets} disabled={!creating} placeholder="BTC" />
          </FormField>
          <FormField label={t("admin.listing.fields.quote_asset")}>
            <Select value={v.quote_asset} onValueChange={set("quote_asset")} options={assets} disabled={!creating} />
          </FormField>
        </div>
        <div className={grid}>
          <Field label={t("admin.listing.fields.tick_size")} value={v.tick_size} onChange={set("tick_size")} error={errors.tick} mono />
          <Field label={t("admin.listing.fields.lot_size")} value={v.lot_size} onChange={set("lot_size")} error={errors.lot} mono />
          <Field label={t("admin.listing.fields.min_quantity")} value={v.min_quantity} onChange={set("min_quantity")} error={errors.minQty} mono />
          <Field label={t("admin.listing.fields.max_quantity")} value={v.max_quantity} onChange={set("max_quantity")} error={errors.maxQty} mono />
          <Field
            label={t("admin.listing.fields.min_notional")} value={v.min_notional} onChange={set("min_notional")} error={errors.minNotional} mono
            unit={v.quote_asset}
          />
          <Field
            label={t("admin.listing.fields.price_band")} value={v.price_band} onChange={set("price_band")} error={errors.band} mono
            hint={t("admin.listing.bandHint")}
          />
        </div>
        <FormField label={t("admin.listing.fields.fee_tier")}>
          <Select value={v.fee_tier} onValueChange={set("fee_tier")} options={cfg.fee_schedules.map((f) => ({ value: f.tier, label: feeLabel(f) }))} />
        </FormField>
        <div className={grid}>
          <Field
            label={t("admin.listing.fields.reference_symbol")} value={v.reference_symbol ?? ""} onChange={(x) => set("reference_symbol")(x.toUpperCase())}
            placeholder="BTCUSDT" hint={t("admin.listing.referenceHint")} mono
          />
          <Field
            label={t("admin.listing.fields.reference_multiplier")} value={v.reference_multiplier} onChange={set("reference_multiplier")}
            error={errors.multiplier} hint={t("admin.listing.multiplierHint")} mono
          />
        </div>
        <Footer busy={busy} disabled={!ok} onSave={save} onCancel={onClose} />
      </div>
      {dialog}
    </Drawer>
  );
}

const feeLabel = (f: FeeSchedule) => `${f.tier} · ${f.maker_fee_rate} / ${f.taker_fee_rate}`;

/**
 * steps checks a pair's steps against its assets' decimals as
 * instrument-service does: the price step fits the quote asset, the
 * quantity step the base asset, and their product the quote asset (so
 * order values are exact). Each field is the decimals allowed when
 * exceeded.
 */
export function steps(cfg: InstrumentConfig, p: Pick<PairConfig, "base_asset" | "quote_asset" | "tick_size" | "lot_size">) {
  const base = cfg.assets.find((a) => a.asset_code === p.base_asset)?.decimals;
  const quote = cfg.assets.find((a) => a.asset_code === p.quote_asset)?.decimals;
  const ok = (v: string) => isAmount(v) && dec.gt(v, "0");
  const tick = quote !== undefined && ok(p.tick_size) && dec.decimalsOf(p.tick_size) > quote ? quote : undefined;
  const lot = base !== undefined && ok(p.lot_size) && dec.decimalsOf(p.lot_size) > base ? base : undefined;
  const product =
    quote !== undefined && ok(p.tick_size) && ok(p.lot_size) && dec.decimalsOf(dec.mul(p.tick_size, p.lot_size)) > quote ? quote : undefined;
  return { tick, lot, product };
}

/** blankAsset is a new asset's defaults: trading on, no deposits or withdrawals (an internal asset). */
const blankAsset = (): AssetConfig => ({
  asset_code: "", name: "", decimals: 8, deposit_enabled: false, withdraw_enabled: false, trading_enabled: true, risk_restricted: false,
  networks: [],
});

/** AssetDrawer creates an asset (asset null) or edits one and its networks; decimals are fixed once set. */
export function AssetDrawer({ cfg, asset, onClose }: { cfg: InstrumentConfig; asset: AssetConfig | null; onClose: () => void }) {
  const { t } = useTranslation();
  const creating = asset === null;
  const [v, setV] = useState<AssetConfig>(asset ?? blankAsset());
  const [network, setNetwork] = useState<NetworkConfig | "new" | null>(null);
  const { review, busy, dialog } = useReview();
  const code = v.asset_code.trim().toUpperCase();
  const taken = creating && cfg.assets.some((a) => a.asset_code === code);
  const errors = {
    code: !/^[A-Z0-9]{2,10}$/.test(code) ? t("admin.listing.needCode") : taken ? t("admin.listing.taken", { symbol: code }) : undefined,
    name: !v.name.trim() ? t("admin.listing.needName") : undefined,
    decimals: !Number.isInteger(v.decimals) || v.decimals < 0 || v.decimals > 18 ? t("admin.listing.needDecimals") : undefined,
  };
  const ok = Object.values(errors).every((e) => !e);
  const { networks: _, ...rest } = v;
  const save = () => {
    const item = withoutVersion({ ...rest, asset_code: code, name: v.name.trim(), categories: v.categories?.filter(Boolean) });
    void review({ assets: [item] }, creating ? t("admin.listing.createAsset", { code }) : t("admin.listing.editAsset", { code }), onClose);
  };
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={creating ? t("admin.listing.newAsset") : `${code} · ${v.name}`}
      description={creating ? t("admin.listing.newAssetHint") : t("admin.listing.editHint")}
      width={640}
    >
      <div className="flex flex-col gap-4">
        <div className={grid}>
          <Field
            label={t("admin.listing.fields.asset_code")} value={v.asset_code} onChange={(x) => setV({ ...v, asset_code: x.toUpperCase() })}
            disabled={!creating} error={errors.code} mono
          />
          <Field label={t("admin.listing.fields.name")} value={v.name} onChange={(x) => setV({ ...v, name: x })} error={errors.name} />
          <Field
            label={t("admin.listing.fields.decimals")} value={String(v.decimals)} onChange={(x) => setV({ ...v, decimals: Number(x) })}
            disabled={!creating} error={errors.decimals} hint={t("admin.listing.decimalsHint")} mono
          />
          <Field label={t("admin.listing.fields.rank")} value={v.rank ? String(v.rank) : ""} onChange={(x) => setV({ ...v, rank: Number(x) || undefined })} mono />
        </div>
        <Field
          label={t("admin.listing.fields.categories")} value={(v.categories ?? []).join(", ")}
          onChange={(x) => setV({ ...v, categories: x.split(",").map((c) => c.trim().toLowerCase()) })} placeholder="layer1, defi"
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Switch label={t("admin.listing.fields.trading_enabled")} checked={v.trading_enabled} onCheckedChange={(c) => setV({ ...v, trading_enabled: c })} />
          <Switch label={t("admin.listing.fields.risk_restricted")} checked={v.risk_restricted} onCheckedChange={(c) => setV({ ...v, risk_restricted: c })} />
          <Switch label={t("admin.listing.fields.deposit_enabled")} checked={v.deposit_enabled} onCheckedChange={(c) => setV({ ...v, deposit_enabled: c })} />
          <Switch label={t("admin.listing.fields.withdraw_enabled")} checked={v.withdraw_enabled} onCheckedChange={(c) => setV({ ...v, withdraw_enabled: c })} />
        </div>
        <Footer busy={busy} disabled={!ok} onSave={save} onCancel={onClose} />
        {!creating && (
          <section className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <h3 className="flex-1 text-sm font-semibold">{t("admin.instruments.networks")}</h3>
              <Button size="sm" variant="secondary" icon={<Plus size={14} />} onClick={() => setNetwork("new")}>
                {t("admin.listing.newNetwork")}
              </Button>
            </div>
            {(v.networks ?? []).length === 0 && <p className="text-sm text-fg-3">{t("admin.listing.noNetworks")}</p>}
            <ul className="flex flex-col divide-y divide-line-1 rounded-2 border border-line-1">
              {(v.networks ?? []).map((n) => (
                <li key={n.network} className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
                  <span className="font-mono font-medium">{n.network}</span>
                  <span className="text-xs text-fg-3">{n.display_name || n.chain}</span>
                  {n.provider && <Badge tone="info">{n.provider}</Badge>}
                  {!n.deposit_enabled && <Badge tone="neutral">{t("admin.listing.noDeposits")}</Badge>}
                  {!n.withdraw_enabled && <Badge tone="neutral">{t("admin.listing.noWithdrawals")}</Badge>}
                  <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setNetwork(n)}>
                    {t("admin.listing.edit")}
                  </Button>
                </li>
              ))}
            </ul>
          </section>
        )}
        {network && asset && <NetworkForm asset={asset} network={network === "new" ? null : network} onDone={() => setNetwork(null)} />}
        {!creating && <AssetProfileSection code={code} writable />}
      </div>
      {dialog}
    </Drawer>
  );
}

/** blankNetwork is a new network's defaults: the platform's own EVM wallet, both ways closed until checked. */
const blankNetwork = (): NetworkConfig => ({
  network: "", chain: "", contract_address: "", confirmations: 12, min_deposit: "0", min_withdraw: "0", withdraw_fee: "0", memo_required: false,
  deposit_enabled: false, withdraw_enabled: false, address_format: "EVM", provider: "",
});

/** NetworkForm creates or edits one network of an asset; the asset itself goes along unchanged. */
function NetworkForm({ asset, network, onDone }: { asset: AssetConfig; network: NetworkConfig | null; onDone: () => void }) {
  const { t } = useTranslation();
  const amount = useAmount();
  const creating = network === null;
  const [v, setV] = useState<NetworkConfig>(network ?? blankNetwork());
  const { review, busy, dialog } = useReview();
  const set = (k: keyof NetworkConfig) => (x: string) => setV((n) => ({ ...n, [k]: x }));
  const code = v.network.trim().toUpperCase();
  const errors = {
    network: !/^[A-Z0-9-]{2,32}$/.test(code) ? t("admin.listing.needNetwork") : creating && (asset.networks ?? []).some((n) => n.network === code) ? t("admin.listing.taken", { symbol: code }) : undefined,
    chain: !v.chain.trim() ? t("admin.listing.needChain") : undefined,
    confirmations: !Number.isInteger(v.confirmations) || v.confirmations < 0 ? t("admin.listing.needCount") : undefined,
    minDeposit: amount(v.min_deposit, true), minWithdraw: amount(v.min_withdraw, true), fee: amount(v.withdraw_fee, true),
  };
  const ok = Object.values(errors).every((e) => !e);
  const save = () => {
    const { networks: _, ...rest } = asset;
    const item = { ...withoutVersion(rest), networks: [withoutVersion({ ...v, network: code, asset_code: asset.asset_code })] };
    void review({ assets: [item] }, t(creating ? "admin.listing.createNetwork" : "admin.listing.editNetwork", { code: `${asset.asset_code}/${code}` }), onDone);
  };
  return (
    <section className="flex flex-col gap-3 rounded-2 border border-brand/50 bg-bg-0 p-3 animate-rise">
      <h4 className="text-sm font-semibold">{creating ? t("admin.listing.newNetwork") : `${asset.asset_code} / ${v.network}`}</h4>
      <div className={grid}>
        <Field label={t("admin.listing.fields.network")} value={v.network} onChange={(x) => set("network")(x.toUpperCase())} disabled={!creating} error={errors.network} mono />
        <Field label={t("admin.listing.fields.chain")} value={v.chain} onChange={set("chain")} error={errors.chain} mono placeholder="11155111" />
        <Field label={t("admin.listing.fields.display_name")} value={v.display_name ?? ""} onChange={set("display_name")} placeholder="TRC20" />
        <FormField label={t("admin.listing.fields.address_format")}>
          <Select
            value={v.address_format ?? "EVM"}
            onValueChange={set("address_format")}
            options={["EVM", "TRON", "BTC"].map((f) => ({ value: f, label: f }))}
          />
        </FormField>
        <Field
          label={t("admin.listing.fields.contract_address")} value={v.contract_address} onChange={set("contract_address")} mono
          hint={t("admin.listing.contractHint")}
        />
        <Field
          label={t("admin.listing.fields.confirmations")} value={String(v.confirmations)} onChange={(x) => setV({ ...v, confirmations: Number(x) })}
          error={errors.confirmations} mono
        />
        <Field label={t("admin.listing.fields.min_deposit")} value={v.min_deposit} onChange={set("min_deposit")} error={errors.minDeposit} mono unit={asset.asset_code} />
        <Field label={t("admin.listing.fields.min_withdraw")} value={v.min_withdraw} onChange={set("min_withdraw")} error={errors.minWithdraw} mono unit={asset.asset_code} />
        <Field label={t("admin.listing.fields.withdraw_fee")} value={v.withdraw_fee} onChange={set("withdraw_fee")} error={errors.fee} mono unit={asset.asset_code} />
        <FormField label={t("admin.listing.fields.provider")}>
          <Select
            value={v.provider || "SELF"}
            onValueChange={(x) => set("provider")(x === "SELF" ? "" : x)}
            options={[
              { value: "SELF", label: t("admin.listing.providerSelf") },
              { value: "UDUN", label: t("admin.listing.providerUdun") },
            ]}
          />
        </FormField>
        {v.provider === "UDUN" && (
          <Field label={t("admin.listing.fields.provider_coin")} value={v.provider_coin ?? ""} onChange={set("provider_coin")} mono placeholder="195:TR7NH…" />
        )}
        <Field label={t("admin.listing.fields.explorer_tx_url")} value={v.explorer_tx_url ?? ""} onChange={set("explorer_tx_url")} mono placeholder="https://…/tx/{tx}" />
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <Switch label={t("admin.listing.fields.deposit_enabled")} checked={v.deposit_enabled} onCheckedChange={(c) => setV({ ...v, deposit_enabled: c })} />
        <Switch label={t("admin.listing.fields.withdraw_enabled")} checked={v.withdraw_enabled} onCheckedChange={(c) => setV({ ...v, withdraw_enabled: c })} />
        <Switch label={t("admin.listing.fields.memo_required")} checked={v.memo_required} onCheckedChange={(c) => setV({ ...v, memo_required: c })} />
      </div>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="secondary" onClick={onDone}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" loading={busy} disabled={!ok} onClick={save}>
          {t("admin.listing.preview")}
        </Button>
      </div>
      {dialog}
    </section>
  );
}

/** blankContract is a new perpetual's defaults: the BTC ladder's first tiers, 8-hour funding. */
function blankContract(cfg: InstrumentConfig): ContractConfig {
  return {
    symbol: "", type: "PERPETUAL", base_asset: "", quote_asset: "USDT", index_symbol: "", tick_size: "0.01", lot_size: "0.01", min_quantity: "0.01",
    max_quantity: "100000", min_notional: "5", price_band: "0.05",
    risk_tiers: [
      { max_notional: "50000", max_leverage: 50, mmr: "0.01" },
      { max_notional: "250000", max_leverage: 20, mmr: "0.025" },
      { max_notional: "1000000", max_leverage: 10, mmr: "0.05" },
    ],
    funding_interval_hours: 8, interest_rate: "0.0001", funding_cap: "0.0075", impact_notional: "10000",
    fee_tier: cfg.fee_schedules.find((f) => f.tier === "perp")?.tier ?? cfg.fee_schedules[0]?.tier ?? "perp", status: "PREPARE",
  };
}

/** ContractDrawer creates a perpetual (contract null) or edits one with its risk ladder. */
export function ContractDrawer({ cfg, contract, onClose }: { cfg: InstrumentConfig; contract: ContractConfig | null; onClose: () => void }) {
  const { t } = useTranslation();
  const amount = useAmount();
  const creating = contract === null;
  const [v, setV] = useState<ContractConfig>(contract ?? blankContract(cfg));
  const { review, busy, dialog } = useReview();
  const set = (k: keyof ContractConfig) => (x: string) => setV((c) => ({ ...c, [k]: x }));
  const index = cfg.pairs.find((p) => p.symbol === v.index_symbol);
  const base = creating ? (index?.base_asset ?? "") : v.base_asset;
  const quote = creating ? (index?.quote_asset ?? "") : v.quote_asset;
  const symbol = creating ? (base && quote ? `${base}-${quote}-PERP` : "") : v.symbol;
  const taken = creating && cfg.contracts.some((c) => c.symbol === symbol);
  const tiers = riskTierProblems(v.risk_tiers);
  const errors = {
    index: !index ? t("admin.listing.needIndex") : taken ? t("admin.listing.taken", { symbol }) : undefined,
    tick: amount(v.tick_size), lot: amount(v.lot_size), minQty: amount(v.min_quantity), maxQty: amount(v.max_quantity),
    minNotional: amount(v.min_notional, true), band: amount(v.price_band), rate: amount(v.interest_rate, true), cap: amount(v.funding_cap),
    impact: amount(v.impact_notional),
    hours: ![1, 2, 4, 8].includes(v.funding_interval_hours) ? t("admin.listing.needHours") : undefined,
    tiers: tiers.length > 0 ? t("admin.listing.tiersInvalid") : undefined,
  };
  const ok = Object.values(errors).every((e) => !e);
  const save = () => {
    const item = withoutVersion({ ...v, symbol, base_asset: base, quote_asset: quote });
    void review({ contracts: [item] }, creating ? t("admin.listing.createContract", { symbol }) : t("admin.listing.editContract", { symbol }), onClose);
  };
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={creating ? t("admin.listing.newContract") : symbol}
      description={creating ? t("admin.listing.newContractHint") : t("admin.listing.editHint")}
      width={680}
    >
      <div className="flex flex-col gap-4">
        <FormField label={t("admin.listing.fields.index_symbol")} error={errors.index || undefined} hint={symbol ? t("admin.listing.contractSymbol", { symbol }) : undefined}>
          <Select
            value={v.index_symbol || undefined}
            onValueChange={set("index_symbol")}
            options={cfg.pairs.map((p) => ({ value: p.symbol, label: p.symbol }))}
            disabled={!creating}
            placeholder="BTC-USDT"
          />
        </FormField>
        <div className={grid}>
          <Field label={t("admin.listing.fields.tick_size")} value={v.tick_size} onChange={set("tick_size")} error={errors.tick} mono />
          <Field label={t("admin.listing.fields.lot_size")} value={v.lot_size} onChange={set("lot_size")} error={errors.lot} mono />
          <Field label={t("admin.listing.fields.min_quantity")} value={v.min_quantity} onChange={set("min_quantity")} error={errors.minQty} mono />
          <Field label={t("admin.listing.fields.max_quantity")} value={v.max_quantity} onChange={set("max_quantity")} error={errors.maxQty} mono />
          <Field label={t("admin.listing.fields.min_notional")} value={v.min_notional} onChange={set("min_notional")} error={errors.minNotional} mono unit={quote} />
          <Field label={t("admin.listing.fields.price_band")} value={v.price_band} onChange={set("price_band")} error={errors.band} mono />
          <FormField label={t("admin.listing.fields.funding_interval_hours")} error={errors.hours || undefined}>
            <Select
              value={String(v.funding_interval_hours)}
              onValueChange={(x) => setV({ ...v, funding_interval_hours: Number(x) })}
              options={[1, 2, 4, 8].map((h) => ({ value: String(h), label: t("admin.instruments.hours", { n: h }) }))}
            />
          </FormField>
          <Field label={t("admin.listing.fields.interest_rate")} value={v.interest_rate} onChange={set("interest_rate")} error={errors.rate} mono />
          <Field label={t("admin.listing.fields.funding_cap")} value={v.funding_cap} onChange={set("funding_cap")} error={errors.cap} mono />
          <Field label={t("admin.listing.fields.impact_notional")} value={v.impact_notional} onChange={set("impact_notional")} error={errors.impact} mono unit={quote} />
        </div>
        <FormField label={t("admin.listing.fields.fee_tier")}>
          <Select value={v.fee_tier} onValueChange={set("fee_tier")} options={cfg.fee_schedules.map((f) => ({ value: f.tier, label: feeLabel(f) }))} />
        </FormField>
        <RiskTiers tiers={v.risk_tiers} problems={tiers} onChange={(risk_tiers) => setV({ ...v, risk_tiers })} notionalIn={v.settle_asset || quote} />
        <Footer busy={busy} disabled={!ok} onSave={save} onCancel={onClose} />
      </div>
      {dialog}
    </Drawer>
  );
}

/** RiskTiers edits a contract's risk ladder row by row, marking the cells instrument-service would refuse. */
function RiskTiers({
  tiers, problems, onChange, notionalIn,
}: {
  tiers: RiskTier[];
  problems: string[];
  onChange: (t: RiskTier[]) => void;
  /** The asset the notionals are in: the contract's settlement asset (coins of a coin-margined one, G0). */
  notionalIn?: string;
}) {
  const { t } = useTranslation();
  const bad = (kind: string, i: number) => problems.includes(`${kind}:${i}`);
  const put = (i: number, patch: Partial<RiskTier>) => onChange(tiers.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  const cell = (invalid: boolean) => (invalid ? "border-danger" : undefined);
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h3 className="flex-1 text-sm font-semibold">{t("admin.listing.riskTiers")}</h3>
        <Button
          size="sm" variant="secondary" icon={<Plus size={14} />} disabled={tiers.length >= 20}
          onClick={() => {
            const last = tiers[tiers.length - 1];
            onChange([...tiers, last ? { max_notional: dec.mul(last.max_notional || "0", "2"), max_leverage: Math.max(1, Math.floor(last.max_leverage / 2)), mmr: dec.mul(last.mmr || "0", "2") } : { max_notional: "50000", max_leverage: 20, mmr: "0.01" }]);
          }}
        >
          {t("admin.listing.addTier")}
        </Button>
      </div>
      <p className="text-xs text-fg-3">
        {t("admin.listing.tiersHint")}
        {notionalIn ? ` ${t("admin.coinm.tiersIn", { asset: notionalIn })}` : ""}
      </p>
      <table className="w-full text-left text-sm">
        <thead className="text-xs text-fg-3">
          <tr>
            <th className="py-1 font-normal">#</th>
            <th className="py-1 font-normal">{t("admin.listing.fields.max_notional")}</th>
            <th className="py-1 font-normal">{t("admin.listing.fields.max_leverage")}</th>
            <th className="py-1 font-normal">{t("admin.listing.fields.mmr")}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {tiers.map((tier, i) => (
            <tr key={i} className="border-t border-line-1">
              <td className="py-1.5 pr-2 text-xs text-fg-3">{i + 1}</td>
              <td className="py-1.5 pr-2">
                <Input size="sm" value={tier.max_notional} onValueChange={(x) => put(i, { max_notional: x })} boxClassName={cell(bad("notional", i))} className="font-mono" aria-label={`${t("admin.listing.fields.max_notional")} ${i + 1}`} />
              </td>
              <td className="py-1.5 pr-2">
                <Input size="sm" value={String(tier.max_leverage)} onValueChange={(x) => put(i, { max_leverage: Number(x) })} boxClassName={cell(bad("leverage", i))} className="font-mono" unit="x" aria-label={`${t("admin.listing.fields.max_leverage")} ${i + 1}`} />
              </td>
              <td className="py-1.5 pr-2">
                <Input size="sm" value={tier.mmr} onValueChange={(x) => put(i, { mmr: x })} boxClassName={cell(bad("mmr", i))} className="font-mono" aria-label={`${t("admin.listing.fields.mmr")} ${i + 1}`} />
              </td>
              <td className="py-1.5 text-right">
                <IconButton
                  size="sm" icon={<Trash2 size={14} />} label={t("admin.listing.removeTier")} disabled={tiers.length <= 1}
                  onClick={() => onChange(tiers.filter((_, j) => j !== i))}
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

/** FeeDrawer creates a fee tier (fee null) or changes one's rates; every pair and contract on the tier follows. */
export function FeeDrawer({ cfg, fee, onClose }: { cfg: InstrumentConfig; fee: FeeSchedule | null; onClose: () => void }) {
  const { t } = useTranslation();
  const creating = fee === null;
  const [v, setV] = useState<FeeSchedule>(fee ?? { tier: "", maker_fee_rate: "0.001", taker_fee_rate: "0.001" });
  const { review, busy, dialog } = useReview();
  const tier = v.tier.trim().toLowerCase();
  const rate = (x: string) => (!isAmount(x) || dec.gte(x, "0.1") ? t("admin.listing.needRate") : undefined);
  const users = cfg.pairs.filter((p) => p.fee_tier === tier).length + cfg.contracts.filter((c) => c.fee_tier === tier).length;
  const errors = {
    tier: !/^[a-z0-9_-]{1,32}$/.test(tier) ? t("admin.listing.needTier") : creating && cfg.fee_schedules.some((f) => f.tier === tier) ? t("admin.listing.taken", { symbol: tier }) : undefined,
    maker: rate(v.maker_fee_rate), taker: rate(v.taker_fee_rate),
  };
  const ok = Object.values(errors).every((e) => !e);
  const save = () =>
    void review({ fee_schedules: [withoutVersion({ ...v, tier })] }, creating ? t("admin.listing.createFee", { tier }) : t("admin.listing.editFee", { tier }), onClose);
  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} title={creating ? t("admin.listing.newFee") : tier} description={t("admin.listing.feeHint", { n: users })} width={480}>
      <div className="flex flex-col gap-4">
        <Field label={t("admin.listing.fields.tier")} value={v.tier} onChange={(x) => setV({ ...v, tier: x })} disabled={!creating} error={errors.tier} mono />
        <Field label={t("admin.listing.fields.maker_fee_rate")} value={v.maker_fee_rate} onChange={(x) => setV({ ...v, maker_fee_rate: x })} error={errors.maker} mono hint={t("admin.listing.rateHint")} />
        <Field label={t("admin.listing.fields.taker_fee_rate")} value={v.taker_fee_rate} onChange={(x) => setV({ ...v, taker_fee_rate: x })} error={errors.taker} mono />
        <Footer busy={busy} disabled={!ok} onSave={save} onCancel={onClose} />
      </div>
      {dialog}
    </Drawer>
  );
}
