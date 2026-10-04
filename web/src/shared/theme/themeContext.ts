import { createContext } from "react";

type Theme = "light" | "dark";
type ThemePreference = Theme | "system";

type ThemeContextValue = {
  theme: Theme;
  toggleTheme: () => void;
};

const themeStorageKey = "zhulong.theme.v1";
const ThemeContext = createContext<ThemeContextValue | null>(null);

export { ThemeContext, themeStorageKey };
export type { Theme, ThemeContextValue, ThemePreference };
