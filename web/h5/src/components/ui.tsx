import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from "react";

export function Card({ title, children, actions }: { title?: ReactNode; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="rounded-xl border border-white/10 bg-[#161a1e] p-4 sm:p-5">
      {(title || actions) && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          {title && <h2 className="text-base font-semibold">{title}</h2>}
          {actions}
        </div>
      )}
      {children}
    </section>
  );
}

export function Button({ variant = "primary", className = "", ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "ghost" | "danger" }) {
  const styles = {
    primary: "bg-[#f0b90b] text-black hover:bg-[#f8d12f]",
    ghost: "border border-white/15 text-gray-200 hover:bg-white/5",
    danger: "border border-red-500/40 text-red-300 hover:bg-red-500/10",
  }[variant];
  return (
    <button
      className={`rounded-lg px-4 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${styles} ${className}`}
      {...rest}
    />
  );
}

export function Field({ label, hint, ...rest }: InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs text-gray-400">{label}</span>
      <input
        className="w-full rounded-lg border border-white/10 bg-[#0b0e11] px-3 py-2 text-sm outline-none focus:border-[#f0b90b]"
        {...rest}
      />
      {hint && <span className="block text-xs text-gray-500">{hint}</span>}
    </label>
  );
}

export function ErrorText({ text }: { text?: string }) {
  if (!text) return null;
  return <p className="rounded-md bg-red-500/10 px-3 py-2 text-sm text-red-300">{text}</p>;
}

export function Notice({ text }: { text?: string }) {
  if (!text) return null;
  return <p className="rounded-md bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300">{text}</p>;
}

export function Badge({ children, tone = "gray" }: { children: ReactNode; tone?: "gray" | "green" | "yellow" | "red" }) {
  const tones = {
    gray: "bg-white/10 text-gray-300",
    green: "bg-emerald-500/15 text-emerald-300",
    yellow: "bg-yellow-500/15 text-yellow-300",
    red: "bg-red-500/15 text-red-300",
  }[tone];
  return <span className={`inline-block rounded px-2 py-0.5 text-xs ${tones}`}>{children}</span>;
}

export function Time({ value }: { value: string }) {
  return <time dateTime={value}>{new Date(value).toLocaleString()}</time>;
}
