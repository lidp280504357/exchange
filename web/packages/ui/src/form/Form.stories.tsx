import { ApiError, dec } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { Controller } from "react-hook-form";
import { z } from "zod";
import { Checkbox } from "../components/Checkbox";
import { Input } from "../components/Input";
import { NumberInput } from "../components/NumberInput";
import { Select } from "../components/Select";
import { Toaster, toast } from "../components/Toast";
import { Form, FormError, FormField, FormSubmit, setServerError, useZodForm } from "./Form";

// A withdrawal form: amounts are decimal strings checked with dec, never Number().
const schema = z.object({
  network: z.string().min(1, "请选择网络"),
  address: z.string().trim().min(26, "地址太短").max(64, "地址太长"),
  amount: z
    .string()
    .refine((v) => dec.checkAmount(v, 6) === "ok", "请输入正确的数量（最多 6 位小数）")
    .refine((v) => dec.checkAmount(v, 6) !== "ok" || dec.gte(v, "10"), "最小提现 10 USDT"),
  memo: z.string().max(32, "最多 32 个字").optional(),
  agree: z.literal(true, { error: "请确认地址与网络一致" }),
});

type Values = z.input<typeof schema>;

const meta = { title: "Form/Form" } satisfies Meta;
export default meta;

type Story = StoryObj;

/**
 * Errors show in place; the server's codes land on their field
 * (WALLET_INVALID_ADDRESS → address, LEDGER_INSUFFICIENT_BALANCE → amount)
 * or above the form (COMMON_UNAVAILABLE). Try an address ending in 0, 1 or 2.
 */
export const Withdraw: Story = {
  render: () => {
    const form = useZodForm(schema, { defaultValues: { network: "", address: "", amount: "", memo: "", agree: false as unknown as true } satisfies Values });
    const submit = async (v: z.output<typeof schema>) => {
      await new Promise((r) => setTimeout(r, 700));
      const code = v.address.endsWith("0")
        ? "WALLET_INVALID_ADDRESS"
        : v.address.endsWith("1")
          ? "LEDGER_INSUFFICIENT_BALANCE"
          : v.address.endsWith("2")
            ? "COMMON_UNAVAILABLE"
            : null;
      if (code) {
        setServerError(form, new ApiError(400, code, code, {}, "5d65903b0824a7e4"), {
          WALLET_INVALID_ADDRESS: "address",
          WALLET_ADDRESS_COOLDOWN: "address",
          LEDGER_INSUFFICIENT_BALANCE: "amount",
          "WALLET_BELOW_*": "amount",
        });
        return;
      }
      toast.success("提现已提交", { description: `${v.amount} USDT → ${v.address.slice(0, 8)}…` });
    };
    return (
      <div className="w-96">
        <Form form={form} onSubmit={submit}>
          <FormError />
          <FormField label="网络" name="network" required>
            <Controller
              control={form.control}
              name="network"
              render={({ field, fieldState }) => (
                <Select
                  value={field.value || undefined}
                  onValueChange={field.onChange}
                  invalid={Boolean(fieldState.error)}
                  className="w-full"
                  placeholder="选择网络"
                  options={[
                    { value: "TRC20", label: "TRON (TRC20)" },
                    { value: "BEP20", label: "BNB Smart Chain (BEP20)" },
                    { value: "ERC20", label: "Ethereum (ERC20)" },
                  ]}
                  aria-label="Network"
                />
              )}
            />
          </FormField>
          <FormField label="地址" name="address" required hint="请仔细核对，发错网络无法找回">
            <Input {...form.register("address")} placeholder="TQn9Y2khEsLJW1ChVWFMSMeRDow5KcbLSE" clearable />
          </FormField>
          <FormField label="数量" name="amount" required extra="可用 1,250.50 USDT">
            <Controller
              control={form.control}
              name="amount"
              render={({ field }) => (
                <NumberInput value={field.value} onValueChange={field.onChange} onBlur={field.onBlur} decimals={6} max="1250.5" unit="USDT" align="right" />
              )}
            />
          </FormField>
          <FormField label="备注" name="memo" optional>
            <Input {...form.register("memo")} />
          </FormField>
          <Controller
            control={form.control}
            name="agree"
            render={({ field, fieldState }) => (
              <div className="flex flex-col gap-1">
                <Checkbox checked={field.value === true} onCheckedChange={(c) => field.onChange(c)} invalid={Boolean(fieldState.error)} label="我确认地址与网络一致" />
                {fieldState.error && <p className="text-xs text-danger">{fieldState.error.message}</p>}
              </div>
            )}
          />
          <FormSubmit block>提交提现</FormSubmit>
        </Form>
        <Toaster />
      </div>
    );
  },
};
