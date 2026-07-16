import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { Pencil, Trash2, Eye, Plus } from "lucide-react";
import {
  PageHeader, Button, Badge, DataTable,
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
  AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogCancel, AlertDialogAction,
  useT,
  type ColumnDef, type SortingState, type PaginationState, type DataTableServerState,
} from "@togo-framework/ui";
import {
  adminListPaged, adminCreate, adminUpdate, adminDelete, resourceFields,
  controlFor, formatValue, type ResourceField, type PagedResult,
} from "../lib/admin";
import { ResourceForm, validateForm } from "../components/admin/ResourceForm";
import { Infolist } from "../components/admin/Infolist";
import { useToast } from "../components/admin/toast";
import { API } from "../lib/api";

type Row = Record<string, any>;
type Mode = "create" | "edit" | "view" | "delete";

const labelOf = (name: string) => name.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

const DEFAULT_PAGE_SIZE = 20;

export function AdminResource() {
  const { resource } = useParams({ strict: false }) as { resource: string };
  const { language } = useT();
  const { toast } = useToast();
  const ar = language === "ar";
  const dir = ar ? "rtl" : "ltr";
  const single = resource.replace(/s$/, "");

  // Data state
  const [result, setResult] = useState<PagedResult | null>(null);
  const [fields, setFields] = useState<ResourceField[]>([]);
  const [err, setErr] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  // Server-side state (lifted from DataTable via serverCallbacks)
  const [serverSorting, setServerSorting] = useState<SortingState>([]);
  const [serverPagination, setServerPagination] = useState<PaginationState>({ pageIndex: 0, pageSize: DEFAULT_PAGE_SIZE });
  const [serverGlobalFilter, setServerGlobalFilter] = useState("");

  // Modal + form state
  const [modal, setModal] = useState<{ mode: Mode; row?: Row } | null>(null);
  const [form, setForm] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  // Track current resource so we reset pagination on navigation
  const resourceRef = useRef(resource);

  const refresh = useCallback(async (
    pagination: PaginationState = serverPagination,
    sorting: SortingState = serverSorting,
    globalFilter: string = serverGlobalFilter,
  ) => {
    const s = sorting[0];
    const r = await adminListPaged(resource, {
      page: pagination.pageIndex + 1,
      pageSize: pagination.pageSize,
      sort: s?.id,
      order: s ? (s.desc ? "desc" : "asc") : undefined,
      search: globalFilter || undefined,
    }).catch(() => ({ items: [], total: 0, page: 1, pageSize: pagination.pageSize }));
    setResult(r);
  }, [resource, serverPagination, serverSorting, serverGlobalFilter]);

  useEffect(() => {
    // Reset state when switching resources
    if (resourceRef.current !== resource) {
      resourceRef.current = resource;
      setServerSorting([]);
      setServerPagination({ pageIndex: 0, pageSize: DEFAULT_PAGE_SIZE });
      setServerGlobalFilter("");
    }
    setResult(null);
    resourceFields(resource).then(setFields);
    refresh({ pageIndex: 0, pageSize: DEFAULT_PAGE_SIZE }, [], "");

    const es = new EventSource(`${API}/events`);
    es.onmessage = () => refresh();
    return () => es.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resource]);

  // DataTable server-side callback — called by DataTable on sort/filter/page change.
  const serverCallbacks = useMemo(() => ({
    onStateChange: (state: DataTableServerState) => {
      setServerSorting(state.sorting);
      setServerPagination(state.pagination);
      setServerGlobalFilter(state.globalFilter);
      refresh(state.pagination, state.sorting, state.globalFilter);
    },
  }), [refresh]);

  function open(mode: Mode, row?: Row) {
    const init: Record<string, string> = {};
    fields.forEach((f) => (init[f.name] = row ? String(row[f.name] ?? "") : ""));
    setForm(init); setErr(""); setErrors({}); setModal({ mode, row });
  }

  async function save() {
    setErr("");
    const { errors: errs, ok } = validateForm(fields, form);
    setErrors(errs);
    if (!ok) return;
    setSaving(true);
    const payload: Record<string, unknown> = {};
    for (const f of fields) {
      const v = form[f.name] ?? "";
      const c = controlFor(f);
      if (v === "") { if (!f.nullable && c !== "switch") payload[f.name] = ""; continue; }
      payload[f.name] = c === "number" || c === "relation" ? Number(v) : c === "switch" ? v === "true" : c === "json" ? safeJson(v) : v;
    }
    try {
      if (modal?.mode === "edit") { await adminUpdate(resource, modal.row!.id, payload); toast(ar ? "تم التحديث" : "Updated"); }
      else { await adminCreate(resource, payload); toast(ar ? "تم الإنشاء" : "Created"); }
      setModal(null); await refresh();
    } catch (e: any) { setErr(e.message); toast(e.message, "error"); }
    finally { setSaving(false); }
  }

  async function del(ids?: string[]) {
    setErr("");
    try {
      const targets = ids ?? (modal?.row ? [String(modal.row.id)] : []);
      await Promise.all(targets.map((id) => adminDelete(resource, id)));
      setModal(null); toast(ar ? `تم حذف ${targets.length}` : `Deleted ${targets.length}`); await refresh();
    } catch (e: any) { setErr(e.message); toast(e.message, "error"); }
  }

  const columns: ColumnDef<Row>[] = useMemo(() => [
    { accessorKey: "id", header: "id", cell: ({ getValue }) => <span className="text-muted-foreground">#{String(getValue())}</span> },
    ...fields.map((f) => ({
      accessorKey: f.name,
      header: labelOf(f.name),
      cell: ({ getValue }: any) => <Cell f={f} v={getValue()} language={language} />,
    }) as ColumnDef<Row>),
    {
      id: "actions",
      header: () => <span className="block text-end">{ar ? "إجراءات" : "Actions"}</span>,
      enableSorting: false, enableHiding: false,
      cell: ({ row }) => (
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="ghost" aria-label="view" onClick={(e) => { e.stopPropagation(); open("view", row.original); }}><Eye className="h-3.5 w-3.5" /></Button>
          <Button size="sm" variant="ghost" aria-label="edit" onClick={(e) => { e.stopPropagation(); open("edit", row.original); }}><Pencil className="h-3.5 w-3.5" /></Button>
          <Button size="sm" variant="ghost" aria-label="delete" className="text-destructive" onClick={(e) => { e.stopPropagation(); setModal({ mode: "delete", row: row.original }); }}><Trash2 className="h-3.5 w-3.5" /></Button>
        </div>
      ),
    },
  ], [fields, language, ar]);

  // Per-column select filters for enum + boolean fields (Filament-style table filters).
  const filterDefs = useMemo(() =>
    fields.filter((f) => f.enum?.length || /bool/.test(f.type.toLowerCase())).map((f) => ({
      columnId: f.name,
      type: "select" as const,
      options: (f.enum?.length ? f.enum : ["true", "false"]).map((v) => ({ value: v, label_en: v, label_ar: v })),
      placeholder_en: labelOf(f.name), placeholder_ar: labelOf(f.name),
    })), [fields]);

  const rows = result?.items ?? [];
  const bulkActions = useMemo(() => [
    { id: "export", label_en: "Export", label_ar: "تصدير", variant: "outline" as const, onAction: (ids: string[]) => exportRows(rows.filter((r) => ids.includes(String(r.id))), resource) },
    { id: "delete", label_en: "Delete", label_ar: "حذف", variant: "destructive" as const, onAction: (ids: string[]) => del(ids) },
  ], [rows, resource, ar]);

  return (
    <div className="space-y-6 p-6" dir={dir}>
      <PageHeader title={labelOf(resource)} description={`${result?.total ?? 0} ${ar ? "سجل" : "records"}`}
        actions={<Button onClick={() => open("create")}><Plus className="me-1.5 h-4 w-4" />{ar ? "إضافة" : "Create"}</Button>} />
      {err && <p className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{err}</p>}

      <DataTable
        columns={columns}
        data={rows}
        getRowId={(r) => String((r as Row).id)}
        loading={result === null}
        showGlobalSearch
        showCsvExport
        csvFilename={resource}
        enableRowSelection
        bulkActions={bulkActions}
        filterDefs={filterDefs}
        onRowClick={(r) => open("view", r as Row)}
        language={language}
        /* Server-side mode: pagination, sorting, and global search are all server-driven.
           DataTable fires onStateChange whenever any of these change; we re-fetch from the API. */
        totalRows={result?.total}
        serverCallbacks={serverCallbacks}
      />

      {/* Create / edit — schema form with validation + relation pickers. */}
      <Dialog open={modal?.mode === "create" || modal?.mode === "edit"} onOpenChange={(o) => !o && setModal(null)}>
        <DialogContent dir={dir} className="sm:max-w-2xl">
          <DialogHeader><DialogTitle className="capitalize">{modal?.mode === "edit" ? (ar ? "تعديل" : "Edit") : (ar ? "إضافة" : "Create")} {single}</DialogTitle></DialogHeader>
          <div className="max-h-[60vh] overflow-y-auto px-0.5 py-1">
            <ResourceForm fields={fields} value={form} errors={errors} onChange={setForm} language={language} />
          </div>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setModal(null)}>{ar ? "إلغاء" : "Cancel"}</Button>
            <Button onClick={save} disabled={saving}>{saving ? (ar ? "جارٍ الحفظ…" : "Saving…") : (ar ? "حفظ" : "Save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* View — infolist. */}
      <Dialog open={modal?.mode === "view"} onOpenChange={(o) => !o && setModal(null)}>
        <DialogContent dir={dir} className="sm:max-w-xl">
          <DialogHeader><DialogTitle className="capitalize">{single}</DialogTitle></DialogHeader>
          {modal?.row && <Infolist row={modal.row} fields={fields} language={language} />}
          <DialogFooter>
            <Button variant="secondary" onClick={() => setModal(null)}>{ar ? "إغلاق" : "Close"}</Button>
            <Button onClick={() => modal?.row && open("edit", modal.row)}>{ar ? "تعديل" : "Edit"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation. */}
      <AlertDialog open={modal?.mode === "delete"} onOpenChange={(o) => !o && setModal(null)}>
        <AlertDialogContent dir={dir}>
          <AlertDialogHeader>
            <AlertDialogTitle>{ar ? "حذف السجل" : "Delete record"}</AlertDialogTitle>
            <AlertDialogDescription>{ar ? "لا يمكن التراجع عن هذا الإجراء. حذف هذا السجل؟" : "This action cannot be undone. Delete this record?"}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{ar ? "إلغاء" : "Cancel"}</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-destructive-foreground hover:bg-destructive/90" onClick={() => del()}>{ar ? "حذف" : "Delete"}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function Cell({ f, v, language }: { f: ResourceField; v: any; language: string }) {
  const c = controlFor(f);
  if (v === null || v === undefined || v === "") return <span className="text-muted-foreground/50">—</span>;
  if (c === "switch" || typeof v === "boolean") {
    const on = v === true || v === "true";
    return <Badge variant={on ? "default" : "secondary"}>{on ? "Yes" : "No"}</Badge>;
  }
  if (c === "select") return <Badge variant="secondary" className="capitalize">{String(v)}</Badge>;
  if (c === "relation") return <Badge variant="outline">#{String(v)}</Badge>;
  const text = formatValue(f, v, language);
  return <span className="line-clamp-1 max-w-[28ch]">{text}</span>;
}

function safeJson(s: string): unknown { try { return JSON.parse(s); } catch { return s; } }

/** Export selected rows as a CSV download (bulk action). */
function exportRows(rows: Record<string, any>[], name: string) {
  if (!rows.length) return;
  const cols = Array.from(new Set(rows.flatMap((r) => Object.keys(r))));
  const esc = (v: any) => `"${String(v ?? "").replace(/"/g, '""')}"`;
  const csv = [cols.join(","), ...rows.map((r) => cols.map((c) => esc(r[c])).join(","))].join("\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = `${name}-selected.csv`; a.click(); URL.revokeObjectURL(url);
}
