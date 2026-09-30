import { Check, X } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

export type StepStatus = "done" | "current" | "upcoming" | "error";

export type StepItem = {
  /** A stable key (defaults to the title when it is a string). */
  key?: string;
  title: ReactNode;
  description?: ReactNode;
  /** Overrides the status from `current` (a failed step, a skipped one). */
  status?: StepStatus;
};

export type StepperProps = {
  steps: StepItem[];
  /** Index of the current step: earlier ones are done, later upcoming. */
  current: number;
  orientation?: "horizontal" | "vertical";
  /** Lets people go back to a done step (deposit: change the coin). */
  onStepClick?: (index: number) => void;
  size?: "sm" | "md";
  className?: string;
  "aria-label"?: string;
};

/** stepStatus is a step's status from the current index, unless overridden. */
export function stepStatus(step: StepItem, index: number, current: number): StepStatus {
  if (step.status) return step.status;
  return index < current ? "done" : index === current ? "current" : "upcoming";
}

const dotClass: Record<StepStatus, string> = {
  done: "border-brand bg-brand text-brand-fg",
  current: "border-brand bg-brand-soft text-brand",
  upcoming: "border-line-2 bg-bg-2 text-fg-3",
  error: "border-danger bg-danger text-white",
};

/**
 * Stepper shows where a flow stands: the three deposit steps (horizontal)
 * or a withdrawal's status timeline (vertical). Done steps get a check,
 * a failed one a cross; lines between done steps are filled.
 */
export function Stepper({ steps, current, orientation = "horizontal", onStepClick, size = "md", className, "aria-label": ariaLabel }: StepperProps) {
  const { t } = useTranslation();
  const vertical = orientation === "vertical";
  const dot = size === "sm" ? "size-5 text-xs" : "size-7 text-sm";
  const statusText: Record<StepStatus, string> = {
    done: t("ui.stepDone"), current: t("ui.stepCurrent"), upcoming: t("ui.stepUpcoming"), error: t("ui.stepError"),
  };
  return (
    <ol aria-label={ariaLabel ?? t("ui.steps")} className={cn("flex", vertical ? "flex-col" : "w-full items-start", className)}>
      {steps.map((step, i) => {
        const status = stepStatus(step, i, current);
        const last = i === steps.length - 1;
        const lineDone = status === "done";
        const clickable = Boolean(onStepClick) && status === "done";
        const Marker = clickable ? "button" : "span";
        return (
          <li
            key={step.key ?? (typeof step.title === "string" ? step.title : `step-${i}`)}
            aria-current={status === "current" ? "step" : undefined}
            className={cn("relative flex min-w-0", vertical ? "gap-3 pb-5 last:pb-0" : "flex-1 flex-col items-center text-center")}
          >
            {!last && (
              <span
                aria-hidden
                className={cn(
                  "absolute transition-colors duration-[var(--t-slow)]",
                  lineDone ? "bg-brand" : "bg-line-2",
                  vertical
                    ? cn("bottom-0 w-px", size === "sm" ? "left-2.5 top-6" : "left-3.5 top-8")
                    : cn("h-px", size === "sm" ? "left-[calc(50%+14px)] right-[calc(-50%+14px)] top-2.5" : "left-[calc(50%+18px)] right-[calc(-50%+18px)] top-3.5"),
                )}
              />
            )}
            <Marker
              {...(clickable ? { type: "button" as const, onClick: () => onStepClick?.(i) } : {})}
              className={cn(
                "relative z-10 grid shrink-0 place-items-center rounded-full border font-semibold tabular-nums transition-colors duration-[var(--t-base)]",
                dot,
                dotClass[status],
                clickable && "cursor-pointer hover:brightness-110",
              )}
            >
              {status === "done" ? <Check size={size === "sm" ? 12 : 14} strokeWidth={3} /> : status === "error" ? <X size={size === "sm" ? 12 : 14} strokeWidth={3} /> : i + 1}
              <span className="sr-only">{statusText[status]}</span>
            </Marker>
            <div className={cn("min-w-0", vertical ? "pt-0.5" : "mt-2 px-1")}>
              <div
                className={cn(
                  "text-sm font-medium",
                  status === "upcoming" ? "text-fg-3" : status === "error" ? "text-danger" : "text-fg-1",
                )}
              >
                {step.title}
              </div>
              {step.description && <div className="mt-0.5 text-xs text-fg-3">{step.description}</div>}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
