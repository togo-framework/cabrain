// Same-origin by default: dev uses the vite proxy (vite.config.ts), prod is served by the Go backend.
export const API = import.meta.env.VITE_API_ORIGIN ?? "";
export const APP_NAME = import.meta.env.VITE_APP_NAME ?? "togo";
