import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { ApiError } from "./api/client";
import { App } from "./App";
import "./index.css";

// A 401 anywhere means the session ended (expired, idle, revoked): show
// the login page.
function signedOut(err: unknown) {
  if (err instanceof ApiError && err.status === 401) queryClient.setQueryData(["me"], null);
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: signedOut }),
  mutationCache: new MutationCache({ onError: signedOut }),
  defaultOptions: { queries: { retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 1, staleTime: 5_000, refetchOnWindowFocus: false } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter basename="/admin">
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
