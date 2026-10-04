import { Outlet, useLocation } from "react-router";
import { Footer } from "./Footer";
import { LearningBanner } from "./LearningBanner";
import { StatusBanner } from "./StatusBanner";
import { TopNav } from "./TopNav";

/**
 * AppShell: the fixed top bar, the page and the footer. A page fades in
 * and rises 8 px when the path changes (design §5.3), by CSS keyed on the
 * path, so the first screen carries no animation library.
 */
export function AppShell() {
  const { pathname } = useLocation();
  return (
    <div className="flex min-h-dvh min-w-[1024px] flex-col bg-bg-0">
      <TopNav />
      <LearningBanner />
      <StatusBanner />
      <main key={pathname} className="flex-1 animate-fade-up">
        <Outlet />
      </main>
      <Footer />
    </div>
  );
}
