import "../test/setup";
import { ApiError } from "@exchange/core";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { z } from "zod";
import { Input } from "../components/Input";
import { Form, FormError, FormField, FormSubmit, mapServerError, setServerError, useZodForm } from "./Form";

describe("mapServerError", () => {
  const map = { WALLET_INVALID_ADDRESS: "address", LEDGER_INSUFFICIENT_BALANCE: "amount", "AUTH_OTP_*": "code" };

  it("maps a known code to its field with the localized message", () => {
    const m = mapServerError(new ApiError(400, "WALLET_INVALID_ADDRESS", "bad", {}, "trace-1"), map);
    expect(m).toEqual({ field: "address", message: "Not a valid address", code: "WALLET_INVALID_ADDRESS", traceId: "trace-1" });
  });

  it("matches code prefixes", () => {
    expect(mapServerError(new ApiError(400, "AUTH_OTP_EXPIRED", "x"), map).field).toBe("code");
  });

  it("falls back to a form-level error", () => {
    const m = mapServerError(new ApiError(503, "COMMON_UNAVAILABLE", "x"), map);
    expect(m.field).toBeUndefined();
    expect(m.message).toBe("Temporarily unavailable, try again");
    expect(mapServerError(new Error("boom"), map)).toEqual({ message: "Something went wrong (boom)" });
  });
});

const schema = z.object({ address: z.string().min(3, "Too short") });

function Withdraw({ fail, onValid }: { fail?: ApiError; onValid?: (v: { address: string }) => void }) {
  const form = useZodForm(schema, { defaultValues: { address: "" } });
  return (
    <Form
      form={form}
      onSubmit={async (v) => {
        if (fail) setServerError(form, fail, { WALLET_INVALID_ADDRESS: "address" });
        else onValid?.(v);
      }}
    >
      <FormError />
      <FormField label="Address" name="address">
        <Input {...form.register("address")} />
      </FormField>
      <FormSubmit>Send</FormSubmit>
    </Form>
  );
}

describe("Form", () => {
  it("shows schema errors in place and submits valid values", async () => {
    const onValid = vi.fn();
    render(<Withdraw onValid={onValid} />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    expect(screen.getByText("Too short")).toBeTruthy();
    expect(screen.getByLabelText("Address").getAttribute("aria-invalid")).toBe("true");
    fireEvent.change(screen.getByLabelText("Address"), { target: { value: "TQn9Y2khEsLJW1" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    expect(onValid).toHaveBeenCalledWith({ address: "TQn9Y2khEsLJW1" });
  });

  it("puts a server error on its field or above the form", async () => {
    const { unmount } = render(<Withdraw fail={new ApiError(400, "WALLET_INVALID_ADDRESS", "x")} />);
    fireEvent.change(screen.getByLabelText("Address"), { target: { value: "abcdef" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    expect(screen.getByText("Not a valid address")).toBeTruthy();
    unmount();

    render(<Withdraw fail={new ApiError(503, "COMMON_UNAVAILABLE", "x")} />);
    fireEvent.change(screen.getByLabelText("Address"), { target: { value: "abcdef" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    expect(screen.getByRole("alert").textContent).toContain("Temporarily unavailable, try again");
  });
});
