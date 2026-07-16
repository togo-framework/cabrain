import { ReactNode } from "react";
import { ThemeProvider, LanguageProvider, themes } from "@togo-framework/ui";

// ThemeProvider applies the ToGO brand tokens + multi-theme switching (data-theme on
// <html>, persisted to localStorage). The full `themes` list enables the ThemePicker
// to cycle through all presets (dark, light, purple, rose, emerald, and light variants).
// LanguageProvider supplies EN/AR i18n + RTL.
export function Providers({ children }: { children: ReactNode }) {
  return (
    <ThemeProvider themes={themes}>
      <LanguageProvider initialLanguage="en">{children}</LanguageProvider>
    </ThemeProvider>
  );
}
