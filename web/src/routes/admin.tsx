import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Table2 } from "lucide-react";
import { PageHeader, Card } from "@togo-framework/ui";
import { metaResources } from "../lib/admin";

export function AdminHome() {
  const nav = useNavigate();
  const [list, setList] = useState<{ name: string; table: string }[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => { metaResources().then(setList).finally(() => setLoading(false)); }, []);
  return (
    <div className="mx-auto max-w-5xl p-8">
      <PageHeader title="Admin" description={`Manage your resources · ${list.length}`} />
      {loading ? <p className="text-muted-foreground">Loading…</p> : list.length === 0 ? (
        <Card className="p-5"><p className="text-muted-foreground">No resources yet — run `togo make:resource Post title:string` and they'll appear here.</p></Card>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((r) => (
            <button key={r.table} onClick={() => nav({ to: "/admin/$resource", params: { resource: r.table } })} className="text-start">
              <Card className="p-5 transition hover:border-primary/50">
                <div className="flex items-center gap-3">
                  <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 text-primary"><Table2 className="h-4 w-4" /></span>
                  <span><span className="block font-medium capitalize">{r.name || r.table}</span><span className="block text-xs text-muted-foreground">/api/{r.table}</span></span>
                </div>
              </Card>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
