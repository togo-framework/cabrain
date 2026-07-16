import { useEffect, useState } from "react";
import { ProfileView, useT, Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@togo-framework/ui";
import { Languages } from "lucide-react";
import { sessionMe, type Me } from "../lib/auth";

// Add a language here to offer it across the app (it also needs strings in the kit's
// LanguageProvider). The profile uses a dropdown so the list scales beyond two.
const LANGUAGES = [
  { code: "en", label: "English" },
  { code: "ar", label: "العربية" },
] as const;

export function Profile() {
  const { language, setLanguage } = useT();
  const ar = language === "ar";
  const [me, setMe] = useState<Me | null>(null);
  useEffect(() => { sessionMe().then(setMe); }, []);
  if (!me) return <div className="p-8 text-muted-foreground">{ar ? "جارٍ التحميل…" : "Loading…"}</div>;

  return (
    <div dir={ar ? "rtl" : "ltr"}>
      <ProfileView user={{ email: me.email, roles: me.roles }} language={language} twoFactorEnabled={false} sessions={[]} />

      {/* Language preference — switching it updates the whole UI immediately (LanguageProvider). */}
      <div className="mx-auto max-w-5xl px-6 pb-10">
        <div className="rounded-xl border border-border bg-card p-5">
          <div className="mb-1 flex items-center gap-2 text-sm font-semibold"><Languages className="h-4 w-4" />{ar ? "اللغة" : "Language"}</div>
          <p className="mb-4 text-sm text-muted-foreground">{ar ? "تغيير لغة الواجهة — يُطبّق فورًا." : "Change the interface language — applies instantly."}</p>
          <Select value={language} onValueChange={(v) => setLanguage(v as "en" | "ar")}>
            <SelectTrigger className="w-64" aria-label={ar ? "اللغة" : "Language"}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {LANGUAGES.map((l) => (
                <SelectItem key={l.code} value={l.code}>{l.label}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}
