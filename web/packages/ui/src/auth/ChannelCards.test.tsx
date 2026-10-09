import "../test/setup";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ChannelCards } from "./ChannelCards";

describe("ChannelCards", () => {
  it("shows each channel with where its code goes, the chosen one checked, one the account lacks greyed", () => {
    const onChange = vi.fn();
    render(
      <ChannelCards
        options={[
          { channel: "EMAIL", target: "a***@example.com", bound: true },
          { channel: "SMS", bound: false },
        ]}
        value="EMAIL"
        onValueChange={onChange}
      />,
    );
    const [email, sms] = screen.getAllByRole("radio");
    expect(screen.getByRole("radiogroup")).toBeTruthy();
    expect(email?.getAttribute("aria-checked")).toBe("true");
    expect(email?.textContent).toContain("a***@example.com");
    expect(sms?.hasAttribute("disabled")).toBe(true);
    expect(sms?.textContent).toContain("Not linked");
    fireEvent.click(sms!);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("switches to another bound channel", () => {
    const onChange = vi.fn();
    render(
      <ChannelCards
        options={[
          { channel: "EMAIL", target: "a***@example.com", bound: true },
          { channel: "SMS", target: "+86138****1234", bound: true },
        ]}
        value="EMAIL"
        onValueChange={onChange}
      />,
    );
    const [, sms] = screen.getAllByRole("radio");
    expect(sms?.textContent).toContain("+86138****1234");
    fireEvent.click(sms!);
    expect(onChange).toHaveBeenCalledWith("SMS");
  });

  it("says where the code goes, without a choice, for a single channel", () => {
    render(<ChannelCards options={[{ channel: "SMS", target: "+86138****1234", bound: true }]} value="SMS" onValueChange={() => {}} />);
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.getByTestId("otp-channel-only").textContent).toContain("+86138****1234");
  });
});
