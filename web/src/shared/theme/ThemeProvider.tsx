import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ThemeContext, themeStorageKey } from "./themeContext";
import type { Theme, ThemePreference } from "./themeContext";

function readThemePreference(): ThemePreference {
  try {
    const storedTheme = window.localStorage.getItem(themeStorageKey);
    if (storedTheme === "light" || storedTheme === "dark") return storedTheme;
  } catch {
    return "system";
  }
  return "system";
}

function systemPrefersDark(): boolean {
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreference] = useState<ThemePreference>(readThemePreference);
  const [prefersDark, setPrefersDark] = useState(systemPrefersDark);
  const theme: Theme = preference === "system" ? (prefersDark ? "dark" : "light") : preference;

  useEffect(() => {
    const mediaQuery = window.matchMedia?.("(prefers-color-scheme: dark)");
    if (!mediaQuery) return undefined;
    const updateSystemTheme = (event: MediaQueryListEvent) => setPrefersDark(event.matches);
    mediaQuery.addEventListener("change", updateSystemTheme);
    return () => mediaQuery.removeEventListener("change", updateSystemTheme);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
  }, [theme]);

  const toggleTheme = () => {
    const nextTheme: Theme = theme === "dark" ? "light" : "dark";
    setPreference(nextTheme);
    try {
      window.localStorage.setItem(themeStorageKey, nextTheme);
    } catch {
      return;
    }
  };

  return <ThemeContext.Provider value={{ theme, toggleTheme }}>{children}</ThemeContext.Provider>;
}
