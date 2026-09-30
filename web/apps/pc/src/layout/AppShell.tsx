import { page } from "@exchange/ui";
import { AnimatePresence, motion } from "motion/react";
import { Outlet, useLocation } from "react-router";
import { Footer } from "./Footer";
import { StatusBanner } from "./StatusBanner";
import { TopNav } from "./TopNav";

/** AppShell: the fixed top bar, the page (faded in on navigation) and the footer. */
export function AppShell() {
  const { pathname } = useLocation();
  return (
    <div className="flex min-h-dvh min-w-[1024px] flex-col bg-bg-0">
      <TopNav />
      <StatusBanner />
      <AnimatePresence mode="wait" initial={false}>
        <motion.main key={pathname} variants={page} initial="initial" animate="animate" exit="exit" className="flex-1">
          <Outlet />
        </motion.main>
      </AnimatePresence>
      <Footer />
    </div>
  );
}
