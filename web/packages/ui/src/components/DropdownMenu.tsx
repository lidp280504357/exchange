import { Check } from "lucide-react";
import { DropdownMenu as RMenu } from "radix-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type MenuEntry =
  | {
      type?: "item";
      /** A stable key; defaults to the label when it is a string. */
      key?: string;
      label: ReactNode;
      icon?: ReactNode;
      shortcut?: string;
      onSelect?: () => void;
      danger?: boolean;
      disabled?: boolean;
    }
  | { type: "checkbox"; key: string; label: ReactNode; checked: boolean; onCheckedChange: (checked: boolean) => void; disabled?: boolean }
  | { type: "label"; key: string; label: ReactNode }
  | { type: "separator"; key: string };

export type DropdownMenuProps = {
  /** The element that opens the menu (rendered asChild). */
  trigger: ReactNode;
  items: MenuEntry[];
  align?: "start" | "center" | "end";
  side?: "top" | "right" | "bottom" | "left";
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
};

const itemClass =
  "relative flex cursor-pointer select-none items-center gap-2 rounded-1 px-2 py-1.5 text-sm text-fg-1 outline-none data-[highlighted]:bg-bg-3 data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50";

/**
 * DropdownMenu lists actions under a trigger (the user menu, row actions):
 * items with icons and shortcuts, checkbox items, labels and separators;
 * arrow keys and type-ahead come from Radix.
 */
export function DropdownMenu({ trigger, items, align = "end", side = "bottom", open, onOpenChange, className }: DropdownMenuProps) {
  return (
    <RMenu.Root open={open} onOpenChange={onOpenChange}>
      <RMenu.Trigger asChild>{trigger}</RMenu.Trigger>
      <RMenu.Portal>
        <RMenu.Content
          align={align}
          side={side}
          sideOffset={6}
          collisionPadding={8}
          className={cn(
            "z-[var(--z-dropdown)] min-w-44 rounded-2 border border-line-1 bg-bg-2 p-1 shadow-pop",
            "origin-(--radix-dropdown-menu-content-transform-origin) data-[state=open]:animate-pop-in data-[state=closed]:animate-pop-out",
            className,
          )}
        >
          {items.map((it, i) => {
            if (it.type === "separator") return <RMenu.Separator key={it.key} className="my-1 h-px bg-line-1" />;
            if (it.type === "label") {
              return (
                <RMenu.Label key={it.key} className="px-2 py-1 text-xs text-fg-3">
                  {it.label}
                </RMenu.Label>
              );
            }
            if (it.type === "checkbox") {
              return (
                <RMenu.CheckboxItem
                  key={it.key}
                  checked={it.checked}
                  disabled={it.disabled}
                  onCheckedChange={(c) => it.onCheckedChange(c === true)}
                  onSelect={(e) => e.preventDefault()}
                  className={cn(itemClass, "pl-7")}
                >
                  <RMenu.ItemIndicator className="absolute left-2 inline-flex text-brand">
                    <Check size={14} />
                  </RMenu.ItemIndicator>
                  {it.label}
                </RMenu.CheckboxItem>
              );
            }
            return (
              <RMenu.Item
                key={it.key ?? (typeof it.label === "string" ? it.label : `item-${i}`)}
                disabled={it.disabled}
                onSelect={it.onSelect}
                className={cn(itemClass, it.danger && "text-danger data-[highlighted]:bg-danger/10")}
              >
                {it.icon && <span className="inline-flex shrink-0 text-fg-3 [&_svg]:size-4">{it.icon}</span>}
                <span className="flex-1">{it.label}</span>
                {it.shortcut && <kbd className="font-sans text-xs text-fg-3">{it.shortcut}</kbd>}
              </RMenu.Item>
            );
          })}
        </RMenu.Content>
      </RMenu.Portal>
    </RMenu.Root>
  );
}
