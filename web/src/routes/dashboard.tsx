import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { StatCard, Card, MiniBarChart, useT, type BarPoint } from "@togo-framework/ui";
import { sessionMe, type Me } from "../lib/auth";
import { metaResources, adminList, type ResourceMeta } from "../lib/admin";

const labelOf = (s: string) => s.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

export function Dashboard() {
  const { language } = useT();
  const ar = language === "ar";
  const [me, setMe] = useState<Me | null>(null);
  const [counts, setCounts] = useState<{ meta: ResourceMeta; count: number }[]>([]);
  const [trend, setTrend] = useState<BarPoint[]>([]);

  useEffect(() => { sessionMe().then(setMe); }, []);
  useEffect(() => {
    metaResources().then(async (ms) => {
      const all = await Promise.all(ms.map(async (m) => ({ meta: m, rows: await adminList(m.table).catch(() => []) })));
      setCounts(all.map(({ meta, rows }) => ({ meta, count: rows.length })));
      // Records-created-per-day over the last 7 days, across every resource (uses created_at).
      const days: { key: string; label: string }[] = [];
      for (let i = 6; i >= 0; i--) {
        const d = new Date(); d.setDate(d.getDate() - i);
        days.push({ key: d.toISOString().slice(0, 10), label: d.toLocaleDateString(ar ? "ar" : "en", { weekday: "short" }) });
      }
      const bucket: Record<string, number> = Object.fromEntries(days.map((d) => [d.key, 0]));
      for (const { rows } of all) for (const r of rows) {
        const ts = r.created_at ?? r.createdAt;
        if (ts) { const k = new Date(ts).toISOString().slice(0, 10); if (k in bucket) bucket[k]++; }
      }
      setTrend(days.map((d) => ({ label: d.label, value: bucket[d.key] })));
    });
  }, [ar]);

  if (!me) return <div className="p-6 text-muted-foreground">{ar ? "جارٍ التحميل…" : "Loading…"}</div>;

  const byResource: BarPoint[] = counts.map(({ meta, count }) => ({ label: labelOf(meta.name || meta.table), value: count }));
  const total = counts.reduce((s, c) => s + c.count, 0);
  const hasTrend = trend.some((p) => p.value > 0);

  return (
    <div className="space-y-6 p-6" dir={ar ? "rtl" : "ltr"}>
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{ar ? "لوحة التحكم" : "Dashboard"}</h1>
        <p className="text-sm text-muted-foreground">{ar ? `مرحبًا بعودتك، ${me.email}` : `Welcome back, ${me.email}`}</p>
      </div>

      {/* Stat-card widgets — one per registered resource (record count). */}
      {counts.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {counts.map(({ meta, count }) => (
            <Link key={meta.table} to="/admin/$resource" params={{ resource: meta.table }} className="block transition-transform hover:-translate-y-0.5">
              <StatCard label={labelOf(meta.name || meta.table)} value={String(count)} tone="info" />
            </Link>
          ))}
        </div>
      )}

      {/* Chart widgets. */}
      {counts.length > 0 && (
        <div className="grid gap-4 lg:grid-cols-2">
          <Card className="p-5">
            <div className="mb-3 flex items-baseline justify-between">
              <h2 className="text-sm font-semibold">{ar ? "السجلات حسب المورد" : "Records by resource"}</h2>
              <span className="text-xs text-muted-foreground">{total} {ar ? "إجمالي" : "total"}</span>
            </div>
            <MiniBarChart data={byResource} height={160} />
          </Card>
          <Card className="p-5">
            <h2 className="mb-3 text-sm font-semibold">{ar ? "سجلات جديدة (7 أيام)" : "New records (7 days)"}</h2>
            {hasTrend ? <MiniBarChart data={trend} height={160} />
              : <p className="py-10 text-center text-sm text-muted-foreground">{ar ? "لا توجد بيانات بعد" : "No timestamped records yet"}</p>}
          </Card>
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <StatCard label={ar ? "الحساب" : "Account"} value={me.email} />
        <StatCard label={ar ? "الأدوار" : "Roles"} value={(me.roles ?? ["user"]).join(", ")} tone="info" />
        <StatCard label={ar ? "الصلاحيات" : "Permissions"} value={String((me.permissions ?? []).length)} tone="success" />
      </div>
    </div>
  );
}
