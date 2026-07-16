import { Badge } from "@togo-framework/ui";
import { controlFor, formatValue, type ResourceField } from "../../lib/admin";

const labelOf = (name: string) => name.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

/** A Filament-style infolist: labelled key/value rows with badges for enums/bools,
 * formatted dates, and a — placeholder for empties. Driven by the resource schema. */
export function Infolist({ row, fields, language = "en" }: { row: Record<string, any>; fields: ResourceField[]; language?: string }) {
  const byName = new Map(fields.map((f) => [f.name, f]));
  // Show schema fields first (in declared order), then any extra row keys (id/timestamps).
  const keys = [
    "id",
    ...fields.map((f) => f.name).filter((n) => n !== "id"),
    ...Object.keys(row).filter((k) => k !== "id" && !byName.has(k)),
  ].filter((k, i, a) => a.indexOf(k) === i && k in row);

  return (
    <dl className="divide-y divide-border/60 rounded-lg border border-border text-sm">
      {keys.map((k) => {
        const f = byName.get(k);
        const v = row[k];
        return (
          <div key={k} className="flex gap-3 px-3 py-2.5">
            <dt className="w-40 shrink-0 text-muted-foreground">{labelOf(k)}</dt>
            <dd className="min-w-0 break-words font-medium"><Value f={f} v={v} language={language} /></dd>
          </div>
        );
      })}
    </dl>
  );
}

function Value({ f, v, language }: { f?: ResourceField; v: any; language: string }) {
  if (v === null || v === undefined || v === "") return <span className="text-muted-foreground/60">—</span>;
  const control = f ? controlFor(f) : "text";
  if (control === "switch" || typeof v === "boolean") {
    const on = v === true || v === "true";
    return <Badge variant={on ? "default" : "secondary"}>{on ? "Yes" : "No"}</Badge>;
  }
  if (control === "select") return <Badge variant="secondary" className="capitalize">{String(v)}</Badge>;
  if (control === "relation") return <Badge variant="outline">#{String(v)}</Badge>;
  return <span>{formatValue(f, v, language)}</span>;
}
