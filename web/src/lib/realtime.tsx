import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { subscribeBrainEvents, type BrainEventName } from "./brain";

type LiveStatus = "connecting" | "live" | "down";

const RealtimeContext = createContext<LiveStatus>("connecting");

/** Live-connection status for the "live" indicator. */
export function useLiveStatus(): LiveStatus {
  return useContext(RealtimeContext);
}

// Which TanStack query families to refetch for each SSE event. Keys are matched
// by prefix so every variant (e.g. ["brain","gaps","open"]) is invalidated.
const INVALIDATE: Record<BrainEventName, string[][]> = {
  retain: [["brain", "stats"], ["brain", "activity"], ["brain", "namespaces"], ["brain", "graph"]],
  recall: [["brain", "stats"], ["brain", "activity"]],
  search: [["brain", "stats"], ["brain", "activity"]],
  gap: [["brain", "gaps"], ["brain", "stats"]],
  grant: [["brain", "tokens"]],
  brain: [["brain", "namespaces"], ["brain", "stats"], ["brain", "graph"], ["brain", "detail"]],
};

/**
 * Single shared EventSource for the whole console. On each brain event it
 * invalidates the relevant TanStack queries so every page live-updates across
 * users / MCP clients, and exposes connection status via useLiveStatus().
 */
export function RealtimeProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [status, setStatus] = useState<LiveStatus>("connecting");
  // Keep the latest client without re-subscribing the stream on every render.
  const qcRef = useRef(qc);
  qcRef.current = qc;

  useEffect(() => {
    const stop = subscribeBrainEvents({
      onOpen: () => setStatus("live"),
      onError: () => setStatus("down"),
      onEvent: (name) => {
        setStatus("live");
        for (const key of INVALIDATE[name] ?? []) {
          qcRef.current.invalidateQueries({ queryKey: key });
        }
      },
    });
    return stop;
  }, []);

  return <RealtimeContext.Provider value={status}>{children}</RealtimeContext.Provider>;
}

/** Small "live" pill — green pulse when the SSE stream is connected. */
export function LiveIndicator() {
  const status = useLiveStatus();
  const map: Record<LiveStatus, { dot: string; label: string; text: string }> = {
    live: { dot: "bg-emerald-500", label: "Live", text: "text-emerald-500" },
    connecting: { dot: "bg-amber-500", label: "Connecting", text: "text-amber-500" },
    down: { dot: "bg-muted-foreground", label: "Offline", text: "text-muted-foreground" },
  };
  const s = map[status];
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-2 py-0.5 text-xs font-medium"
      title={`Realtime: ${s.label}`}
    >
      <span className="relative flex h-2 w-2">
        {status === "live" && (
          <span className={`absolute inline-flex h-full w-full animate-ping rounded-full ${s.dot} opacity-60`} />
        )}
        <span className={`relative inline-flex h-2 w-2 rounded-full ${s.dot}`} />
      </span>
      <span className={s.text}>{s.label}</span>
    </span>
  );
}
