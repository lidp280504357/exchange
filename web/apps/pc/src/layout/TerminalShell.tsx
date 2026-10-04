import { Outlet } from "react-router";
import { TestModeBanner } from "./TestModeBanner";
import { StatusBanner } from "./StatusBanner";
import { TopNav } from "./TopNav";

/**
 * TerminalShell holds the trading terminals: the top bar and a page that
 * fills the rest of the viewport (no footer, no page scroll above 760 px
 * of height; panels scroll inside).
 */
export function TerminalShell() {
  return (
    <div className="flex h-dvh min-h-[760px] min-w-[1024px] flex-col bg-bg-0">
      <TopNav />
      <TestModeBanner />
      <StatusBanner />
      <main className="flex min-h-0 flex-1 flex-col">
        <Outlet />
      </main>
    </div>
  );
}
