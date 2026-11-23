import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./styles.css";
import "./tokens.css";

import { App } from "./App";
import { migrateLegacyHash } from "./models/route";
import { applyColorTheme, watchColorTheme } from "./models/theme";

// No transport is configured here. The screen's liveness is `useObservatorySocket`'s
// answer: while the socket is up nothing polls, and while it is down the query hooks fall
// back to a slow re-read and the header says so rather than looking live.
const client = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
});

// The theme is applied here as well as before the first paint (index.html), because the
// platform can change while the screen is open and a rendered document's tones follow it.
migrateLegacyHash();
applyColorTheme();
watchColorTheme();

const root = document.getElementById("root");
if (!root) throw new Error("index.html carries no #root element");
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
