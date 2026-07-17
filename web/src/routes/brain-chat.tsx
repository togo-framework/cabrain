import { useMemo, useRef, useState, useEffect } from "react";
import { useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { MessagesSquare, Send, Sparkles, FileText, Clock, Cpu, Layers, AlertTriangle } from "lucide-react";
import { brainApi, type ChatTurn, type ChatAnswer, type Recalled } from "../lib/brain";
import { SynapseField } from "../components/neural";

// A rendered turn: user text, or an assistant answer with its provenance.
type Msg =
  | { role: "user"; content: string }
  | { role: "assistant"; content: string; answer?: ChatAnswer; loading?: boolean; error?: string };

/** Inline [n] citation chips → scroll/expand the matching source below. */
function renderWithCitations(text: string, onCite: (n: number) => void) {
  const parts = text.split(/(\[\d+\])/g);
  return parts.map((p, i) => {
    const m = p.match(/^\[(\d+)\]$/);
    if (m) {
      const n = Number(m[1]);
      return (
        <button
          key={i}
          onClick={() => onCite(n)}
          className="mx-0.5 inline-flex h-5 min-w-5 items-center justify-center rounded bg-primary/15 px-1 text-xs font-semibold text-primary align-baseline hover:bg-primary/25"
          title={`Source ${n}`}
        >
          {n}
        </button>
      );
    }
    return <span key={i}>{p}</span>;
  });
}

/** Provenance footer for an assistant answer: footprint + expandable citations. */
function Provenance({ answer, focusCite }: { answer: ChatAnswer; focusCite: number | null }) {
  const fp = answer.footprint;
  return (
    <div className="mt-3 space-y-2">
      {/* Footprint — the auditable trace (Shape-of-AI: Footprints) */}
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5"><Layers className="h-3 w-3" /> {fp.recalled} memories</span>
        <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5"><Cpu className="h-3 w-3" /> {fp.model}</span>
        <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5"><Clock className="h-3 w-3" /> {(fp.latencyMs / 1000).toFixed(1)}s</span>
        {!fp.grounded && (
          <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-amber-500"><AlertTriangle className="h-3 w-3" /> no matching memory</span>
        )}
      </div>
      {/* Citations (Shape-of-AI: Citations / References) */}
      {answer.citations.length > 0 && (
        <details open className="rounded-lg border border-border bg-background/60">
          <summary className="cursor-pointer px-3 py-2 text-xs font-medium text-muted-foreground">
            {answer.citations.length} cited memories
          </summary>
          <div className="space-y-1.5 px-3 pb-3">
            {answer.citations.map((c: Recalled, i) => (
              <div
                key={c.id}
                id={`cite-${i + 1}`}
                className={`rounded-lg border p-2.5 text-xs transition ${focusCite === i + 1 ? "border-primary bg-primary/5" : "border-border bg-card"}`}
              >
                <div className="mb-1 flex items-center gap-1.5 text-muted-foreground">
                  <span className="flex h-4 min-w-4 items-center justify-center rounded bg-primary/15 px-1 font-semibold text-primary">{i + 1}</span>
                  <FileText className="h-3 w-3" />
                  <span>{c.network}·{c.memoryType}</span>
                  {c.sourceKind && <span>· {c.sourceKind}{c.sourceRef ? `/${c.sourceRef}` : ""}</span>}
                  <span className="ms-auto tabular-nums">score {c.score.toFixed(3)}</span>
                </div>
                <div className="text-foreground/90">{c.content}</div>
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

export function BrainChat() {
  const { namespace } = useParams({ strict: false }) as { namespace: string };
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [focusCite, setFocusCite] = useState<number | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Suggestions from the brain's graph (Shape-of-AI: Suggestions / Initial CTA) —
  // pick a few named entities/ventures so the blank canvas is never empty.
  const graph = useQuery({ queryKey: ["brain", "graph", namespace], queryFn: () => brainApi.graph(namespace, 60) });
  const suggestions = useMemo(() => {
    const nodes = (graph.data?.nodes ?? []).filter((n) => !["root", "type"].includes(n.group ?? ""));
    const names = Array.from(new Set(nodes.map((n) => n.name).filter(Boolean)));
    return names.slice(0, 4).map((n) => `Tell me about ${n}`);
  }, [graph.data]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" });
  }, [msgs]);

  // Reset the conversation when the brain changes.
  useEffect(() => { setMsgs([]); setInput(""); }, [namespace]);

  const send = async (text: string) => {
    const q = text.trim();
    if (!q || busy) return;
    setInput("");
    setBusy(true);
    const history: ChatTurn[] = msgs
      .filter((m): m is Extract<Msg, { role: "user" }> | Extract<Msg, { role: "assistant" }> => true)
      .map((m) => ({ role: m.role, content: m.content }));
    setMsgs((prev) => [...prev, { role: "user", content: q }, { role: "assistant", content: "", loading: true }]);
    try {
      const r = await brainApi.chat({ namespace, message: q, history });
      setMsgs((prev) => {
        const next = [...prev];
        const last = next[next.length - 1];
        if (last && last.role === "assistant") {
          if (r.error) next[next.length - 1] = { role: "assistant", content: "", error: `${r.error.code}: ${r.error.message}` };
          else next[next.length - 1] = { role: "assistant", content: r.answer, answer: r };
        }
        return next;
      });
    } catch (e: any) {
      setMsgs((prev) => {
        const next = [...prev];
        next[next.length - 1] = { role: "assistant", content: "", error: String(e?.message ?? e) };
        return next;
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-full flex-col">
      {/* Scrollable transcript */}
      <div ref={scrollRef} className="flex-1 overflow-auto">
        <div className="mx-auto max-w-3xl space-y-4 p-6">
          {msgs.length === 0 ? (
            // Initial CTA — inviting, brain-branded
            <div className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-indigo-500/10 via-violet-500/5 to-teal-400/10 p-8 text-center">
              <SynapseField className="opacity-30" />
              <div className="relative mx-auto max-w-md">
                <span className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15 text-primary"><MessagesSquare className="h-6 w-6" /></span>
                <h2 className="text-lg font-semibold text-foreground">Ask the <span className="text-primary">{namespace}</span> brain</h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  A live agent grounded only in this brain's memories — every answer cites the memories it used.
                </p>
                {suggestions.length > 0 && (
                  <div className="mt-4 flex flex-wrap justify-center gap-2">
                    {suggestions.map((s) => (
                      <button key={s} onClick={() => send(s)}
                        className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1.5 text-xs text-foreground transition hover:border-primary/50 hover:text-primary">
                        <Sparkles className="h-3 w-3 text-primary" /> {s}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          ) : (
            msgs.map((m, i) =>
              m.role === "user" ? (
                <div key={i} className="flex justify-end">
                  <div className="max-w-[85%] rounded-2xl rounded-br-sm bg-primary px-4 py-2.5 text-sm text-primary-foreground">{m.content}</div>
                </div>
              ) : (
                <div key={i} className="flex justify-start">
                  <div className="max-w-[90%] rounded-2xl rounded-bl-sm border border-border bg-card px-4 py-3">
                    {m.loading ? (
                      <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <span className="flex gap-1">
                          <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-primary [animation-delay:-0.3s]" />
                          <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-primary [animation-delay:-0.15s]" />
                          <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-primary" />
                        </span>
                        recalling memories…
                      </div>
                    ) : m.error ? (
                      <div className="text-sm text-rose-400">{m.error}</div>
                    ) : (
                      <>
                        <div className="whitespace-pre-wrap text-sm text-foreground">
                          {renderWithCitations(m.content, (n) => {
                            setFocusCite(n);
                            document.getElementById(`cite-${n}`)?.scrollIntoView({ behavior: "smooth", block: "center" });
                          })}
                        </div>
                        {m.answer && <Provenance answer={m.answer} focusCite={focusCite} />}
                      </>
                    )}
                  </div>
                </div>
              )
            )
          )}
        </div>
      </div>

      {/* Composer */}
      <div className="border-t border-border bg-background/80 p-4 backdrop-blur">
        <div className="mx-auto flex max-w-3xl items-end gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(input); } }}
            rows={1}
            placeholder={`Ask the ${namespace} brain…`}
            className="max-h-40 min-h-[44px] flex-1 resize-none rounded-xl border border-border bg-card px-4 py-2.5 text-sm outline-none focus:border-primary"
          />
          <button
            onClick={() => send(input)}
            disabled={busy || !input.trim()}
            className="inline-flex h-11 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition hover:opacity-90 disabled:opacity-40"
          >
            <Send className="h-4 w-4" /> Ask
          </button>
        </div>
        <p className="mx-auto mt-1.5 max-w-3xl text-center text-xs text-muted-foreground">
          Answers are grounded only in this brain's memories and cite their sources.
        </p>
      </div>
    </div>
  );
}
