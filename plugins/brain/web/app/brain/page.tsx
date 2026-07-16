"use client";

import { useEffect, useState } from "react";

export default function CabrainPage() {
  const [status, setStatus] = useState("…");
  useEffect(() => {
    fetch("/api/cabrain/ping").then((r) => r.json()).then((d) => setStatus(d.status)).catch(() => setStatus("error"));
  }, []);
  return (
    <div className="mx-auto max-w-xl p-8">
      <h1 className="text-2xl font-semibold">Cabrain</h1>
      <p className="mt-2 text-slate-500">Backend status: {status}</p>
    </div>
  );
}
