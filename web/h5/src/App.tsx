import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, Route, Routes } from "react-router";
import { refresh } from "./api/client";
import { Layout } from "./components/Layout";
import { NotificationsPage, SecurityPage, SettingsPage } from "./pages/account";
import { AssetsPage } from "./pages/assets";
import { ChallengePage, LoginPage, RegisterPage, ResetPage } from "./pages/auth";
import { DepositPage } from "./pages/deposit";
import { FuturesPage } from "./pages/futures";
import { MarketsPage } from "./pages/markets";
import { TradePage } from "./pages/trade";
import { TransferPage } from "./pages/transfer";
import { WithdrawPage } from "./pages/withdraw";
import { mayHaveSession, useSession } from "./store/session";

function RequireSession({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const { session, restoring } = useSession();
  if (restoring) return <p className="p-8 text-center text-sm text-gray-500">{t("common.loading")}</p>;
  if (!session) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

// useSessionKeeper restores the session from the refresh cookie at start
// (when one may exist) and refreshes the access token a minute before it
// expires.
function useSessionKeeper() {
  const { session, doneRestoring } = useSession();
  useEffect(() => {
    if (!mayHaveSession()) {
      doneRestoring();
      return;
    }
    void refresh().finally(doneRestoring);
  }, [doneRestoring]);
  useEffect(() => {
    if (!session) return;
    const wait = Math.max(5000, session.expiresAt - Date.now() - 60000);
    const t = setTimeout(() => void refresh(), wait);
    return () => clearTimeout(t);
  }, [session]);
}

export function App() {
  useSessionKeeper();
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/login/challenge" element={<ChallengePage />} />
      <Route path="/register" element={<RegisterPage />} />
      <Route path="/reset" element={<ResetPage />} />
      <Route element={<RequireSession><Layout /></RequireSession>}>
        <Route index element={<AssetsPage />} />
        <Route path="/markets" element={<MarketsPage />} />
        <Route path="/trade/:symbol" element={<TradePage />} />
        <Route path="/futures/:symbol" element={<FuturesPage />} />
        <Route path="/transfer" element={<TransferPage />} />
        <Route path="/deposit" element={<DepositPage />} />
        <Route path="/withdraw" element={<WithdrawPage />} />
        <Route path="/notifications" element={<NotificationsPage />} />
        <Route path="/security" element={<SecurityPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
