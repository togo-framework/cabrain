import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import {
  Layers, LayoutGrid, BookOpen, Boxes, FileText, Blocks, Github, ArrowRight, ArrowLeft,
} from "lucide-react";
import { Button, useT } from "@togo-framework/ui";
import { API, APP_NAME } from "../lib/api";
import { sessionMe, type Me } from "../lib/auth";

type Card = {
  icon: typeof Layers;
  en: string; ar: string;
  descEn: string; descAr: string;
  to?: string; href?: string;
};

// The resource grid — Laravel-welcome style: where a fresh togo app sends you next.
const CARDS: Card[] = [
  { icon: LayoutGrid, en: "Dashboard", ar: "لوحة التحكم", descEn: "Your app's admin panel — account, roles, resources.", descAr: "لوحة الإدارة — الحساب والأدوار والموارد.", to: "/dashboard" },
  { icon: BookOpen, en: "REST API", ar: "واجهة REST", descEn: "Typed OpenAPI 3.1 docs for every resource.", descAr: "توثيق OpenAPI 3.1 لكل مورد.", href: `${API}/docs` },
  { icon: Boxes, en: "GraphQL", ar: "GraphQL", descEn: "Explore the schema in the GraphQL playground.", descAr: "استكشف المخطط في GraphQL.", href: `${API}/graphql/play` },
  { icon: FileText, en: "Documentation", ar: "التوثيق", descEn: "Guides, generators and the togo CLI.", descAr: "أدلة ومولّدات وواجهة togo.", href: "https://to-go.dev/docs" },
  { icon: Blocks, en: "Plugins", ar: "الإضافات", descEn: "Add auth, cache, queue, storage in one command.", descAr: "أضف المصادقة والتخزين بأمر واحد.", href: "https://to-go.dev/plugins" },
  { icon: Github, en: "GitHub", ar: "GitHub", descEn: "Source, issues and the framework repos.", descAr: "المصدر والمستودعات.", href: "https://github.com/togo-framework" },
];

export function Welcome() {
  const { language } = useT();
  const ar = language === "ar";
  const tx = (en: string, a: string) => (ar ? a : en);
  const Arrow = ar ? ArrowLeft : ArrowRight;

  const [health, setHealth] = useState<{ status?: string; togo?: string } | null>(null);
  const [me, setMe] = useState<Me | null | undefined>(undefined);

  useEffect(() => {
    fetch(`${API}/api/health`).then((r) => r.json()).then(setHealth).catch(() => setHealth(null));
    sessionMe().then(setMe).catch(() => setMe(null));
  }, []);

  const online = health?.status === "ok";

  return (
    <main dir={ar ? "rtl" : "ltr"} className="relative min-h-screen overflow-hidden bg-background text-foreground">
      {/* subtle brand glow — theme-aware, decorative */}
      <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-96"
        style={{ background: "radial-gradient(620px 320px at 50% -4%, color-mix(in srgb, var(--primary) 22%, transparent), transparent 70%)" }} />

      <div className="mx-auto w-full max-w-4xl px-6 py-16 sm:py-20">
        {/* hero */}
        <header className="text-center">
          <div className="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-2xl text-white shadow-lg"
            style={{ background: "linear-gradient(135deg,#1FC7DC,#2D8CE6 55%,#1659C8)" }}>
            <Layers className="h-8 w-8" />
          </div>
          <h1 className="text-4xl font-extrabold tracking-tight sm:text-5xl">{APP_NAME}</h1>
          <p className="mx-auto mt-3 max-w-xl text-base text-muted-foreground sm:text-lg">
            {tx("Built with togo — your Go API and React UI, shipped as one binary.",
                "مبنيٌّ باستخدام togo — واجهة Go وتطبيق React في ثنائيّة واحدة.")}
          </p>

          {/* auth-aware CTAs */}
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            {me ? (
              <Button asChild size="lg" className="w-full sm:w-auto">
                <Link to="/dashboard">{tx("Go to dashboard", "اذهب إلى لوحة التحكم")} <Arrow className="ms-1 h-4 w-4" /></Link>
              </Button>
            ) : (
              <>
                <Button asChild size="lg" className="w-full sm:w-auto">
                  <Link to="/login">{tx("Log in", "تسجيل الدخول")}</Link>
                </Button>
                <Button asChild size="lg" variant="outline" className="w-full sm:w-auto">
                  <Link to="/register">{tx("Create account", "إنشاء حساب")}</Link>
                </Button>
              </>
            )}
          </div>
        </header>

        {/* resource grid — Laravel-welcome style */}
        <div className="mt-14 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {CARDS.map((c) => {
            const inner = (
              <>
                <div className="flex items-center gap-3">
                  <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-inset ring-primary/15">
                    <c.icon className="h-5 w-5" />
                  </span>
                  <span className="font-semibold">{tx(c.en, c.ar)}</span>
                  <Arrow className="ms-auto h-4 w-4 text-muted-foreground/50 transition-all group-hover:text-primary group-hover:translate-x-0.5 rtl:group-hover:-translate-x-0.5" />
                </div>
                <p className="mt-3 text-sm text-muted-foreground">{tx(c.descEn, c.descAr)}</p>
              </>
            );
            const cls = "group block rounded-2xl border border-border bg-card p-5 text-start transition-colors hover:border-primary/40 hover:bg-accent/40";
            return c.to ? (
              <Link key={c.en} to={c.to} className={cls}>{inner}</Link>
            ) : (
              <a key={c.en} href={c.href} target={c.href?.startsWith("http") ? "_blank" : undefined} rel="noopener noreferrer" className={cls}>{inner}</a>
            );
          })}
        </div>

        {/* footer status */}
        <footer className="mt-14 flex flex-wrap items-center justify-center gap-x-3 gap-y-2 text-xs text-muted-foreground">
          <span className="inline-flex items-center gap-1.5">
            <span className={`h-2 w-2 rounded-full ${online ? "bg-emerald-400" : "bg-muted-foreground/40"}`} />
            {tx(online ? "API connected" : "API offline", online ? "الواجهة متّصلة" : "الواجهة غير متّصلة")}
          </span>
          <span aria-hidden>·</span>
          <span>togo {health?.togo ?? "…"}</span>
          <span aria-hidden>·</span>
          <span>{tx("powered by Go", "مدعوم بـ Go")}</span>
        </footer>
      </div>
    </main>
  );
}
