import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Boxes, Plus, Search, Rocket, Download, Trash2, MoreHorizontal,
  ArrowRight, HelpCircle, LayoutGrid, List, Waypoints, Activity, Lock, Loader2,
} from "lucide-react";
import {
  Button, Input, Badge, StatusBadge, StatCard, PageHeader, EmptyState, Skeleton,
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator,
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
  ToggleGroup, ToggleGroupItem,
} from "@togo-framework/ui";
import { brainApi, type BrainDetail, type NamespaceInfo } from "../lib/brain";
import { hueForBrain } from "../lib/brain-colors";
import { LaunchSessionModal } from "../components/launch-session-modal";

/* ---------------------------------------------------------------- helpers -- */

function relTime(iso?: string): string {
  if (!iso) return "never";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "never";
  const s = Math.max(1, Math.floor((Date.now() - t) / 1000));
  const units: [number, string][] = [
    [60, "s"], [3600, "m"], [86400, "h"], [604800, "d"], [2629800, "w"], [31557600, "mo"],
  ];
  let prev = 1;
  for (const [limit, label] of units) {
    if (s < limit) return `${Math.floor(s / prev)}${label} ago`;
    prev = limit;
  }
  return `${Math.floor(s / 31557600)}y ago`;
}

function nf(n?: number) { return (n ?? 0).toLocaleString(); }

/** A restrained per-brain identity monogram — a tinted tile in the brain's own
 *  stable hue. Identity without the rainbow: one accent, low chroma, no glow. */
export function BrainAvatar({ namespace, size = 40 }: { namespace: string; size?: number }) {
  const c = hueForBrain(namespace);
  return (
    <span
      aria-hidden
      className="flex shrink-0 items-center justify-center rounded-lg font-semibold uppercase"
      style={{
        height: size, width: size, fontSize: size * 0.42,
        color: c, background: `color-mix(in srgb, ${c} 14%, transparent)`,
        border: `1px solid color-mix(in srgb, ${c} 30%, transparent)`,
      }}
    >
      {namespace.slice(0, 1)}
    </span>
  );
}

/* ------------------------------------------------------------ create modal -- */

function NewBrainDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const slug = name.trim().toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "");
  const create = useMutation({
    mutationFn: () =>
      brainApi.retain({
        namespace: slug,
        content: `Brain "${slug}" created from the console.${desc.trim() ? " " + desc.trim() : ""}`,
        sourceKind: "system",
        sourceRef: "console/new-brain",
      }),
    onSuccess: (res) => {
      if ((res as { error?: unknown }).error) return;
      qc.invalidateQueries({ queryKey: ["brain", "namespaces"] });
      qc.invalidateQueries({ queryKey: ["brain", "stats"] });
      setName(""); setDesc(""); onClose();
    },
  });

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New brain</DialogTitle>
          <DialogDescription>
            A brain is a namespace. Creating one seeds a marker so it exists and is connectable.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <label className="text-sm font-medium">Name</label>
            <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. research" />
            {name && slug !== name.trim().toLowerCase() && (
              <p className="text-xs text-muted-foreground">Namespace: <code className="font-mono">{slug || "—"}</code></p>
            )}
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium">Description <span className="text-muted-foreground">(optional)</span></label>
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="What this brain holds" />
          </div>
          {(create.data as { error?: { message?: string } } | undefined)?.error && (
            <p className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {(create.data as { error: { message?: string } }).error.message ?? "Could not create brain"}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={() => create.mutate()} disabled={!slug || create.isPending}>
            {create.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
            Create brain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ------------------------------------------------------------ delete modal -- */

function DeleteBrainDialog({ namespace, onClose }: { namespace: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [typed, setTyped] = useState("");
  const del = useMutation({
    mutationFn: () => brainApi.deleteBrain({ namespace, confirm: namespace }),
    onSuccess: (res) => {
      if (res.error) return;
      qc.invalidateQueries({ queryKey: ["brain", "namespaces"] });
      qc.invalidateQueries({ queryKey: ["brain", "stats"] });
      onClose();
    },
  });
  const match = typed === namespace;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-destructive">Delete brain</DialogTitle>
          <DialogDescription>
            This permanently removes every memory in <code className="font-mono text-foreground">{namespace}</code>. This cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <label className="text-sm text-muted-foreground">
            Type <span className="font-mono font-medium text-foreground">{namespace}</span> to confirm.
          </label>
          <Input autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={namespace} />
          {del.data?.error && (
            <p className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {del.data.error.code}: {del.data.error.message}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button variant="destructive" onClick={() => del.mutate()} disabled={!match || del.isPending}>
            {del.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
            Delete brain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ---------------------------------------------------------- row-level menu -- */

function BrainActions({ namespace, onLaunch, onDelete }: { namespace: string; onLaunch: () => void; onDelete: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="relative z-20 h-8 w-8 shrink-0" aria-label={`Actions for ${namespace}`} onClick={(e) => e.preventDefault()}>
          <MoreHorizontal className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="z-30">
        <DropdownMenuItem onSelect={onLaunch}><Rocket className="h-4 w-4" /> Launch session</DropdownMenuItem>
        <DropdownMenuItem asChild>
          <a href={brainApi.exportUrl(namespace)}><Download className="h-4 w-4" /> Export JSONL</a>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onDelete} className="text-destructive focus:text-destructive"><Trash2 className="h-4 w-4" /> Delete brain</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function TypeTags({ detail }: { detail?: BrainDetail }) {
  if (!detail) return <Skeleton className="h-5 w-40" />;
  const types = Object.entries(detail.types).sort((a, b) => b[1] - a[1]);
  if (types.length === 0) return <span className="text-xs text-muted-foreground">No memory types yet</span>;
  const top = types.slice(0, 3);
  const rest = types.length - top.length;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {top.map(([t, n]) => (
        <Badge key={t} variant="secondary" className="font-normal">
          {t}<span className="ms-1 tabular-nums opacity-60">{nf(n)}</span>
        </Badge>
      ))}
      {rest > 0 && <span className="text-xs text-muted-foreground">+{rest} more</span>}
    </div>
  );
}

/* ------------------------------------------------------------- brain cell -- */

function useDetail(namespace: string) {
  const q = useQuery({ queryKey: ["brain", "detail", namespace], queryFn: () => brainApi.brainDetail(namespace) });
  return q.data && !q.data.error ? (q.data as BrainDetail) : undefined;
}

function Metric({ value, label }: { value: string; label: string }) {
  return (
    <div className="min-w-0">
      <div className="truncate text-lg font-semibold tabular-nums leading-tight text-foreground">{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  );
}

function BrainCard({ b, onLaunch, onDelete }: { b: NamespaceInfo; onLaunch: () => void; onDelete: () => void }) {
  const d = useDetail(b.namespace);
  return (
    <div className="group relative flex flex-col rounded-xl border border-border bg-card p-4 transition-colors hover:border-primary/40 focus-within:border-primary/50">
      <Link
        to="/b/$namespace" params={{ namespace: b.namespace }}
        aria-label={`Open ${b.namespace}`}
        className="absolute inset-0 z-0 rounded-xl focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      />
      <div className="pointer-events-none relative z-10 flex items-start gap-3">
        <BrainAvatar namespace={b.namespace} size={40} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-semibold text-foreground group-hover:text-primary">{b.namespace}</div>
          <div className="text-xs text-muted-foreground">Updated {relTime(b.lastAt)}</div>
        </div>
        <div className="pointer-events-auto"><BrainActions namespace={b.namespace} onLaunch={onLaunch} onDelete={onDelete} /></div>
      </div>

      <div className="pointer-events-none relative z-10 mt-4 grid grid-cols-3 gap-2">
        <Metric value={nf(b.memories)} label="memories" />
        <Metric value={d ? nf(d.recalls) : "—"} label="recalls" />
        <Metric value={d ? nf(Object.keys(d.types).length) : "—"} label="types" />
      </div>

      <div className="pointer-events-none relative z-10 mt-3 min-h-[1.75rem]"><TypeTags detail={d} /></div>

      <div className="pointer-events-none relative z-10 mt-4 flex items-center justify-between border-t border-border pt-3">
        {d && d.openGaps > 0
          ? <StatusBadge tone="warning"><HelpCircle className="h-3 w-3" /> {d.openGaps} gaps</StatusBadge>
          : <span className="text-xs text-muted-foreground">No open gaps</span>}
        <span className="pointer-events-none inline-flex items-center gap-1 text-sm font-medium text-muted-foreground group-hover:text-primary">
          Open <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
        </span>
      </div>
    </div>
  );
}

function RowStat({ value, label }: { value: string; label: string }) {
  return (
    <div className="text-right">
      <div className="text-sm font-semibold tabular-nums text-foreground">{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  );
}

function BrainRow({ b, onLaunch, onDelete }: { b: NamespaceInfo; onLaunch: () => void; onDelete: () => void }) {
  const d = useDetail(b.namespace);
  return (
    <div className="group relative flex items-center gap-3 rounded-lg border border-border bg-card px-3 py-2.5 transition-colors hover:border-primary/40">
      <Link to="/b/$namespace" params={{ namespace: b.namespace }} aria-label={`Open ${b.namespace}`} className="absolute inset-0 z-0 rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring" />
      <div className="pointer-events-none"><BrainAvatar namespace={b.namespace} size={34} /></div>
      <div className="pointer-events-none min-w-0 flex-1">
        <div className="truncate font-medium text-foreground group-hover:text-primary">{b.namespace}</div>
        <div className="truncate text-xs text-muted-foreground">Updated {relTime(b.lastAt)}</div>
      </div>
      <div className="pointer-events-none hidden items-center gap-6 sm:flex">
        <RowStat value={nf(b.memories)} label="memories" />
        <RowStat value={d ? nf(d.recalls) : "—"} label="recalls" />
        {d && d.openGaps > 0 && <StatusBadge tone="warning"><HelpCircle className="h-3 w-3" /> {d.openGaps}</StatusBadge>}
      </div>
      <div className="relative z-20"><BrainActions namespace={b.namespace} onLaunch={onLaunch} onDelete={onDelete} /></div>
    </div>
  );
}

/* ----------------------------------------------------------------- the hub -- */

type Sort = "recent" | "name" | "memories";
type ViewMode = "grid" | "list";

export function BrainsHub() {
  const q = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, refetchInterval: 15_000 });
  const stats = useQuery({ queryKey: ["brain", "stats"], queryFn: brainApi.stats, refetchInterval: 15_000 });
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<Sort>("recent");
  const [view, setView] = useState<ViewMode>("grid");
  const [deleteNs, setDeleteNs] = useState<string | null>(null);
  const [launchNs, setLaunchNs] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const brains = useMemo(() => {
    let rows = q.data?.brains ?? [];
    const term = search.trim().toLowerCase();
    if (term) rows = rows.filter((b) => b.namespace.toLowerCase().includes(term));
    return [...rows].sort((a, b) => {
      if (sort === "name") return a.namespace.localeCompare(b.namespace);
      if (sort === "memories") return b.memories - a.memories;
      return new Date(b.lastAt || 0).getTime() - new Date(a.lastAt || 0).getTime();
    });
  }, [q.data, search, sort]);

  const s = stats.data;
  const loading = q.isLoading;

  return (
    <div className="mx-auto w-full max-w-6xl space-y-5 p-4 sm:p-6">
      <PageHeader
        title="Brains"
        description="Each brain is an isolated memory namespace. Open one to explore its graph, sessions, sources and gaps."
        icon={<Boxes className="h-5 w-5" />}
        actions={<Button onClick={() => setCreating(true)}><Plus className="h-4 w-4" /> New brain</Button>}
      />

      {/* KPI strip — restrained stat tiles, wraps on mobile */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard tone="info" label="Brains" value={loading ? "—" : nf(s?.brains ?? brains.length)} />
        <StatCard label="Memories" value={loading ? "—" : nf(s?.memories)} />
        <StatCard label="Graph nodes" value={loading ? "—" : nf(s?.entities)} />
        <StatCard tone="success" label="Recalls · 24h" value={loading ? "—" : nf(s?.recalls24h)} />
        <StatCard tone={s?.openGaps ? "warning" : "muted"} label="Open gaps" value={loading ? "—" : nf(s?.openGaps)} />
      </div>

      {/* Toolbar — search + sort + view, stacks on mobile */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="relative w-full sm:max-w-xs">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search brains…" className="ps-9" />
        </div>
        <div className="flex items-center gap-2">
          <Select value={sort} onValueChange={(v) => setSort(v as Sort)}>
            <SelectTrigger className="w-[160px]"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="recent">Recently updated</SelectItem>
              <SelectItem value="name">Name (A–Z)</SelectItem>
              <SelectItem value="memories">Most memories</SelectItem>
            </SelectContent>
          </Select>
          <ToggleGroup type="single" value={view} onValueChange={(v) => v && setView(v as ViewMode)} className="hidden sm:flex">
            <ToggleGroupItem value="grid" aria-label="Grid view"><LayoutGrid className="h-4 w-4" /></ToggleGroupItem>
            <ToggleGroupItem value="list" aria-label="List view"><List className="h-4 w-4" /></ToggleGroupItem>
          </ToggleGroup>
        </div>
      </div>

      {/* Content */}
      {loading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-44 rounded-xl" />)}
        </div>
      ) : brains.length === 0 ? (
        <EmptyState
          icon={<Boxes className="h-6 w-6" />}
          title={search ? "No brains match your search" : "No brains yet"}
          description={search ? "Try a different name." : "A brain forms the moment an agent retains a memory into a namespace — or create one now."}
          action={!search ? <Button onClick={() => setCreating(true)}><Plus className="h-4 w-4" /> New brain</Button> : undefined}
        />
      ) : view === "grid" ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {brains.map((b) => <BrainCard key={b.namespace} b={b} onLaunch={() => setLaunchNs(b.namespace)} onDelete={() => setDeleteNs(b.namespace)} />)}
        </div>
      ) : (
        <div className="space-y-2">
          {brains.map((b) => <BrainRow key={b.namespace} b={b} onLaunch={() => setLaunchNs(b.namespace)} onDelete={() => setDeleteNs(b.namespace)} />)}
        </div>
      )}

      {/* Admin pointer */}
      <div className="flex flex-wrap items-center gap-1.5 rounded-lg border border-border bg-muted/40 px-4 py-3 text-xs text-muted-foreground">
        <Lock className="h-3.5 w-3.5" /> Tokens, users and cross-brain access controls live under
        <Link to="/admin/users" className="font-medium text-primary hover:underline">Admin</Link>.
        <span className="ms-auto hidden items-center gap-3 sm:flex">
          <span className="inline-flex items-center gap-1"><Waypoints className="h-3.5 w-3.5" /> {nf(s?.entities)} nodes</span>
          <span className="inline-flex items-center gap-1"><Activity className="h-3.5 w-3.5" /> {nf(s?.sessions24h)} sessions · 24h</span>
        </span>
      </div>

      <NewBrainDialog open={creating} onClose={() => setCreating(false)} />
      {deleteNs && <DeleteBrainDialog namespace={deleteNs} onClose={() => setDeleteNs(null)} />}
      {launchNs && <LaunchSessionModal namespace={launchNs} onClose={() => setLaunchNs(null)} />}
    </div>
  );
}
