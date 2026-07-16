import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { CheckCircle2, AlertCircle, X } from "lucide-react";

type Toast = { id: number; kind: "success" | "error"; msg: string };
type Ctx = { toast: (msg: string, kind?: "success" | "error") => void };

const ToastCtx = createContext<Ctx>({ toast: () => {} });
export const useToast = () => useContext(ToastCtx);

let seq = 0;

/** Lightweight toast provider — confirmation/error feedback for admin actions.
 * The kit ships no toast, so this stays self-contained (tokens + RTL aware). */
export function ToastProvider({ children, dir = "ltr" }: { children: ReactNode; dir?: "ltr" | "rtl" }) {
  const [items, setItems] = useState<Toast[]>([]);
  const toast = useCallback((msg: string, kind: "success" | "error" = "success") => {
    const id = ++seq;
    setItems((s) => [...s, { id, kind, msg }]);
    setTimeout(() => setItems((s) => s.filter((t) => t.id !== id)), 4000);
  }, []);
  const dismiss = (id: number) => setItems((s) => s.filter((t) => t.id !== id));
  return (
    <ToastCtx.Provider value={{ toast }}>
      {children}
      <div dir={dir} className="pointer-events-none fixed bottom-4 z-[100] flex flex-col gap-2 px-4" style={{ [dir === "rtl" ? "left" : "right"]: 0 }}>
        {items.map((t) => (
          <div key={t.id} role="status"
            className="pointer-events-auto flex items-center gap-2 rounded-lg border border-border bg-card px-3.5 py-2.5 text-sm shadow-lg">
            {t.kind === "success" ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <AlertCircle className="h-4 w-4 text-destructive" />}
            <span className="font-medium">{t.msg}</span>
            <button onClick={() => dismiss(t.id)} className="ms-2 text-muted-foreground hover:text-foreground"><X className="h-3.5 w-3.5" /></button>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
