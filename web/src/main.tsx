import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import "./shared/i18n";
import { ThemeProvider } from "./shared/theme/ThemeProvider";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnReconnect: true,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </ThemeProvider>
  </React.StrictMode>,
);