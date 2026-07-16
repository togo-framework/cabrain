import { API } from "./api";

export async function adminList(table: string): Promise<any[]> {
  const r = await fetch(`${API}/api/${table}`, { credentials: "include" });
  if (!r.ok) throw new Error(`load failed (${r.status})`);
  const d = await r.json();
  return Array.isArray(d) ? d : (d.items ?? d.data ?? []);
}

export interface PagedResult {
  items: any[];
  total: number;
  page: number;
  pageSize: number;
}

/** Fetch a resource page with server-side pagination and optional sort/search.
 * Falls back to slicing the flat list if the API doesn't support pagination params. */
export async function adminListPaged(
  table: string,
  opts: { page: number; pageSize: number; sort?: string; order?: "asc" | "desc"; search?: string },
): Promise<PagedResult> {
  const qs = new URLSearchParams();
  qs.set("page", String(opts.page));
  qs.set("page_size", String(opts.pageSize));
  if (opts.sort) qs.set("sort", opts.sort);
  if (opts.order) qs.set("order", opts.order);
  if (opts.search) qs.set("q", opts.search);

  const r = await fetch(`${API}/api/${table}?${qs}`, { credentials: "include" });
  if (!r.ok) throw new Error(`load failed (${r.status})`);
  const d = await r.json();
  // Server returns { items, total } → use directly.
  if (d && typeof d.total === "number") {
    return { items: d.items ?? d.data ?? [], total: d.total, page: opts.page, pageSize: opts.pageSize };
  }
  // Flat array → client-side slice (graceful degradation).
  const all: any[] = Array.isArray(d) ? d : (d.items ?? d.data ?? []);
  const start = (opts.page - 1) * opts.pageSize;
  return { items: all.slice(start, start + opts.pageSize), total: all.length, page: opts.page, pageSize: opts.pageSize };
}
export async function adminGet(table: string, id: string): Promise<any> {
  const r = await fetch(`${API}/api/${table}/${id}`, { credentials: "include" });
  if (!r.ok) throw new Error(`load failed (${r.status})`);
  return r.json();
}
async function csrf(): Promise<string> {
  const res = await fetch(`${API}/api/auth/csrf`, { credentials: "include" });
  return (await res.json().catch(() => ({}))).csrf_token ?? "";
}
async function write(method: string, path: string, body?: unknown) {
  const token = await csrf();
  const r = await fetch(`${API}${path}`, {
    method, credentials: "include",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok && r.status !== 204) {
    const d = await r.json().catch(() => ({}));
    throw new Error(d.error || d.detail || `${method} failed (${r.status})`);
  }
}
export const adminCreate = (t: string, data: Record<string, unknown>) => write("POST", `/api/${t}`, data);
export const adminUpdate = (t: string, id: string, data: Record<string, unknown>) => write("PUT", `/api/${t}/${id}`, data);
export const adminDelete = (t: string, id: string) => write("DELETE", `/api/${t}/${id}`);

export interface ResourceField { name: string; type: string; nullable: boolean; enum?: string[]; relation?: string }
export interface ResourceMeta {
  name: string;
  table: string;
  fields?: ResourceField[];
  /** Optional sidebar group label (e.g. "Content", "Commerce"). Groups sidebar nav. */
  group?: string;
  /** Optional lucide icon name for the sidebar entry (e.g. "users", "package"). */
  icon?: string;
}

/** The form control a field renders with — Filament-style, derived from the schema. */
export type Control = "switch" | "number" | "datetime" | "date" | "textarea" | "email" | "select" | "relation" | "json" | "text";

/** Pluralize a singular relation/base name to its REST table (best-effort, matches togo's table naming). */
export function pluralize(s: string): string {
  if (/[^aeiou]y$/.test(s)) return s.replace(/y$/, "ies");
  if (/(s|x|z|ch|sh)$/.test(s)) return s + "es";
  return s + "s";
}

/** The related table for a belongs-to field: explicit `relation:` wins, else infer from a `*_id` name. */
export function relationTable(f: ResourceField): string | null {
  if (f.relation) return f.relation.includes("/") ? f.relation : pluralize(f.relation.replace(/s$/, ""));
  if (/_id$/.test(f.name) && /int|uint|number/.test(f.type.toLowerCase())) return pluralize(f.name.replace(/_id$/, ""));
  return null;
}

/** Pick the form control for a field from its declared type/name (the schema drives the UI). */
export function controlFor(f: ResourceField): Control {
  const t = f.type.toLowerCase();
  if (relationTable(f)) return "relation";
  if (f.enum && f.enum.length) return "select";
  if (/bool/.test(t)) return "switch";
  if (/json|jsonb|map|\[\]/.test(t)) return "json";
  if (/time|date/.test(t)) return /^date$/.test(t) || /\bdate\b/.test(f.name) ? "date" : "datetime";
  if (/int|float|decimal|number|uint/.test(t)) return "number";
  if (/email/.test(f.name)) return "email";
  if (t === "text" || /body|content|description|bio|notes?|message/.test(f.name)) return "textarea";
  return "text";
}

/** A human label for a row: prefer a name/title/email field, else #id. */
export function rowLabel(row: Record<string, any>): string {
  for (const k of ["name", "title", "label", "email", "slug"]) if (row?.[k]) return String(row[k]);
  return row?.id != null ? `#${row.id}` : "—";
}

/** Format a value for a table cell / infolist, given its field type. */
export function formatValue(f: ResourceField | undefined, v: any, language = "en"): string {
  if (v === null || v === undefined || v === "") return "—";
  const t = (f?.type ?? "").toLowerCase();
  if (/time|date/.test(t) || /_at$/.test(f?.name ?? "")) {
    const d = new Date(v); if (!isNaN(d.getTime())) return d.toLocaleString(language === "ar" ? "ar" : "en", { dateStyle: "medium", timeStyle: "short" });
  }
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

/** Required + format validation derived from the schema. Returns an error key or "". */
export function validateField(f: ResourceField, raw: string): string {
  const v = (raw ?? "").trim();
  if (!f.nullable && v === "" && controlFor(f) !== "switch") return "required";
  if (v === "") return "";
  const c = controlFor(f);
  if (c === "email" && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v)) return "email";
  if (c === "number" && isNaN(Number(v))) return "number";
  return "";
}

export async function metaResources(): Promise<ResourceMeta[]> {
  const r = await fetch(`${API}/api/_meta/resources`, { credentials: "include" }).catch(() => null);
  if (!r || !r.ok) return [];
  return (await r.json().catch(() => ({ resources: [] }))).resources ?? [];
}

const SKIP = ["id", "created_at", "updated_at"];

/** Editable fields for a resource, sourced from the resource DEFINITION (so create
 * forms work on an empty table). Falls back to inferring from a sample row. */
export async function resourceFields(table: string, sampleRow?: Record<string, unknown>): Promise<ResourceField[]> {
  const meta = (await metaResources()).find((m) => m.table === table || m.name === table);
  if (meta?.fields?.length) return meta.fields.filter((f) => !SKIP.includes(f.name));
  if (sampleRow) return editableColumns(sampleRow).map((name) => ({ name, type: "string", nullable: true }));
  return [];
}

export function editableColumns(row: Record<string, unknown>): string[] {
  return Object.keys(row).filter((k) => !SKIP.includes(k));
}

/** Map a resource field's go-type to an HTML input type. */
export function inputType(field: ResourceField): string {
  const t = field.type.toLowerCase();
  if (/int|float|decimal|number/.test(t)) return "number";
  if (/time|date/.test(t)) return "datetime-local";
  if (/bool/.test(t)) return "checkbox";
  return "text";
}
